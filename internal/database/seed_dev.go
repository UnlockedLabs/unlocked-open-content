package database

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// SeedDev fills a local database with sample Open Content data for core's
// residents: 25 videos, favorites, and three months of reading and viewing
// activity, against the videos and whatever libraries the Kiwix scrape has
// added. It moved from core's dev seeder (`make seed`), which runs it after
// adding core's facilities and users. Each run adds more; a failed run adds
// nothing.
func (db *DB) SeedDev(ctx context.Context) error {
	if err := seedDev(ctx, db.DB, time.Now()); err != nil {
		return err
	}
	log.Info("Seeded the sample Open Content data")
	return nil
}

// devUser is a core user the sample data is for.
type devUser struct {
	ID         uint
	Role       string
	FacilityID uint
}

// seedDev adds the sample data in one transaction, so a failed run adds
// nothing.
func seedDev(ctx context.Context, db *gorm.DB, now time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var residents []devUser
		if err := tx.Table("users").Select("id, role, facility_id").Where("role = ? AND deleted_at IS NULL", "student").Scan(&residents).Error; err != nil {
			return fmt.Errorf("find residents: %w", err)
		}
		if len(residents) == 0 {
			return errors.New("no residents: run core's seeder first")
		}
		var batchUserID uint
		if err := tx.Table("users").Select("id").Where("username = ?", "system_batch").Limit(1).Scan(&batchUserID).Error; err != nil {
			return fmt.Errorf("find the system_batch user: %w", err)
		}
		if batchUserID != 0 {
			tx = tx.WithContext(identity.NewContext(ctx, &identity.Claims{UserID: batchUserID}))
		}

		var youtube models.OpenContentProvider
		if err := tx.Where("title = ?", models.Youtube).First(&youtube).Error; err != nil {
			return fmt.Errorf("find the YouTube provider: %w", err)
		}
		videos := sampleVideos(youtube.ID, 25)
		if err := tx.Create(&videos).Error; err != nil {
			return fmt.Errorf("create videos: %w", err)
		}
		var libraries []models.Library
		if err := tx.Find(&libraries).Error; err != nil {
			return fmt.Errorf("find libraries: %w", err)
		}
		urls := make([]models.OpenContentUrl, 0, len(videos)+len(libraries))
		for _, v := range videos {
			urls = append(urls, models.OpenContentUrl{ContentURL: fmt.Sprintf("/api/proxy/videos/%d", v.ID)})
		}
		for _, lib := range libraries {
			urls = append(urls, models.OpenContentUrl{ContentURL: fmt.Sprintf("/api/proxy/libraries/%d/", lib.ID)})
		}
		if err := tx.Create(&urls).Error; err != nil {
			return fmt.Errorf("create content urls: %w", err)
		}

		for _, user := range residents {
			if err := tx.Create(sampleFavorites(user, libraries, videos, urls, now)).Error; err != nil {
				return fmt.Errorf("create favorites for user %d: %w", user.ID, err)
			}
			activity := sampleActivity(user, libraries, videos, urls, now)
			if len(activity) == 0 {
				continue
			}
			if err := tx.CreateInBatches(activity, 500).Error; err != nil {
				return fmt.Errorf("create activity for user %d: %w", user.ID, err)
			}
		}
		return nil
	})
}

func sampleVideos(providerID uint, count int) []models.Video {
	videos := make([]models.Video, 0, count)
	for i := range count {
		id := rand.Intn(999999)
		videos = append(videos, models.Video{
			ExternalID:            fmt.Sprintf("vid-%d-%d", id, i),
			Url:                   fmt.Sprintf("https://video.example.com/watch?v=%d-%d", id, i),
			Title:                 fmt.Sprintf("Sample video %d", id),
			Availability:          models.VideoAvailable,
			ChannelTitle:          models.StringPtr(fmt.Sprintf("Sample channel %d", rand.Intn(10)+1)),
			Duration:              rand.Intn(7200) + 60, // 1 to 120 minutes
			Description:           models.StringPtr("A sample video for local development."),
			ThumbnailUrl:          models.StringPtr(fmt.Sprintf("https://img.example.com/thumbs/%d.jpg", id)),
			OpenContentProviderID: providerID,
		})
	}
	return videos
}

// sampleFavorites is 3 to 10 favorites, each a library (with its proxy URL)
// or a video.
func sampleFavorites(user devUser, libraries []models.Library, videos []models.Video, urls []models.OpenContentUrl, now time.Time) []models.OpenContentFavorite {
	count := rand.Intn(8) + 3
	favorites := make([]models.OpenContentFavorite, 0, count)
	for range count {
		created := now.AddDate(0, 0, -rand.Intn(90))
		if rand.Intn(2) == 0 && len(libraries) > 0 {
			lib := libraries[rand.Intn(len(libraries))]
			favorites = append(favorites, models.OpenContentFavorite{
				UserID:                user.ID,
				ContentID:             lib.ID,
				OpenContentProviderID: lib.OpenContentProviderID,
				OpenContentUrlID:      urlID(urls, fmt.Sprintf("/api/proxy/libraries/%d/", lib.ID)),
				Name:                  models.StringPtr(lib.Title),
				CreatedAt:             created,
			})
			continue
		}
		vid := videos[rand.Intn(len(videos))]
		favorites = append(favorites, models.OpenContentFavorite{
			UserID:                user.ID,
			ContentID:             vid.ID,
			OpenContentProviderID: vid.OpenContentProviderID,
			Name:                  models.StringPtr(vid.Title),
			CreatedAt:             created,
		})
	}
	return favorites
}

// sampleActivity is up to 20 sessions with each video and library, at random
// times in the last three months, each up to six hours long.
func sampleActivity(user devUser, libraries []models.Library, videos []models.Video, urls []models.OpenContentUrl, now time.Time) []models.OpenContentActivity {
	start := now.AddDate(0, -3, 0)
	days := int(now.Sub(start).Hours() / 24)
	var activity []models.OpenContentActivity
	add := func(providerID, contentID uint) {
		for range rand.Intn(20) {
			if rand.Intn(3) != 0 {
				continue
			}
			day := start.AddDate(0, 0, rand.Intn(days))
			requested := time.Date(day.Year(), day.Month(), day.Day(), rand.Intn(23), rand.Intn(60), rand.Intn(60), 0, time.UTC)
			activity = append(activity, models.OpenContentActivity{
				OpenContentProviderID: providerID,
				FacilityID:            user.FacilityID,
				UserID:                user.ID,
				ContentID:             contentID,
				OpenContentUrlID:      urls[rand.Intn(len(urls))].ID,
				RequestTS:             requested,
				StopTS:                requested.Add(time.Duration(rand.Intn(360)) * time.Minute),
			})
		}
	}
	for _, v := range videos {
		add(v.OpenContentProviderID, v.ID)
	}
	for _, lib := range libraries {
		add(lib.OpenContentProviderID, lib.ID)
	}
	return activity
}

// urlID is the ID of the content URL url, if there is one.
func urlID(urls []models.OpenContentUrl, url string) *uint {
	for i := range urls {
		if urls[i].ContentURL == url {
			return &urls[i].ID
		}
	}
	return nil
}
