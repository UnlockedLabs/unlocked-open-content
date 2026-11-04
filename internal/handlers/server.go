// Package handlers is Open Content's HTTP API: libraries (Kiwix), videos,
// helpful links and the content proxies, and its answers to core's questions.
// It was a module in core until EN-205.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// server is Open Content's routes and what they use. The clients that need
// a running service are nil in tests: S3 without S3_BUCKET_NAME, the library
// cache and job publishing without NATS.
type server struct {
	db     *database.DB
	notify notifier
	nats   *nats.Conn
	// libraryPaths caches each library's proxy target (LibraryProxyPO) at
	// each facility.
	libraryPaths nats.KeyValue
	sesClient    *sesv2.Client
	presigner    *s3.PresignClient
	s3Bucket     string
	// client calls Kiwix's search and Google's suggestions.
	client *http.Client
	dev    bool
	// helpfulSortOrder is each facility's helpful-links sort order.
	helpfulSortOrder sortOrders
}

// clientTimeout caps each call client makes, so a slow Kiwix or Google can't
// hold a request open.
const clientTimeout = 30 * time.Second

// NewHandler serves Open Content's routes, each behind requireLogin and its
// access checks, its metrics on /api/metrics for Prometheus, and its answers
// to core's questions on /internal/v1/open-content/, which the ingress never
// routes, to callers with token, the site's secret core also has. A failure to
// reach AWS or NATS is an error.
func NewHandler(ctx context.Context, db *database.DB, nc *nats.Conn, requireLogin func(http.Handler) http.Handler, token string) (http.Handler, error) {
	srv := &server{
		db:     db,
		notify: natsNotifier{nc},
		nats:   nc,
		client: &http.Client{Timeout: clientTimeout},
		dev:    os.Getenv("APP_ENV") == "dev",
	}
	if err := srv.connectAWS(ctx); err != nil {
		return nil, err
	}
	kv, err := libraryPathsBucket(nc)
	if err != nil {
		return nil, fmt.Errorf("set up the %s bucket: %w", libraryPaths, err)
	}
	srv.libraryPaths = kv

	mux := http.NewServeMux()
	mux.Handle("/api/", newRouter(srv.routes(), requireLogin))
	mux.Handle("GET /api/metrics", promhttp.Handler())
	mux.Handle("/internal/v1/open-content/", api.NewHandler(newAPI(db), token))
	return mux, nil
}

// connectAWS sets up email for content requests and, when S3_BUCKET_NAME is
// set, the presigned video links.
func (srv *server) connectAWS(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	srv.sesClient = sesv2.NewFromConfig(cfg)
	if bucket := os.Getenv("S3_BUCKET_NAME"); bucket != "" {
		srv.presigner = s3.NewPresignClient(s3.NewFromConfig(cfg))
		srv.s3Bucket = bucket
	}
	return nil
}

// libraryPaths is the NATS key-value bucket that caches each library's proxy
// target.
const libraryPaths = "library_paths"

// libraryPathsBucket opens the library_paths bucket, creating it on first
// start.
func libraryPathsBucket(nc *nats.Conn) (nats.KeyValue, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("initialize JetStream: %w", err)
	}
	kv, err := js.KeyValue(libraryPaths)
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{Bucket: libraryPaths, History: 1, TTL: 24 * time.Hour})
}

// Websocket events, as the frontend knows them.
const (
	visitEvent    = "visits"
	bookmarkEvent = "bookmarks"
)

// notifier sends a message to a user's open browser tabs. A user with no open
// tab misses it.
type notifier interface {
	Notify(n api.Notification)
}

// natsNotifier hands each message to core over NATS, for core to send over
// its websocket.
type natsNotifier struct{ nc *nats.Conn }

func (n natsNotifier) Notify(msg api.Notification) {
	data, err := json.Marshal(msg)
	if err == nil {
		err = n.nc.Publish(api.NotifySubject, data)
	}
	if err != nil {
		logrus.WithFields(logrus.Fields{"user_id": msg.UserID, "event": msg.Event}).Errorf("notify user: %v", err)
	}
}

// createContentActivityAndNotifyWS records a visit and tells the user's open
// tab: the activity, so the tab can end it, and for a library page whether
// it is bookmarked.
func (srv *server) createContentActivityAndNotifyWS(urlString string, activity *models.OpenContentActivity) {
	srv.db.CreateContentActivity(urlString, activity)
	if activity.ID > 0 {
		srv.notify.Notify(api.Notification{UserID: activity.UserID, Event: visitEvent, ActivityID: activity.ID})
		if !strings.HasPrefix(urlString, "/viewer/videos") {
			msg := "false"
			if srv.db.IsBookmarked(activity) {
				msg = "true"
			}
			srv.notify.Notify(api.Notification{UserID: activity.UserID, Event: bookmarkEvent, Text: msg})
		}
	}
}

// sendEmail sends a content request to TO_EMAIL.
func (srv *server) sendEmail(ctx context.Context, subject, bodyText, bodyHTML string) error {
	charset := aws.String("UTF-8")
	input := &sesv2.SendEmailInput{
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: charset,
				},
				Body: &types.Body{
					Text: &types.Content{
						Data:    aws.String(bodyText),
						Charset: charset,
					},
					Html: &types.Content{
						Data:    aws.String(bodyHTML),
						Charset: charset,
					},
				},
			},
		},
		Destination: &types.Destination{
			ToAddresses: []string{os.Getenv("TO_EMAIL")},
		},
		FromEmailAddress: aws.String(os.Getenv("FROM_EMAIL")),
	}
	if _, err := srv.sesClient.SendEmail(ctx, input); err != nil {
		logrus.Printf("error sending email: %v\n", err)
		return fmt.Errorf("failed to send email via SES: %v", err)
	}
	return nil
}
