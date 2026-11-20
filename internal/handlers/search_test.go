package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"sync"
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

// kiwixFake is a fake Kiwix search server: it answers every search with
// kiwixSearchXML, counts the calls and keeps the last query.
type kiwixFake struct {
	hits atomic.Int32
	mu   sync.Mutex
	last url.Values
}

func (k *kiwixFake) Load() int32 { return k.hits.Load() }

// books is the books.name values of the last search.
func (k *kiwixFake) books() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	b := append([]string(nil), k.last["books.name"]...)
	sort.Strings(b)
	return b
}

func fakeKiwix(t *testing.T) *kiwixFake {
	t.Helper()
	k := &kiwixFake{}
	kiwix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.hits.Add(1)
		k.mu.Lock()
		k.last = r.URL.Query()
		k.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(kiwixSearchXML))
	}))
	t.Cleanup(kiwix.Close)
	old := models.KiwixLibraryUrl
	models.KiwixLibraryUrl = kiwix.URL
	t.Cleanup(func() { models.KiwixLibraryUrl = old })
	return k
}

type searchItem struct {
	ContentID   uint   `json:"content_id"`
	ContentType string `json:"content_type"`
	Title       string `json:"title"`
	PageTitle   string `json:"page_title"`
	Match       string `json:"match"`
}

type searchChannel struct {
	Items []searchItem `json:"items"`
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

// searchItems is every item of a search answer, as "match/content_type/title".
func searchItems(channels []searchChannel) []string {
	var got []string
	for _, ch := range channels {
		for _, it := range ch.Items {
			got = append(got, it.Match+"/"+it.ContentType+"/"+it.Title)
		}
	}
	sort.Strings(got)
	return got
}

// Title search covers libraries, videos and helpful links visible at the
// facility; "types" picks kinds, sub-features gate videos and links, and a
// category (tags) keeps only libraries in it (EN-337).
func TestSearchScopeAndCategories(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A")
	ts, client := newRouteServer(t, db)
	kiwix := fakeKiwix(t)

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	ytID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('YouTube', 'http://youtube', true)`)
	linkProvID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Links', 'http://links', true)`)
	rome := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, description, url, language) VALUES (?, 'History of Rome', 'Ancient Rome', '/content/test_lib', 'eng')`, kiwixID)
	greece := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, description, url, language) VALUES (?, 'Greece', 'A history of Greece', '/content/greece', 'eng')`, kiwixID)
	hiddenLib := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Hidden History', '/content/hidden', 'eng')`, kiwixID)
	video := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, url, external_id) VALUES (?, 'History in five minutes', '/v1', 'v1')`, ytID)
	link := insertID(t, db, `INSERT INTO helpful_links (title, description, url, open_content_provider_id) VALUES ('History link', 'Read about history', 'https://example.org/h', ?)`, linkProvID)
	hiddenLink := insertID(t, db, `INSERT INTO helpful_links (title, description, url, open_content_provider_id) VALUES ('Hidden history link', 'x', 'https://example.org/x', ?)`, linkProvID)
	for _, v := range []struct {
		provider, content uint
		visible           bool
	}{{kiwixID, rome, true}, {kiwixID, greece, true}, {kiwixID, hiddenLib, false}, {ytID, video, true}, {linkProvID, link, true}, {linkProvID, hiddenLink, false}} {
		require.NoError(t, db.Exec(`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES (?, ?, ?, ?)`,
			facilityA, v.provider, v.content, v.visible).Error)
	}
	tagID := insertID(t, db, `INSERT INTO tags (name) VALUES ('Social Studies')`)
	require.NoError(t, db.Exec(`INSERT INTO open_content_tags (tag_id, content_id, open_content_provider_id) VALUES (?, ?, ?)`, tagID, rome, kiwixID).Error)

	all := &identity.Claims{UserID: 20, Role: users.Student, FacilityID: facilityA, FeatureAccess: features.All}
	noLinks := &identity.Claims{UserID: 21, Role: users.Student, FacilityID: facilityA,
		FeatureAccess: []features.Access{features.OpenContent, features.UploadVideo}}
	search := func(t *testing.T, c *identity.Claims, query string) []string {
		t.Helper()
		return searchItems(data[[]searchChannel](t, expectStatus(t, call(t, ts, client, c, http.MethodGet, "/api/open-content/search?search=history"+query, nil), http.StatusOK)))
	}

	t.Run("no types: every visible kind, title and content matches", func(t *testing.T) {
		require.Equal(t, []string{
			"content/library/Test Library",
			"title/library/Greece",
			"title/library/History of Rome",
			"title/link/History link",
			"title/video/History in five minutes",
		}, search(t, all, ""))
		require.Equal(t, []string{"greece", "test_lib"}, kiwix.books(), "Kiwix asked only for visible libraries")
	})

	t.Run("types=content: inside libraries only", func(t *testing.T) {
		require.Equal(t, []string{"content/library/Test Library"}, search(t, all, "&types=content"))
	})

	t.Run("types=library: library titles only, Kiwix not asked", func(t *testing.T) {
		before := kiwix.Load()
		require.Equal(t, []string{"title/library/Greece", "title/library/History of Rome"}, search(t, all, "&types=library"))
		require.Equal(t, before, kiwix.Load())
	})

	t.Run("types=video&types=link", func(t *testing.T) {
		require.Equal(t, []string{"title/link/History link", "title/video/History in five minutes"}, search(t, all, "&types=video&types=link"))
	})

	t.Run("no helpful-links feature: no links even when asked", func(t *testing.T) {
		require.Equal(t, []string{"title/video/History in five minutes"}, search(t, noLinks, "&types=video&types=link"))
	})

	t.Run("category: only libraries in it, no videos or links", func(t *testing.T) {
		require.Equal(t, []string{"content/library/Test Library", "title/library/History of Rome"}, search(t, all, fmt.Sprintf("&tags=%d", tagID)))
		require.Equal(t, []string{"test_lib"}, kiwix.books(), "Kiwix asked only for libraries in the category")
	})
}
