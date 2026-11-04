// Package testutil is what the tests of more than one package share.
package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// NewDB returns an empty in-memory database with Open Content's tables,
// closed when the test ends.
func NewDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(tables()...); err != nil {
		t.Fatal(err)
	}
	return db
}

// tables is Open Content's tables.
func tables() []any {
	return []any{
		&models.HelpfulLink{},
		&models.OpenContentProvider{},
		&models.OpenContentUrl{},
		&models.OpenContentActivity{},
		&models.Library{},
		&models.FacilityVisibilityStatus{},
		&models.Video{},
		&models.VideoDownloadAttempt{},
		&models.OpenContentFavorite{},
		&models.Tag{},
		&models.OpenContentTag{},
	}
}
