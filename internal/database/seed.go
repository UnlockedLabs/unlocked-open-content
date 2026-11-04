package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

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

// errNoFacility is Seed's answer before core has created its first facility.
var errNoFacility = errors.New("core hasn't created its first facility")

// seedPollInterval is how often WaitAndSeed checks for core's first facility.
const seedPollInterval = 5 * time.Second

// WaitAndSeed seeds a new database, first waiting until core has created its
// first facility, which core does when it first starts. It returns ctx's
// error if ctx is canceled first.
func (db *DB) WaitAndSeed(ctx context.Context) error {
	for {
		err := db.Seed(ctx)
		if !errors.Is(err, errNoFacility) {
			return err
		}
		log.Info("Waiting for core's first facility to seed the new database")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(seedPollInterval):
		}
	}
}

// Seed gives a new database, one that has never had a helpful link or a
// Kiwix or YouTube provider, the default helpful links, visible at core's first
// facility, and the Kiwix and YouTube providers. Until core has created that
// facility it adds nothing and returns errNoFacility. It adds all or nothing,
// and on Postgres two Seeds never run at once.
func (db *DB) Seed(ctx context.Context) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext('open_content_seed'))").Error; err != nil {
				return fmt.Errorf("lock the seed: %w", err)
			}
		}
		// Links are deleted for good, so a site can have none: its providers
		// also tell it apart from a new database.
		var links, providers int64
		if err := tx.Unscoped().Model(&models.HelpfulLink{}).Count(&links).Error; err != nil {
			return fmt.Errorf("count helpful links: %w", err)
		}
		if err := tx.Unscoped().Model(&models.OpenContentProvider{}).Where("title IN ?", []string{models.Kiwix, models.Youtube}).Count(&providers).Error; err != nil {
			return fmt.Errorf("count Kiwix and YouTube providers: %w", err)
		}
		if links > 0 || providers > 0 {
			return nil
		}
		var facilityID uint
		if err := tx.Table("facilities").Select("id").Order("id").Limit(1).Scan(&facilityID).Error; err != nil {
			return fmt.Errorf("find core's first facility: %w", err)
		}
		if facilityID == 0 {
			return errNoFacility
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
	links := []models.HelpfulLink{}
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
		visibility := models.FacilityVisibilityStatus{
			FacilityID:            facilityID,
			OpenContentProviderID: link.OpenContentProviderID,
			ContentID:             link.ID,
			VisibilityStatus:      true,
		}
		if err := db.Create(&visibility).Error; err != nil {
			return fmt.Errorf("create link visibility: %w", err)
		}
	}
	providers := []models.OpenContentProvider{
		{Title: models.StringPtr(models.Kiwix), Url: models.KiwixLibraryUrl, CurrentlyEnabled: true, ThumbnailUrl: models.StringPtr(models.KiwixThumbnailURL), Description: models.StringPtr(models.Kiwix)},
		{Title: models.StringPtr(models.Youtube), Url: models.YoutubeApi, CurrentlyEnabled: true, ThumbnailUrl: models.StringPtr(models.YoutubeThumbnail), Description: models.StringPtr(models.YoutubeDescription)},
	}
	for i := range providers {
		if err := db.Create(&providers[i]).Error; err != nil {
			return fmt.Errorf("create default open content providers: %w", err)
		}
	}
	return nil
}
