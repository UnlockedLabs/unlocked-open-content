// Package opencontent is Open Content: libraries (Kiwix), videos, helpful
// links and the content proxies. It was a module in core until EN-205.
package opencontent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
		client: &http.Client{},
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
	mux.Handle("/internal/v1/open-content/", api.NewHandler(NewAPI(db), token))
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
