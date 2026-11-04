package handlers

import (
	"testing"
	"time"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNotifier struct{ sent []api.Notification }

func (n *fakeNotifier) Notify(msg api.Notification) {
	n.sent = append(n.sent, msg)
}

// A visit is recorded, and the user's tab is told about it so it can end the
// activity. A library page also says whether it is bookmarked.
func TestCreateContentActivityAndNotifyWS(t *testing.T) {
	db := newTestDB(t)
	notify := &fakeNotifier{}
	srv := &server{db: db, notify: notify}

	page := "/api/proxy/libraries/1/A/Lesson_1"
	visit := func(url string) int64 {
		activity := models.OpenContentActivity{OpenContentProviderID: 1, FacilityID: 2, UserID: 7, ContentID: 1, RequestTS: time.Now()}
		srv.createContentActivityAndNotifyWS(url, &activity)
		require.NotZero(t, activity.ID)
		return activity.ID
	}

	t.Run("library page, not bookmarked", func(t *testing.T) {
		notify.sent = nil
		id := visit(page)
		assert.Equal(t, []api.Notification{
			{UserID: 7, Event: "visits", ActivityID: id},
			{UserID: 7, Event: "bookmarks", Text: "false"},
		}, notify.sent)
	})

	t.Run("library page, bookmarked", func(t *testing.T) {
		var url models.OpenContentUrl
		require.NoError(t, db.First(&url, "content_url = ?", page).Error)
		require.NoError(t, db.Create(&models.OpenContentFavorite{UserID: 7, ContentID: 1, OpenContentProviderID: 1, OpenContentUrlID: &url.ID}).Error)

		notify.sent = nil
		id := visit(page)
		assert.Equal(t, []api.Notification{
			{UserID: 7, Event: "visits", ActivityID: id},
			{UserID: 7, Event: "bookmarks", Text: "true"},
		}, notify.sent)
	})

	t.Run("video", func(t *testing.T) {
		notify.sent = nil
		id := visit("/viewer/videos/1")
		assert.Equal(t, []api.Notification{{UserID: 7, Event: "visits", ActivityID: id}}, notify.sent)
	})
}
