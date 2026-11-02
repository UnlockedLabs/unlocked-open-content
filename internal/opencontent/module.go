// Package opencontent is Open Content: libraries (Kiwix), videos, helpful
// links and the content proxies. It was a module in core until EN-205.
package opencontent

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go"
	"github.com/pressly/goose/v3"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Schema is the Postgres schema that holds Open Content's tables, and the
// role its connection runs as.
const Schema = "open_content"

// ledgerTable is the goose version table for Open Content's migrations. It is
// the one core used while Open Content was its module, so a site that already
// ran them doesn't run them again.
const ledgerTable = "public.goose_db_version_opencontent"

//go:embed migrations/*.sql
var migrations embed.FS

// NewHandler serves Open Content's routes, each behind requireLogin and its
// access checks, and its answers to core's questions on
// /internal/v1/open-content/, which the ingress never routes. A failure to
// reach AWS or NATS is an error.
func NewHandler(ctx context.Context, db *gorm.DB, nc *nats.Conn, requireLogin func(http.Handler) http.Handler) (http.Handler, error) {
	srv := &server{
		store:  &store{db: db},
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
	mux.Handle("/internal/v1/open-content/", api.NewHandler(NewAPI(db)))
	return mux, nil
}

// Work runs the worker until ctx is canceled: it runs Open Content's jobs as
// they're published, and publishes the scheduled ones on their schedules.
// Canceling ctx also stops the jobs that are running.
func Work(ctx context.Context, db *gorm.DB, nc *nats.Conn) error {
	w := &worker{store: &store{db: db}}
	if err := w.subscribe(ctx, nc); err != nil {
		return err
	}
	s, err := gocron.NewScheduler()
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}
	p := &jobPublisher{store: &store{db: db}, publish: nc.PublishMsg}
	scheduleJobs(cronScheduler{s}, p, os.Getenv("APP_ENV") == "dev")
	s.Start()
	log.Info("Worker started")

	<-ctx.Done()
	if err := s.Shutdown(); err != nil {
		return fmt.Errorf("stop scheduler: %w", err)
	}
	log.Info("Worker stopped")
	return nil
}

// Migrate runs Open Content's migrations on db, which connects as the
// database's own user, not as the open_content role. Core's migrations must
// have run first: the first one moves tables core's migrations created.
func Migrate(ctx context.Context, db *sql.DB) error {
	p, err := migrationProvider(db)
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Infof("Applied migration %s in %s", r.Source.Path, r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// migrationProvider is goose with Open Content's migrations and ledger.
func migrationProvider(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(ledgerTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return p, nil
}

// migrationPollInterval is how often WaitForMigrations checks, as core's
// does.
const migrationPollInterval = 5 * time.Second

// WaitForMigrations waits until db has every migration this image ships, so
// serve and worker never run against an older schema. It reads the ledger
// only, and waits out errors, since the ledger doesn't exist until migrate
// first runs. It returns ctx's error if ctx is canceled first.
func WaitForMigrations(ctx context.Context, db *sql.DB) error {
	p, err := migrationProvider(db)
	if err != nil {
		return err
	}
	sources := p.ListSources()
	latest := sources[len(sources)-1].Version
	for {
		var applied int64
		err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM `+ledgerTable+` WHERE is_applied`).Scan(&applied)
		switch {
		case err != nil:
			log.Warnf("Waiting for migrations: %v", err)
		case applied < latest:
			log.Infof("Waiting for migrations: at %d of %d", applied, latest)
		default:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(migrationPollInterval):
		}
	}
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

// defaultLeftMenuLinks are the helpful links a new database starts with.
// Provider 1 is the HelpfulLinks provider that core's migration 00026 creates.
const defaultLeftMenuLinks = `[{
			"title": "Unlocked Labs",
			"description": "Unlocked Labs website",
			"url": "https://unlockedlabs.org",
			"visibility_status": true,
	        "thumbnail_url": "https://unlockedlabs.org/favicon.ico",
			"open_content_provider_id": 1,
			"facility_id": 1
		},
	    {
	    "title": "Google",
	    "description": "Google search engine",
	    "url": "https://www.google.com",
	    "visibility_status": true,
	    "open_content_provider_id": 1,
	    "thumbnail_url": "https://www.google.com/favicon.ico",
	    "facility_id": 1
	    }]`

// Seed gives a new database, one that has never had a helpful link or a
// Kiwix provider, the default helpful links and the Kiwix and YouTube
// providers. Core creates its first facility, where the links are visible,
// when it first starts, which can be after migrate runs; until then Seed adds
// nothing, and a later run seeds. It adds all or nothing.
func Seed(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var links, kiwix int64
		if err := tx.Unscoped().Model(&HelpfulLink{}).Count(&links).Error; err != nil {
			return fmt.Errorf("count helpful links: %w", err)
		}
		if err := tx.Unscoped().Model(&OpenContentProvider{}).Where("title = ?", Kiwix).Count(&kiwix).Error; err != nil {
			return fmt.Errorf("count Kiwix providers: %w", err)
		}
		if links > 0 || kiwix > 0 {
			return nil
		}
		var facilityID uint
		if err := tx.Table("facilities").Select("id").Order("id").Limit(1).Scan(&facilityID).Error; err != nil {
			return fmt.Errorf("find core's first facility: %w", err)
		}
		if facilityID == 0 {
			log.Info("Not seeding yet: core hasn't created its first facility")
			return nil
		}
		// Core's seed recorded its rows as made by the system_batch user.
		var batchUserID uint
		if err := tx.Table("users").Select("id").Where("username = ?", "system_batch").Limit(1).Scan(&batchUserID).Error; err != nil {
			return fmt.Errorf("find the system_batch user: %w", err)
		}
		if batchUserID != 0 {
			tx = tx.WithContext(identity.NewContext(tx.Statement.Context, &identity.Claims{UserID: batchUserID}))
		}
		if err := seed(tx, facilityID); err != nil {
			return err
		}
		log.Infof("Seeded the default helpful links and providers, visible at facility %d", facilityID)
		return nil
	})
}

// seed adds the default helpful links, visible at facilityID, and the Kiwix
// and YouTube providers.
func seed(db *gorm.DB, facilityID uint) error {
	links := []HelpfulLink{}
	if err := json.Unmarshal([]byte(defaultLeftMenuLinks), &links); err != nil {
		return fmt.Errorf("read default left menu links: %w", err)
	}
	if err := db.Create(&links).Error; err != nil {
		return fmt.Errorf("create left menu links: %w", err)
	}
	for _, link := range links {
		if link.OpenContentProviderID == 0 {
			continue
		}
		visibility := FacilityVisibilityStatus{
			FacilityID:            facilityID,
			OpenContentProviderID: link.OpenContentProviderID,
			ContentID:             link.ID,
			VisibilityStatus:      true,
		}
		if err := db.Create(&visibility).Error; err != nil {
			return fmt.Errorf("create link visibility: %w", err)
		}
	}
	providers := []OpenContentProvider{
		{Title: stringPtr(Kiwix), Url: KiwixLibraryUrl, CurrentlyEnabled: true, ThumbnailUrl: stringPtr(KiwixThumbnailURL), Description: stringPtr(Kiwix)},
		{Title: stringPtr(Youtube), Url: YoutubeApi, CurrentlyEnabled: true, ThumbnailUrl: stringPtr(YoutubeThumbnail), Description: stringPtr(YoutubeDescription)},
	}
	for i := range providers {
		if err := db.Create(&providers[i]).Error; err != nil {
			return fmt.Errorf("create default open content providers: %w", err)
		}
	}
	return nil
}
