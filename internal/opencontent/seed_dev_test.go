package opencontent

import (
	"fmt"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sample data is for core's residents only, and every row points at
// content that exists.
func TestSeedDev(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT NOT NULL, role TEXT NOT NULL, facility_id INTEGER, deleted_at DATETIME)`).Error)

	require.NoError(t, db.Create(&models.OpenContentProvider{Title: models.StringPtr(models.Youtube), Url: models.YoutubeApi}).Error)
	require.ErrorContains(t, seedDev(t.Context(), db, time.Now()), "run core's seeder first")
	var count int64
	require.NoError(t, db.Model(&models.Video{}).Count(&count).Error)
	require.Zero(t, count, "a failed run adds nothing")

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, role, facility_id) VALUES
		(1, 'admin', 'system_admin', 1), (2, 'jdoe', 'student', 2), (3, 'gone', 'student', 2), (99999, 'system_batch', 'system_admin', 1)`).Error)
	require.NoError(t, db.Exec(`UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = 3`).Error)
	require.NoError(t, db.Create(&models.Library{OpenContentProviderID: 1, ExternalID: models.StringPtr("lib-1"), Title: "Algebra", Url: "/content/algebra"}).Error)

	// A failure partway through adds nothing.
	require.NoError(t, db.Migrator().RenameTable("open_content_favorites", "favorites_away"))
	require.Error(t, seedDev(t.Context(), db, time.Now()))
	require.NoError(t, db.Model(&models.Video{}).Count(&count).Error)
	require.Zero(t, count, "a failed run adds nothing")
	require.NoError(t, db.Migrator().RenameTable("favorites_away", "open_content_favorites"))

	require.NoError(t, seedDev(t.Context(), db, time.Now()))

	var videos []models.Video
	require.NoError(t, db.Find(&videos).Error)
	assert.Len(t, videos, 25)
	for _, v := range videos {
		require.NotNil(t, v.CreateUserID)
		assert.Equal(t, uint(99999), *v.CreateUserID, "made by system_batch")
	}

	var urls []models.OpenContentUrl
	require.NoError(t, db.Find(&urls).Error)
	assert.Len(t, urls, 26, "one per video and library")
	for _, v := range videos {
		assert.NotNil(t, urlID(urls, fmt.Sprintf("/api/proxy/videos/%d", v.ID)), "video %d has its url", v.ID)
	}
	assert.NotNil(t, urlID(urls, "/api/proxy/libraries/1/"))

	var favorites []models.OpenContentFavorite
	require.NoError(t, db.Find(&favorites).Error)
	assert.GreaterOrEqual(t, len(favorites), 3)
	for _, f := range favorites {
		assert.Equal(t, uint(2), f.UserID, "residents only")
	}

	var activity []models.OpenContentActivity
	require.NoError(t, db.Find(&activity).Error)
	for _, a := range activity {
		assert.Equal(t, uint(2), a.UserID, "residents only")
		assert.Equal(t, uint(2), a.FacilityID, "at the resident's facility")
		assert.True(t, a.StopTS.After(a.RequestTS) || a.StopTS.Equal(a.RequestTS))
	}
}
