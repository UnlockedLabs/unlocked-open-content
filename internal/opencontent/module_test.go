package opencontent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Seed fills only a new database, and only once core has a facility, so a
// migrate run before core's first start seeds on a later run.
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
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{}, countRows(t, db))

		require.NoError(t, db.Exec(`INSERT INTO facilities (name) VALUES ('Default')`).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{links: 2, providers: 2, visibility: 2}, countRows(t, db))
	})

	t.Run("site with only deleted links", func(t *testing.T) {
		db := newDB(t, "Default")
		link := HelpfulLink{Title: "Old", Url: "https://example.org"}
		require.NoError(t, db.Create(&link).Error)
		require.NoError(t, db.Delete(&link).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{}, countRows(t, db))
	})

	t.Run("site with a Kiwix provider", func(t *testing.T) {
		db := newDB(t, "Default")
		require.NoError(t, db.Create(&OpenContentProvider{Title: stringPtr(Kiwix), Url: "http://kiwix"}).Error)
		require.NoError(t, Seed(t.Context(), db))
		assert.Equal(t, counts{providers: 1}, countRows(t, db))
	})
}
