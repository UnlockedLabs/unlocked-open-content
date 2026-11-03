package database

import (
	"testing"

	"github.com/UnlockedLabs/unlocked-open-content/internal/testutil"
)

// newTestDB returns an empty in-memory database with Open Content's tables.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	return NewDB(testutil.NewDB(t))
}
