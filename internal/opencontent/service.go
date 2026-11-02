package opencontent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

// server is Open Content's routes and what they use. The clients that need
// a running service are nil in tests: S3 without S3_BUCKET_NAME, the library
// cache and job publishing without NATS.
type server struct {
	store  *store
	notify notifier
	nats   *nats.Conn
	// libraryPaths caches each library's proxy target (LibraryProxyPO) by ID.
	libraryPaths nats.KeyValue
	sesClient    *sesv2.Client
	presigner    *s3.PresignClient
	s3Bucket     string
	// client calls Kiwix's search.
	client *http.Client
	dev    bool
}

// Facility visibility

func (srv *server) setHelpfulLinkVisibility(args *queryContext, id int, facilityIDs []uint, visible bool) error {
	link, err := srv.store.GetLinkFromId(uint(id))
	if err != nil {
		return err
	}
	statuses := buildVisibilityStatuses(link.ID, link.OpenContentProviderID, facilityIDs, visible)
	return srv.store.UpsertFacilityVisibilityStatuses(args, statuses, visible)
}

func (srv *server) setVideoVisibility(args *queryContext, id int, facilityIDs []uint, visible bool) error {
	video, err := srv.store.GetVideoByID(id, args.FacilityID)
	if err != nil {
		return err
	}
	statuses := buildVisibilityStatuses(video.ID, video.OpenContentProviderID, facilityIDs, visible)
	return srv.store.UpsertFacilityVisibilityStatuses(args, statuses, visible)
}

func (srv *server) setLibraryVisibility(args *queryContext, id int, facilityIDs []uint, visible bool) (*Library, error) {
	library, err := srv.store.GetLibraryByID(id)
	if err != nil {
		return nil, err
	}
	statuses := buildVisibilityStatuses(library.ID, library.OpenContentProviderID, facilityIDs, visible)
	if err := srv.store.UpsertFacilityVisibilityStatuses(args, statuses, visible); err != nil {
		return nil, err
	}
	library.VisibilityStatus = visible
	return library, nil
}

// buildVisibilityStatuses is one status for each facility, without duplicates.
func buildVisibilityStatuses(contentID, providerID uint, facilityIDs []uint, visible bool) []FacilityVisibilityStatus {
	statuses := make([]FacilityVisibilityStatus, 0, len(facilityIDs))
	seen := make(map[uint]bool, len(facilityIDs))
	for _, facilityID := range facilityIDs {
		if seen[facilityID] {
			continue
		}
		seen[facilityID] = true
		statuses = append(statuses, FacilityVisibilityStatus{
			FacilityID:            facilityID,
			OpenContentProviderID: providerID,
			ContentID:             contentID,
			VisibilityStatus:      visible,
		})
	}
	return statuses
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
func (srv *server) createContentActivityAndNotifyWS(urlString string, activity *OpenContentActivity) {
	srv.store.CreateContentActivity(urlString, activity)
	if activity.ID > 0 {
		srv.notify.Notify(api.Notification{UserID: activity.UserID, Event: visitEvent, ActivityID: activity.ID})
		if !strings.HasPrefix(urlString, "/viewer/videos") {
			msg := "false"
			if srv.store.isBookmarked(activity) {
				msg = "true"
			}
			srv.notify.Notify(api.Notification{UserID: activity.UserID, Event: bookmarkEvent, Text: msg})
		}
	}
}

// updateLibraryBucket updates a library's cached visibility, or caches the
// library if it isn't yet.
func (srv *server) updateLibraryBucket(key string, library *Library, log sLog) {
	var proxyParams *LibraryProxyPO
	entry, err := srv.libraryPaths.Get(key)
	if err == nil {
		err = json.Unmarshal(entry.Value(), &proxyParams)
		if err != nil {
			log.warn("unable to unmarshal value from LibaryPaths bucket")
			return
		}
		proxyParams.VisibilityStatus = library.VisibilityStatus
	} else { //build a one for the bucket
		proxyParams = library.IntoProxyPO()
	}
	marshaledParams, err := json.Marshal(proxyParams)
	if err != nil {
		log.warn("unable to marshal value to put into the LibaryPaths bucket")
		return
	}
	if _, err := srv.libraryPaths.Put(key, marshaledParams); err != nil {
		log.warnf("unable to update value within LibaryPaths bucket, error is %v", err)
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
