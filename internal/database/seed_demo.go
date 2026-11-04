package database

import (
	"context"
	"math/rand"
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

// The demo site's sample activity, which core's demo seed asks for.

func (db *DB) ResetDemoActivity(ctx context.Context, before time.Time) error {
	return db.WithContext(ctx).Exec("DELETE from open_content_activities where date(request_ts) <= date(?)", before).Error
}

func (db *DB) SeedDemoActivity(ctx context.Context, facilityID uint, userIDs []uint) error {
	tx := db.WithContext(ctx)
	libraries := []models.Library{}
	if err := tx.Model(&models.Library{}).Limit(40).Find(&libraries).Error; err != nil {
		return err
	}
	contentUrls := []models.OpenContentUrl{}
	if err := tx.Model(&models.OpenContentUrl{}).Find(&contentUrls).Error; err != nil {
		return err
	}
	if len(contentUrls) == 0 {
		newContentUrls := []models.OpenContentUrl{
			{ContentURL: "/"}, {ContentURL: "/math"}, {ContentURL: "/science"}, {ContentURL: "/history"},
		}
		if err := tx.CreateInBatches(&newContentUrls, 4).Error; err != nil {
			return err
		}
		contentUrls = newContentUrls
	}
	sixMonthsAgo := time.Now().AddDate(0, -6, 0)
	for _, userID := range userIDs {
		for i := 0; i < 180; i++ {
			for _, library := range libraries {
				if i%(rand.Intn(8)+1) == 0 {
					openContentActivity := models.OpenContentActivity{
						UserID:                userID,
						RequestTS:             sixMonthsAgo.AddDate(0, 0, i).Add(time.Duration(i) * time.Minute),
						ContentID:             library.ID,
						OpenContentProviderID: library.OpenContentProviderID,
						OpenContentUrlID:      uint(rand.Intn(len(contentUrls)) + 1),
						FacilityID:            facilityID,
					}
					if err := tx.Create(&openContentActivity).Error; err != nil {
						continue
					}
				}
			}
		}
	}
	return nil
}
