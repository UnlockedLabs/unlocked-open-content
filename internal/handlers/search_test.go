package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/stretchr/testify/require"
)

// kiwixSearchXML is a one-hit Kiwix search answer for the library at /content/test_lib.
const kiwixSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/">
  <channel>
    <title>Search: history</title>
    <description>Search result for history</description>
    <opensearch:totalResults>1</opensearch:totalResults>
    <opensearch:startIndex>1</opensearch:startIndex>
    <opensearch:itemsPerPage>10</opensearch:itemsPerPage>
    <item>
      <title>A page about history</title>
      <link>/content/test_lib/A/History</link>
      <description>the <b>history</b> of things</description>
      <book><title>Test Library</title></book>
      <wordCount>100</wordCount>
    </item>
  </channel>
</rss>`

// fakeKiwix answers every search with kiwixSearchXML and counts the calls.
func fakeKiwix(t *testing.T) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	kiwix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(kiwixSearchXML))
	}))
	t.Cleanup(kiwix.Close)
	old := models.KiwixLibraryUrl
	models.KiwixLibraryUrl = kiwix.URL
	t.Cleanup(func() { models.KiwixLibraryUrl = old })
	return &hits
}

type searchChannel struct {
	Items []struct {
		ContentID uint   `json:"content_id"`
		PageTitle string `json:"page_title"`
	} `json:"items"`
}

// A search by library_id must not read a library hidden at the resident's
// facility (EN-337: search only covers libraries visible at the facility).
// Admins may still search it, as they may open it.
func TestSearchByLibraryIDRespectsFacilityVisibility(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A", "Facility B")
	ts, client := newRouteServer(t, db)
	hits := fakeKiwix(t)

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	libraryID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Test Library', '/content/test_lib', 'eng')`, kiwixID)

	resident := &identity.Claims{UserID: 20, Role: users.Student, FacilityID: facilityA, FeatureAccess: features.All}
	facAdmin := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	dept := &identity.Claims{UserID: 10, Role: users.DepartmentAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	searchURL := fmt.Sprintf("/api/open-content/search?search=history&library_id=%d", libraryID)
	search := func(t *testing.T, c *identity.Claims) []searchChannel {
		t.Helper()
		return data[[]searchChannel](t, expectStatus(t, call(t, ts, client, c, http.MethodGet, searchURL, nil), http.StatusOK))
	}
	itemCount := func(channels []searchChannel) int {
		n := 0
		for _, ch := range channels {
			n += len(ch.Items)
		}
		return n
	}

	t.Run("hidden at the resident's facility: no results, Kiwix not asked", func(t *testing.T) {
		before := hits.Load()
		require.Zero(t, itemCount(search(t, resident)))
		require.Equal(t, before, hits.Load(), "Kiwix was searched for a hidden library")
	})

	t.Run("admin may search a hidden library", func(t *testing.T) {
		before := hits.Load()
		require.Equal(t, 1, itemCount(search(t, facAdmin)))
		require.Equal(t, before+1, hits.Load())
	})

	t.Run("visible at another facility only: still hidden for the resident", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, dept, http.MethodPut, fmt.Sprintf("/api/libraries/%d/facilities", libraryID),
			map[string]any{"facility_ids": []uint{facilityB}, "visibility_status": true}), http.StatusOK)
		before := hits.Load()
		require.Zero(t, itemCount(search(t, resident)))
		require.Equal(t, before, hits.Load())
	})

	t.Run("visible at the resident's facility: results", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, dept, http.MethodPut, fmt.Sprintf("/api/libraries/%d/facilities", libraryID),
			map[string]any{"facility_ids": []uint{facilityA}, "visibility_status": true}), http.StatusOK)
		channels := search(t, resident)
		require.Equal(t, 1, itemCount(channels))
		require.Equal(t, uint(libraryID), channels[0].Items[0].ContentID)
		require.Equal(t, "A page about history", channels[0].Items[0].PageTitle)
	})
}
