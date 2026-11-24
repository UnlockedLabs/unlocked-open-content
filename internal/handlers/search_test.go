package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

// kiwixFake is a fake Kiwix search server. By default it answers every search
// with kiwixSearchXML. After setHits(n) it has n hits ("Hit 1" to "Hit n",
// in that order) and, like Kiwix, honors the 0-based start and pageLength.
// It counts the calls and keeps the last query.
type kiwixFake struct {
	hits  atomic.Int32
	mu    sync.Mutex
	last  url.Values
	total int
}

// setHits makes the fake have n hits for every search.
func (k *kiwixFake) setHits(n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.total = n
}

// query is the last search's query string values.
func (k *kiwixFake) query() url.Values {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.last
}

func (k *kiwixFake) answer(q url.Values) string {
	k.mu.Lock()
	total := k.total
	k.mu.Unlock()
	if total == 0 {
		return kiwixSearchXML
	}
	start, _ := strconv.Atoi(q.Get("start"))
	length, _ := strconv.Atoi(q.Get("pageLength"))
	var items strings.Builder
	for i := start; i < min(start+length, total); i++ {
		fmt.Fprintf(&items, `<item><title>Hit %d</title><link>/content/test_lib/A/Hit_%d</link>
			<description>hit <b>history</b></description><book><title>Test Library</title></book><wordCount>1</wordCount></item>`, i+1, i+1)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/"><channel>
<title>Search: history</title><description>d</description>
<opensearch:totalResults>%d</opensearch:totalResults><opensearch:startIndex>%d</opensearch:startIndex>
<opensearch:itemsPerPage>%d</opensearch:itemsPerPage>%s</channel></rss>`, total, start, length, items.String())
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
		_, _ = w.Write([]byte(k.answer(r.URL.Query())))
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
	ProviderID  uint   `json:"open_content_provider_id"`
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

// searchPage is one page of a search: its items as "match/content_type/title"
// in the order returned, and the total the page reports.
func searchPage(t *testing.T, ts *httptest.Server, client *http.Client, c *identity.Claims, query string, page, perPage int) ([]string, int64) {
	t.Helper()
	resp := expectStatus(t, call(t, ts, client, c, http.MethodGet,
		fmt.Sprintf("/api/open-content/search?search=history&page=%d&per_page=%d%s", page, perPage, query), nil), http.StatusOK)
	var body struct {
		Data []searchChannel `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.body), &body), resp.body)
	var got []string
	for _, ch := range body.Data {
		for _, it := range ch.Items {
			got = append(got, it.Match+"/"+it.ContentType+"/"+it.Title+"|"+it.PageTitle)
		}
	}
	return got, body.Meta.Total
}

// Within libraries, a library whose title matches comes before one that only
// matched in its description, even when its title sorts later.
func TestSearchRanksTitleMatchesFirst(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A")
	ts, client := newRouteServer(t, db)
	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	anatomy := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, description, url, language) VALUES (?, 'Anatomy', 'Biology basics', '/content/anatomy', 'eng')`, kiwixID)
	biology := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Biology LibreTexts', '/content/biology', 'eng')`, kiwixID)
	for _, lib := range []uint{anatomy, biology} {
		require.NoError(t, db.Exec(`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES (?, ?, ?, true)`,
			facilityA, kiwixID, lib).Error)
	}
	resident := &identity.Claims{UserID: 20, Role: users.Student, FacilityID: facilityA, FeatureAccess: features.All}

	channels := data[[]searchChannel](t, expectStatus(t, call(t, ts, client, resident, http.MethodGet,
		"/api/open-content/search?search=bio&types=library", nil), http.StatusOK))
	var titles []string
	for _, it := range channels[0].Items {
		titles = append(titles, it.Title)
	}
	require.Equal(t, []string{"Biology LibreTexts", "Anatomy"}, titles)
}

// The results are one ordered list: title matches (libraries, then videos,
// then helpful links, each by title) then Kiwix hits in Kiwix's order. Page N
// is a slice of that list and the total is the same on every page (EN-337).
func TestSearchOrderAndPaging(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A")
	ts, client := newRouteServer(t, db)
	kiwix := fakeKiwix(t)
	kiwix.setHits(5)

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	ytID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('YouTube', 'http://youtube', true)`)
	linkProvID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Links', 'http://links', true)`)
	lib := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Test Library', '/content/test_lib', 'eng')`, kiwixID)
	zeta := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, description, url, language) VALUES (?, 'Zeta', 'history', '/content/zeta', 'eng')`, kiwixID)
	alpha := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, description, url, language) VALUES (?, 'Alpha', 'history', '/content/alpha', 'eng')`, kiwixID)
	video := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, url, external_id) VALUES (?, 'History video', '/v1', 'v1')`, ytID)
	chanVideo := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, channel_title, url, external_id) VALUES (?, 'Cooking', 'History Channel', '/v2', 'v2')`, ytID)
	link := insertID(t, db, `INSERT INTO helpful_links (title, description, url, open_content_provider_id) VALUES ('History link', 'x', 'https://example.org/h', ?)`, linkProvID)
	for _, v := range []struct{ provider, content uint }{{kiwixID, lib}, {kiwixID, zeta}, {kiwixID, alpha}, {ytID, video}, {ytID, chanVideo}, {linkProvID, link}} {
		require.NoError(t, db.Exec(`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES (?, ?, ?, true)`,
			facilityA, v.provider, v.content).Error)
	}
	resident := &identity.Claims{UserID: 20, Role: users.Student, FacilityID: facilityA, FeatureAccess: features.All}

	// 4 title matches (2 libraries, 1 video, 1 link: the channel-only video
	// does not match) and 5 Kiwix hits: 9 in all, 3 per page.
	titles := []string{"title/library/Alpha|Alpha", "title/library/Zeta|Zeta", "title/video/History video|History video"}
	pages := [][]string{
		titles,
		{"title/link/History link|History link", "content/library/Test Library|Hit 1", "content/library/Test Library|Hit 2"},
		{"content/library/Test Library|Hit 3", "content/library/Test Library|Hit 4", "content/library/Test Library|Hit 5"},
		nil,
	}
	var all []string
	for i, want := range pages {
		got, total := searchPage(t, ts, client, resident, "", i+1, 3)
		require.Equal(t, want, got, "page %d", i+1)
		require.Equal(t, int64(9), total, "total on page %d", i+1)
		all = append(all, got...)
	}
	require.Len(t, all, 9)
	require.Len(t, map[string]bool{all[0]: true, all[1]: true, all[2]: true, all[3]: true, all[4]: true, all[5]: true, all[6]: true, all[7]: true, all[8]: true}, 9, "no duplicates")

	t.Run("titles fill the page: Kiwix still counted, first Kiwix hit starts the next page", func(t *testing.T) {
		got, total := searchPage(t, ts, client, resident, "", 1, 2)
		require.Equal(t, []string{"title/library/Alpha|Alpha", "title/library/Zeta|Zeta"}, got)
		require.Equal(t, int64(9), total)
		require.Equal(t, "0", kiwix.query().Get("start"))
		got, _ = searchPage(t, ts, client, resident, "", 3, 2)
		require.Equal(t, []string{"content/library/Test Library|Hit 1", "content/library/Test Library|Hit 2"}, got)
		require.Equal(t, "0", kiwix.query().Get("start"), "Kiwix's start is 0-based")
		got, _ = searchPage(t, ts, client, resident, "", 4, 2)
		require.Equal(t, []string{"content/library/Test Library|Hit 3", "content/library/Test Library|Hit 4"}, got)
	})

	t.Run("types=content: Kiwix hits only, in Kiwix's order from the first", func(t *testing.T) {
		got, total := searchPage(t, ts, client, resident, "&types=content", 1, 3)
		require.Equal(t, []string{"content/library/Test Library|Hit 1", "content/library/Test Library|Hit 2", "content/library/Test Library|Hit 3"}, got)
		require.Equal(t, int64(5), total)
	})

	t.Run("library_id search skips the title search", func(t *testing.T) {
		got, total := searchPage(t, ts, client, resident, fmt.Sprintf("&library_id=%d", lib), 2, 3)
		require.Equal(t, []string{"content/library/Test Library|Hit 4", "content/library/Test Library|Hit 5"}, got)
		require.Equal(t, int64(5), total)
	})

	t.Run("items carry the provider id", func(t *testing.T) {
		channels := data[[]searchChannel](t, expectStatus(t, call(t, ts, client, resident, http.MethodGet,
			"/api/open-content/search?search=history&page=2&per_page=3", nil), http.StatusOK))
		require.Len(t, channels[0].Items, 3)
		require.Equal(t, "link", channels[0].Items[0].ContentType)
		require.Equal(t, linkProvID, channels[0].Items[0].ProviderID)
		require.Equal(t, "content", channels[0].Items[1].Match)
		require.Equal(t, kiwixID, channels[0].Items[1].ProviderID)
		channels = data[[]searchChannel](t, expectStatus(t, call(t, ts, client, resident, http.MethodGet,
			"/api/open-content/search?search=history&per_page=3", nil), http.StatusOK))
		require.Equal(t, kiwixID, channels[0].Items[0].ProviderID)
		require.Equal(t, ytID, channels[0].Items[2].ProviderID)
	})

	t.Run("a huge page or per_page is capped instead of overflowing", func(t *testing.T) {
		for _, q := range []string{"page=2&per_page=9223372036854775807", "page=9223372036854775807&per_page=10"} {
			expectStatus(t, call(t, ts, client, resident, http.MethodGet, "/api/open-content/search?search=history&"+q, nil), http.StatusOK)
			length, err := strconv.Atoi(kiwix.query().Get("pageLength"))
			require.NoError(t, err)
			require.LessOrEqual(t, length, 100, q)
		}
	})
}

// A bookmark of a library page needs no earlier visit, can be removed, and
// only covers pages of a library the resident can see (EN-337).
func TestBookmarkLibraryPage(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A")
	ts, client := newRouteServer(t, db)

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	lib := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Test Library', '/content/test_lib', 'eng')`, kiwixID)
	other := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Other', '/content/other', 'eng')`, kiwixID)
	hidden := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url, language) VALUES (?, 'Hidden', '/content/hidden', 'eng')`, kiwixID)
	for id, visible := range map[uint]bool{lib: true, other: true, hidden: false} {
		require.NoError(t, db.Exec(`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES (?, ?, ?, ?)`,
			facilityA, kiwixID, id, visible).Error)
	}
	resident := &identity.Claims{UserID: 20, Role: users.Student, FacilityID: facilityA, FeatureAccess: features.All}
	facAdmin := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	bookmark := func(c *identity.Claims, id uint, body map[string]any) routeResponse {
		return call(t, ts, client, c, http.MethodPut, fmt.Sprintf("/api/open-content/%d/bookmark", id), body)
	}
	favorites := func(userID uint) int64 {
		var n int64
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM open_content_favorites WHERE user_id = ?`, userID).Scan(&n).Error)
		return n
	}
	page := fmt.Sprintf("/api/proxy/libraries/%d/content/test_lib/A/History", lib)
	add := map[string]any{"open_content_provider_id": kiwixID, "name": "History page", "content_url": page}

	t.Run("add with no earlier visit", func(t *testing.T) {
		var activities int64
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM open_content_activities`).Scan(&activities).Error)
		require.Zero(t, activities)
		expectStatus(t, bookmark(resident, lib, add), http.StatusOK)
		require.Equal(t, int64(1), favorites(20))
		var name string
		require.NoError(t, db.Raw(`SELECT f.name FROM open_content_favorites f JOIN open_content_urls u ON u.id = f.open_content_url_id WHERE u.content_url = ?`, page).Scan(&name).Error)
		require.Equal(t, "History page", name)
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM open_content_activities`).Scan(&activities).Error)
		require.Zero(t, activities, "no activity row was made")
	})

	t.Run("adding twice keeps one favorite", func(t *testing.T) {
		expectStatus(t, bookmark(resident, lib, add), http.StatusOK)
		require.Equal(t, int64(1), favorites(20))
	})

	t.Run("remove with the URL only", func(t *testing.T) {
		expectStatus(t, bookmark(resident, lib, map[string]any{"open_content_provider_id": kiwixID, "content_url": page}), http.StatusOK)
		require.Zero(t, favorites(20))
	})

	t.Run("a URL of another library is rejected", func(t *testing.T) {
		body := map[string]any{"open_content_provider_id": kiwixID, "name": "x", "content_url": fmt.Sprintf("/api/proxy/libraries/%d/content/other/A/Page", other)}
		expectStatus(t, bookmark(resident, lib, body), http.StatusBadRequest)
		for _, u := range []string{"https://example.org/x", "/api/proxy/videos/1", fmt.Sprintf("/api/proxy/libraries/%d9/A", lib),
			fmt.Sprintf("/api/proxy/libraries/%d/../%d/A", lib, other)} {
			body["content_url"] = u
			expectStatus(t, bookmark(resident, lib, body), http.StatusBadRequest)
		}
		require.Zero(t, favorites(20))
	})

	t.Run("a wrong provider id is rejected", func(t *testing.T) {
		expectStatus(t, bookmark(resident, lib, map[string]any{"open_content_provider_id": kiwixID + 1, "name": "x", "content_url": page}), http.StatusBadRequest)
		require.Zero(t, favorites(20))
	})

	t.Run("a hidden library is rejected for a resident", func(t *testing.T) {
		body := map[string]any{"open_content_provider_id": kiwixID, "name": "x", "content_url": fmt.Sprintf("/api/proxy/libraries/%d/content/hidden/A/Page", hidden)}
		expectStatus(t, bookmark(resident, hidden, body), http.StatusForbidden)
		require.Zero(t, favorites(20))
		expectStatus(t, bookmark(facAdmin, hidden, body), http.StatusOK)
	})

	t.Run("the library viewer's add (name only) still uses the last visit", func(t *testing.T) {
		body := map[string]any{"open_content_provider_id": kiwixID, "name": "Viewer"}
		expectStatus(t, bookmark(resident, lib, body), http.StatusBadRequest) // the old behavior: no visit yet
		require.NoError(t, db.Exec(`INSERT INTO open_content_urls (content_url) VALUES ('/api/proxy/libraries/1/visited')`).Error)
		require.NoError(t, db.Exec(`INSERT INTO open_content_activities (user_id, content_id, open_content_provider_id, facility_id, open_content_url_id, request_ts)
			VALUES (20, ?, ?, ?, (SELECT id FROM open_content_urls WHERE content_url = '/api/proxy/libraries/1/visited'), CURRENT_TIMESTAMP)`, lib, kiwixID, facilityA).Error)
		expectStatus(t, bookmark(resident, lib, body), http.StatusOK)
		require.Equal(t, int64(1), favorites(20))
	})
}
