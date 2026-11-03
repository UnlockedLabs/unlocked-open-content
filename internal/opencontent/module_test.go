package opencontent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Seed fills only a new database, and only once core has a facility.
func TestSeed(t *testing.T) {
	type counts struct{ links, providers, visibility int64 }
	countRows := func(t *testing.T, db *gorm.DB) counts {
		t.Helper()
		var c counts
		require.NoError(t, db.Model(&HelpfulLink{}).Count(&c.links).Error)
		require.NoError(t, db.Model(&OpenContentProvider{}).Count(&c.providers).Error)
		require.NoError(t, db.Model(&FacilityVisibilityStatus{}).Count(&c.visibility).Error)
		return c
	}
	newDB := func(t *testing.T, facilities ...string) *gorm.DB {
		t.Helper()
		db := newTestDB(t)
		require.NoError(t, db.Exec(`CREATE TABLE facilities (id INTEGER PRIMARY KEY, name TEXT NOT NULL, deleted_at DATETIME)`).Error)
		require.NoError(t, db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT NOT NULL)`).Error)
		for _, name := range facilities {
			require.NoError(t, db.Exec(`INSERT INTO facilities (name) VALUES (?)`, name).Error)
		}
		return db
	}

	t.Run("new database", func(t *testing.T) {
		db := newDB(t, "Default", "North")
		require.NoError(t, db.Exec(`INSERT INTO users (id, username) VALUES (5, 'system_batch')`).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{links: 2, providers: 2, visibility: 2}, countRows(t, db))

		var visibility []FacilityVisibilityStatus
		require.NoError(t, db.Find(&visibility).Error)
		for _, v := range visibility {
			assert.Equal(t, uint(1), v.FacilityID, "visible at the first facility")
		}
		var kiwix OpenContentProvider
		require.NoError(t, db.Where("title = ?", Kiwix).First(&kiwix).Error)
		require.NotNil(t, kiwix.CreateUserID)
		assert.Equal(t, uint(5), *kiwix.CreateUserID, "made by system_batch")
	})

	t.Run("runs once", func(t *testing.T) {
		db := newDB(t, "Default")
		require.NoError(t, Seed(t.Context(), db))
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{links: 2, providers: 2, visibility: 2}, countRows(t, db))
	})

	t.Run("no facility yet", func(t *testing.T) {
		db := newDB(t)
		require.ErrorIs(t, Seed(t.Context(), db), errNoFacility)
		assert.Equal(t, counts{}, countRows(t, db))

		require.NoError(t, db.Exec(`INSERT INTO facilities (name) VALUES ('Default')`).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{links: 2, providers: 2, visibility: 2}, countRows(t, db))
	})

	t.Run("worker waits for core's first facility", func(t *testing.T) {
		db := newDB(t)
		done := make(chan error, 1)
		go func() { done <- waitAndSeed(t.Context(), db) }()
		select {
		case err := <-done:
			t.Fatalf("waitAndSeed returned %v before core had a facility", err)
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, db.Exec(`INSERT INTO facilities (name) VALUES ('Default')`).Error)
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(3 * seedPollInterval):
			t.Fatal("waitAndSeed didn't seed once core had a facility")
		}
		assert.Equal(t, counts{links: 2, providers: 2, visibility: 2}, countRows(t, db))
	})

	t.Run("worker stops waiting when canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, waitAndSeed(ctx, newDB(t)), context.Canceled)
	})

	t.Run("site with only deleted links", func(t *testing.T) {
		db := newDB(t, "Default")
		link := HelpfulLink{Title: "Old", Url: "https://example.org"}
		require.NoError(t, db.Create(&link).Error)
		require.NoError(t, db.Delete(&link).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{}, countRows(t, db))
	})

	for _, title := range []string{Kiwix, Youtube} {
		t.Run("site with no links and a "+title+" provider", func(t *testing.T) {
			db := newDB(t, "Default")
			require.NoError(t, db.Create(&OpenContentProvider{Title: stringPtr(title), Url: "http://" + title}).Error)
			require.NoError(t, Seed(t.Context(), db))
			assert.Equal(t, counts{providers: 1}, countRows(t, db))
		})
	}
}
