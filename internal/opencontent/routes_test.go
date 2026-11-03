package opencontent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests pin what every route answers: the status and the JSON, and who
// may call it. They came from core's tests/integration/open_content_routes_test.go
// (EN-192) with its saved responses in testdata/routes unchanged, so passing
// them shows the routes answer exactly as they did in core (EN-205).
//
// Regenerate the saved responses after an intended change with:
//
//	go test ./internal/opencontent -run 'TestRoute' -update

var updateGolden = flag.Bool("update", false, "rewrite the saved responses in testdata/routes")

// testClaimsHeader carries the caller's identity.Claims as JSON, in place of
// the login session the tests don't have, as core's X-Test-Claims did.
const testClaimsHeader = "X-Test-Claims"

// The callers. The IDs are the ones core's test database gave them, after its
// default user (1); the saved responses name them. Every feature is on.
var (
	residentClaims  = &identity.Claims{UserID: 2, Username: "jdoe", Role: users.Student, FacilityID: 2, FacilityName: "North", FeatureAccess: features.All}
	facAdminClaims  = &identity.Claims{UserID: 3, Username: "facadmin", Role: users.FacilityAdmin, FacilityID: 2, FacilityName: "North", FeatureAccess: features.All}
	deptAdminClaims = &identity.Claims{UserID: 4, Username: "deptadmin", Role: users.DepartmentAdmin, FacilityID: 2, FacilityName: "North", FeatureAccess: features.All}
)

// Core's facilities: its default one, then the two the tests add.
const (
	northID = 2
	southID = 3
)

// newRouteTest starts the routes on a test database seeded as core's tests
// seeded it, and returns a client that doesn't follow redirects, so a test
// sees the redirect a route answers with.
func newRouteTest(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	db := newTestDB(t)
	seedCore(t, db)
	seedOpenContent(t, db)
	return newRouteServer(t, db)
}

// newRouteServer starts the routes on db.
func newRouteServer(t *testing.T, db *gorm.DB) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := &server{store: &store{db: db}, notify: &fakeNotifier{}, client: &http.Client{}}
	ts := httptest.NewServer(newRouter(srv.routes(), testLogin))
	t.Cleanup(ts.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return ts, client
}

// testLogin passes the caller in testClaimsHeader, as identity.SessionChecker
// passes a logged-in caller.
func testLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c identity.Claims
		if err := json.Unmarshal([]byte(r.Header.Get(testClaimsHeader)), &c); err != nil {
			http.Error(w, "test claims: "+err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.NewContext(r.Context(), &c)))
	})
}

// seedCore adds what core's test database started with that the routes read:
// its default facility, then facilities (North and South unless given), and
// the default helpful links and providers its seed added as user 99999,
// visible at the default facility.
func seedCore(t *testing.T, db *gorm.DB, facilities ...string) {
	t.Helper()
	if facilities == nil {
		facilities = []string{"North", "South"}
	}
	require.NoError(t, db.Exec(`CREATE TABLE facilities (id INTEGER PRIMARY KEY, name TEXT NOT NULL, deleted_at DATETIME)`).Error)
	for _, name := range append([]string{"Default"}, facilities...) {
		require.NoError(t, db.Exec(`INSERT INTO facilities (name) VALUES (?)`, name).Error)
	}
	seedCtx := identity.NewContext(t.Context(), &identity.Claims{UserID: 99999})
	require.NoError(t, seed(db.WithContext(seedCtx), 1))
}

// seedOpenContent adds two libraries, two videos and a helpful link at the
// North facility, plus a favorite and some reading activity for the resident.
// Production's migrations also add a HelpfulLinks provider and the links
// point to it, so the seed does the same here.
func seedOpenContent(t *testing.T, db *gorm.DB) {
	t.Helper()
	// Fixed times keep the ordering stable; recent ones fall inside the
	// activity routes' default two-week window.
	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	recent := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	resident := residentClaims.UserID
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO open_content_providers (id, title, url, thumbnail_url, currently_enabled, description, created_at, updated_at)
			VALUES (3, 'HelpfulLinks', 'https://helpful.example', '/ul-logo-d.svg', true, 'Hand picked helpful links for users', ?, ?)`, []any{day, day}},
		{`UPDATE helpful_links SET open_content_provider_id = 3 WHERE open_content_provider_id = 1`, nil},
		{`UPDATE facility_visibility_statuses SET open_content_provider_id = 3 WHERE open_content_provider_id = 1`, nil},
		{`INSERT INTO libraries (id, open_content_provider_id, external_id, title, language, description, url, thumbnail_url, created_at, updated_at) VALUES
			(1, 1, 'lib-algebra', 'Algebra', 'fra', 'Algebre pour tous', '/content/algebra_fr', '/thumbs/algebra.png', ?, ?),
			(2, 1, 'lib-biology', 'Biology', 'spa', 'Biologia', '/content/biology_es', NULL, ?, ?)`,
			[]any{day, day, day.Add(time.Hour), day.Add(time.Hour)}},
		{`INSERT INTO tags (id, name) VALUES (1, 'Science')`, nil},
		{`INSERT INTO open_content_tags (tag_id, content_id, open_content_provider_id) VALUES (1, 2, 1)`, nil},
		{`INSERT INTO videos (id, external_id, url, title, availability, channel_title, duration, description, thumbnail_url, open_content_provider_id, created_at, updated_at) VALUES
			(1, 'vid-ok', 'https://www.youtube.com/watch?v=vid-ok', 'Intro Video', 'available', 'Channel A', 120, 'An intro', '/thumbs/v1.jpg', 2, ?, ?),
			(2, 'vid-error', 'https://www.youtube.com/watch?v=vid-error', 'Broken Video', 'has_error', 'Channel B', 60, NULL, NULL, 2, ?, ?)`,
			[]any{day, day, day.Add(time.Hour), day.Add(time.Hour)}},
		{`INSERT INTO video_download_attempts (video_id, error_message, created_at, updated_at) VALUES
			(2, 'timeout', ?, ?), (2, 'timeout', ?, ?), (2, 'timeout', ?, ?), (2, 'timeout', ?, ?), (2, 'timeout', ?, ?)`,
			[]any{day, day, day, day, day, day, day, day, day, day}},
		{`INSERT INTO helpful_links (id, title, description, url, open_content_provider_id, thumbnail_url, created_at, updated_at)
			VALUES (3, 'Khan Academy', 'Free courses', 'https://www.khanacademy.org', 3, '/thumbs/khan.png', ?, ?)`, []any{day, day}},
		{`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES
			(?, 1, 1, true), (?, 2, 1, true), (?, 3, 3, true)`, []any{northID, northID, northID}},
		{`INSERT INTO open_content_favorites (user_id, content_id, open_content_provider_id, created_at) VALUES (?, 1, 1, ?)`,
			[]any{resident, day}},
		{`INSERT INTO open_content_urls (id, content_url) VALUES (1, '/api/proxy/libraries/1/content/algebra_fr/A/Lesson_1'), (2, '/viewer/videos/1')`, nil},
		{`INSERT INTO open_content_activities (open_content_provider_id, facility_id, user_id, content_id, open_content_url_id, request_ts, stop_ts) VALUES
			(1, ?, ?, 1, 1, ?, ?), (1, ?, ?, 1, 1, ?, ?), (2, ?, ?, 1, 2, ?, ?)`,
			[]any{
				northID, resident, recent, recent.Add(10 * time.Minute),
				northID, resident, recent.Add(time.Hour), recent.Add(70 * time.Minute),
				northID, resident, recent.Add(2 * time.Hour), recent.Add(122 * time.Minute),
			}},
	} {
		require.NoError(t, db.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}
}

// routeResponse is a route's answer.
type routeResponse struct {
	status int
	header http.Header
	body   string
}

// call sends a request as c, with body as JSON unless it is nil.
func call(t *testing.T, ts *httptest.Server, client *http.Client, c *identity.Claims, method, path string, body any) routeResponse {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, reqBody)
	require.NoError(t, err)
	claims, err := json.Marshal(c)
	require.NoError(t, err)
	req.Header.Set(testClaimsHeader, string(claims))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return routeResponse{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// data decodes the "data" of a JSON answer into a T.
func data[T any](t *testing.T, resp routeResponse) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.body), &body), resp.body)
	return body.Data
}

// message is the "message" of a JSON answer.
func message(t *testing.T, resp routeResponse) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.body), &body), resp.body)
	return body.Message
}

func TestRoutes(t *testing.T) {
	ts, client := newRouteTest(t)
	// The helpful-links sort order is kept in memory per facility, so it
	// outlives this test's database. Clear it for the next run.
	defer func() {
		resp := call(t, ts, client, facAdminClaims, http.MethodPut, "/api/helpful-links/sort", map[string]any{"sort_order": ""})
		require.Equal(t, http.StatusOK, resp.status, resp.body)
	}()
	visibilityBody := map[string]any{"facility_ids": []uint{northID, southID}, "visibility_status": true}

	// The cases run in order against one database, so later reads show the
	// earlier writes.
	tests := []struct {
		name   string
		claims *identity.Claims
		method string
		path   string
		body   any
		status int
	}{
		// Libraries
		{"libraries_resident", residentClaims, http.MethodGet, "/api/libraries", nil, http.StatusOK},
		{"libraries_admin_all", facAdminClaims, http.MethodGet, "/api/libraries?visibility=all", nil, http.StatusOK},
		{"libraries_admin_hidden", facAdminClaims, http.MethodGet, "/api/libraries?visibility=hidden", nil, http.StatusOK},
		{"libraries_resident_hidden", residentClaims, http.MethodGet, "/api/libraries?visibility=hidden", nil, http.StatusUnauthorized},
		{"libraries_resident_featured", residentClaims, http.MethodGet, "/api/libraries?visibility=featured", nil, http.StatusOK},
		{"libraries_admin_search_and_tag", facAdminClaims, http.MethodGet, "/api/libraries?visibility=all&search=bio&tags=1", nil, http.StatusOK},
		{"libraries_admin_paged", facAdminClaims, http.MethodGet, "/api/libraries?visibility=all&page=2&per_page=1&order_by=title", nil, http.StatusOK},
		{"library_by_id", residentClaims, http.MethodGet, "/api/libraries/1", nil, http.StatusOK},
		{"library_by_id_bad_id", residentClaims, http.MethodGet, "/api/libraries/abc", nil, http.StatusBadRequest},
		{"library_by_id_missing", residentClaims, http.MethodGet, "/api/libraries/999", nil, http.StatusBadRequest},
		{"tags", residentClaims, http.MethodGet, "/api/tags", nil, http.StatusOK},
		// The library proxy's own checks. A visible library would go on to
		// Kiwix, which the tests don't run.
		{"proxy_library_missing", residentClaims, http.MethodGet, "/api/proxy/libraries/999/", nil, http.StatusNotFound},
		{"proxy_library_hidden", residentClaims, http.MethodGet, "/api/proxy/libraries/2/", nil, http.StatusNotFound},
		{"library_favorite_toggle", residentClaims, http.MethodPut, "/api/libraries/2/favorite", map[string]any{}, http.StatusOK},
		{"library_feature_toggle", facAdminClaims, http.MethodPut, "/api/libraries/1/favorite", map[string]any{}, http.StatusOK},
		{"library_visibility_toggle", facAdminClaims, http.MethodPut, "/api/libraries/2/toggle", map[string]any{}, http.StatusOK},
		{"libraries_resident_after_toggles", residentClaims, http.MethodGet, "/api/libraries", nil, http.StatusOK},
		{"library_facilities", deptAdminClaims, http.MethodGet, "/api/libraries/1/facilities", nil, http.StatusOK},
		{"library_facilities_set", deptAdminClaims, http.MethodPut, "/api/libraries/1/facilities", visibilityBody, http.StatusOK},
		{"library_facilities_set_empty", deptAdminClaims, http.MethodPut, "/api/libraries/1/facilities", map[string]any{"facility_ids": []uint{}}, http.StatusBadRequest},
		{"library_facilities_after_set", deptAdminClaims, http.MethodGet, "/api/libraries/1/facilities", nil, http.StatusOK},

		// Search. The title search is Postgres full-text search and the
		// Kiwix search needs Kiwix, so on SQLite this pins only the shape of
		// an empty result.
		{"search_empty", residentClaims, http.MethodGet, "/api/open-content/search?search=algebra", nil, http.StatusOK},
		{"suggestions_no_query", residentClaims, http.MethodGet, "/api/open-content/suggestions", nil, http.StatusBadRequest},

		// Open content
		{"providers", residentClaims, http.MethodGet, "/api/open-content", nil, http.StatusOK},
		{"providers_admin_all", facAdminClaims, http.MethodGet, "/api/open-content?all=true", nil, http.StatusOK},
		{"bookmark", residentClaims, http.MethodPut, "/api/open-content/1/bookmark",
			map[string]any{"name": "Lesson 1", "content_url": "/api/proxy/libraries/1/content/algebra_fr/A/Lesson_1", "open_content_provider_id": 1}, http.StatusOK},
		{"bookmark_bad_id", residentClaims, http.MethodPut, "/api/open-content/abc/bookmark", map[string]any{}, http.StatusBadRequest},
		{"favorites", residentClaims, http.MethodGet, "/api/open-content/favorites", nil, http.StatusOK},
		{"favorite_groupings", residentClaims, http.MethodGet, "/api/open-content/favorite-groupings", nil, http.StatusOK},
		{"request_content_bad_body", residentClaims, http.MethodPost, "/api/open-content/request-content", "not an object", http.StatusBadRequest},

		// Activity
		{"activity_facility", facAdminClaims, http.MethodGet, "/api/open-content/activity", nil, http.StatusOK},
		{"activity_user", facAdminClaims, http.MethodGet, fmt.Sprintf("/api/open-content/activity/%d", residentClaims.UserID), nil, http.StatusOK},
		{"activity_user_bad_id", facAdminClaims, http.MethodGet, "/api/open-content/activity/abc", nil, http.StatusBadRequest},
		{"activity_libraries", facAdminClaims, http.MethodGet, "/api/libraries/activity", nil, http.StatusOK},

		// Videos
		{"videos_resident", residentClaims, http.MethodGet, "/api/videos", nil, http.StatusOK},
		{"videos_admin_all", facAdminClaims, http.MethodGet, "/api/videos?visibility=all", nil, http.StatusOK},
		// The video proxy, with no S3 bucket, redirects to the local file.
		{"proxy_video", residentClaims, http.MethodGet, "/api/proxy/videos/1", nil, http.StatusTemporaryRedirect},
		{"proxy_video_missing", residentClaims, http.MethodGet, "/api/proxy/videos/999", nil, http.StatusNotFound},
		{"video_by_id_admin", facAdminClaims, http.MethodGet, "/api/videos/1", nil, http.StatusOK},
		// Even an admin can't open a video that isn't available.
		{"video_by_id_admin_not_available", facAdminClaims, http.MethodGet, "/api/videos/2", nil, http.StatusForbidden},
		{"video_by_id_resident_not_available", residentClaims, http.MethodGet, "/api/videos/2", nil, http.StatusForbidden},
		{"video_by_id_bad_id", residentClaims, http.MethodGet, "/api/videos/abc", nil, http.StatusBadRequest},
		{"video_favorite", residentClaims, http.MethodPut, "/api/videos/1/favorite", map[string]any{}, http.StatusOK},
		{"video_feature", facAdminClaims, http.MethodPut, "/api/videos/1/feature", map[string]any{}, http.StatusOK},
		{"video_visibility_toggle", facAdminClaims, http.MethodPut, "/api/videos/1/visibility", map[string]any{}, http.StatusOK},
		{"video_retry_max_attempts", facAdminClaims, http.MethodPut, "/api/videos/2/retry", map[string]any{}, http.StatusBadRequest},
		{"video_action_unknown", facAdminClaims, http.MethodPut, "/api/videos/1/unknown", map[string]any{}, http.StatusBadRequest},
		{"video_action_missing", facAdminClaims, http.MethodPut, "/api/videos/999/feature", map[string]any{}, http.StatusBadRequest},
		{"videos_resident_after_toggle", residentClaims, http.MethodGet, "/api/videos", nil, http.StatusOK},
		{"videos_add_none", facAdminClaims, http.MethodPost, "/api/videos", map[string]any{"video_urls": []string{}}, http.StatusBadRequest},
		{"videos_add_bad_url", facAdminClaims, http.MethodPost, "/api/videos", map[string]any{"video_urls": []string{"ftp://example.com/v"}}, http.StatusBadRequest},
		{"video_facilities", deptAdminClaims, http.MethodGet, "/api/videos/1/facilities", nil, http.StatusOK},
		{"video_facilities_set", deptAdminClaims, http.MethodPut, "/api/videos/1/facilities", visibilityBody, http.StatusOK},
		{"video_facilities_after_set", deptAdminClaims, http.MethodGet, "/api/videos/1/facilities", nil, http.StatusOK},
		{"video_delete", facAdminClaims, http.MethodDelete, "/api/videos/2", nil, http.StatusOK},
		{"videos_admin_after_delete", facAdminClaims, http.MethodGet, "/api/videos?visibility=all", nil, http.StatusOK},

		// Helpful links
		{"links_resident", residentClaims, http.MethodGet, "/api/helpful-links", nil, http.StatusOK},
		{"links_admin", facAdminClaims, http.MethodGet, "/api/helpful-links", nil, http.StatusOK},
		{"link_add", facAdminClaims, http.MethodPut, "/api/helpful-links",
			map[string]any{"title": "Library of Congress", "description": "Public records", "url": "https://www.loc.gov"}, http.StatusCreated},
		{"link_edit", facAdminClaims, http.MethodPatch, "/api/helpful-links/3/edit",
			map[string]any{"title": "Khan Academy", "description": "Free courses and practice", "url": "https://www.khanacademy.org/math"}, http.StatusOK},
		{"link_visibility_toggle", facAdminClaims, http.MethodPut, "/api/helpful-links/toggle/3", map[string]any{}, http.StatusOK},
		{"link_facilities", deptAdminClaims, http.MethodGet, "/api/helpful-links/facilities/3", nil, http.StatusOK},
		{"link_facilities_set", deptAdminClaims, http.MethodPut, "/api/helpful-links/facilities/3", visibilityBody, http.StatusOK},
		{"link_facilities_after_set", deptAdminClaims, http.MethodGet, "/api/helpful-links/facilities/3", nil, http.StatusOK},
		{"link_activity", residentClaims, http.MethodPut, "/api/helpful-links/activity/3", map[string]any{}, http.StatusOK},
		{"link_activity_missing", residentClaims, http.MethodPut, "/api/helpful-links/activity/999", map[string]any{}, http.StatusBadRequest},
		{"link_favorite", residentClaims, http.MethodPut, "/api/helpful-links/favorite/3", map[string]any{}, http.StatusOK},
		{"links_sort", facAdminClaims, http.MethodPut, "/api/helpful-links/sort", map[string]any{"sort_order": "title ASC"}, http.StatusOK},
		{"links_admin_after_changes", facAdminClaims, http.MethodGet, "/api/helpful-links", nil, http.StatusOK},
		{"link_delete", facAdminClaims, http.MethodDelete, "/api/helpful-links/3", nil, http.StatusOK},
		{"links_resident_after_delete", residentClaims, http.MethodGet, "/api/helpful-links", nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := call(t, ts, client, tt.claims, tt.method, tt.path, tt.body)
			require.Equal(t, tt.status, resp.status, resp.body)
			expectGolden(t, tt.name, resp.body)
		})
	}
}

// TestRouteAccess pins each route's access level and feature flags, the part
// of a route most easily lost in a move.
func TestRouteAccess(t *testing.T) {
	ts, client := newRouteTest(t)

	routes := []struct {
		method, path string
		access       accessLevel
		features     []features.Access
	}{
		{http.MethodGet, "/api/libraries", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content/search", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content/suggestions", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/libraries/1", anyUser, []features.Access{features.OpenContent}},
		{http.MethodPut, "/api/libraries/1/favorite", anyUser, []features.Access{features.OpenContent}},
		{http.MethodPut, "/api/libraries/1/toggle", admin, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/libraries/1/facilities", deptAdmin, []features.Access{features.OpenContent}},
		{http.MethodPut, "/api/libraries/1/facilities", deptAdmin, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content/favorites", anyUser, []features.Access{features.OpenContent}},
		{http.MethodPut, "/api/open-content/1/bookmark", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content/favorite-groupings", anyUser, []features.Access{features.OpenContent}},
		{http.MethodPost, "/api/open-content/request-content", anyUser, []features.Access{features.OpenContent, features.RequestContent}},
		{http.MethodGet, "/api/open-content/activity", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/open-content/activity/1", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/libraries/activity", anyUser, []features.Access{features.OpenContent}},
		{http.MethodGet, "/api/videos", anyUser, []features.Access{features.UploadVideo}},
		{http.MethodGet, "/api/videos/1", anyUser, []features.Access{features.UploadVideo}},
		{http.MethodPut, "/api/videos/1/favorite", anyUser, []features.Access{features.UploadVideo}},
		{http.MethodPost, "/api/videos", admin, []features.Access{features.UploadVideo}},
		{http.MethodPut, "/api/videos/1/visibility", admin, []features.Access{features.UploadVideo}},
		{http.MethodGet, "/api/videos/1/facilities", deptAdmin, []features.Access{features.UploadVideo}},
		{http.MethodPut, "/api/videos/1/facilities", deptAdmin, []features.Access{features.UploadVideo}},
		{http.MethodDelete, "/api/videos/1", admin, []features.Access{features.UploadVideo}},
		{http.MethodGet, "/api/helpful-links", anyUser, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links", admin, []features.Access{features.HelpfulLinks}},
		{http.MethodPatch, "/api/helpful-links/1/edit", admin, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links/toggle/1", admin, []features.Access{features.HelpfulLinks}},
		{http.MethodDelete, "/api/helpful-links/1", admin, []features.Access{features.HelpfulLinks}},
		{http.MethodGet, "/api/helpful-links/facilities/1", deptAdmin, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links/facilities/1", deptAdmin, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links/activity/1", anyUser, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links/sort", admin, []features.Access{features.HelpfulLinks}},
		{http.MethodPut, "/api/helpful-links/favorite/1", anyUser, []features.Access{features.HelpfulLinks}},
		{http.MethodGet, "/api/tags", anyUser, nil},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// A department admin passes every role check, so a 401 here is
			// the feature check. Each required feature must be checked.
			for _, f := range rt.features {
				resp := call(t, ts, client, withFeatures(deptAdminClaims, f), rt.method, rt.path, map[string]any{})
				require.Equal(t, http.StatusUnauthorized, resp.status, resp.body)
				require.Equal(t, "Feature not enabled", message(t, resp))
			}
			switch rt.access {
			case anyUser:
				// A resident gets past every check. The body isn't valid JSON,
				// so a route that reads one stops there without side effects.
				resp := call(t, ts, client, residentClaims, rt.method, rt.path, "not an object")
				require.NotEqual(t, http.StatusUnauthorized, resp.status, resp.body)
			case admin:
				resp := call(t, ts, client, residentClaims, rt.method, rt.path, map[string]any{})
				require.Equal(t, http.StatusUnauthorized, resp.status, resp.body)
				require.Equal(t, "Unauthorized - not admin", message(t, resp))
			case deptAdmin:
				for _, c := range []*identity.Claims{residentClaims, facAdminClaims} {
					resp := call(t, ts, client, c, rt.method, rt.path, map[string]any{})
					require.Equal(t, http.StatusUnauthorized, resp.status, resp.body)
					require.Equal(t, "User is not allowed to view this resource\n", resp.body)
				}
			}
		})
	}

	// The proxies send a user without Open Content back to log in.
	for _, path := range []string{"/api/proxy/libraries/1/", "/api/proxy/videos/1"} {
		t.Run("GET "+path, func(t *testing.T) {
			resp := call(t, ts, client, withFeatures(residentClaims, features.OpenContent), http.MethodGet, path, nil)
			require.Equal(t, http.StatusSeeOther, resp.status, resp.body)
			require.Equal(t, authCallbackRoute, resp.header.Get("Location"))
		})
	}
}

// Only the proxies' pages may be framed, by the app itself, as core set it.
func TestSecurityHeaders(t *testing.T) {
	ts, client := newRouteTest(t)
	for path, frame := range map[string]string{
		"/api/libraries":        "DENY",
		"/api/proxy/videos/999": "SAMEORIGIN",
	} {
		resp := call(t, ts, client, residentClaims, http.MethodGet, path, nil)
		require.Equal(t, frame, resp.header.Get("X-Frame-Options"), path)
		require.Equal(t, "nosniff", resp.header.Get("X-Content-Type-Options"), path)
		require.Equal(t, "max-age=31536000; includeSubDomains", resp.header.Get("Strict-Transport-Security"), path)
		require.Equal(t, "strict-origin-when-cross-origin", resp.header.Get("Referrer-Policy"), path)
	}
}

// Each request is counted under its route's pattern, as core counted it,
// including the ones a check refuses, and /api/metrics serves the counts.
func TestMetrics(t *testing.T) {
	ts, client := newRouteTest(t)
	const route = "/api/libraries/{id}"
	ok := testutil.ToFloat64(responseStatus.WithLabelValues(route, http.MethodGet, "200"))
	refused := testutil.ToFloat64(errorCount.WithLabelValues("/api/libraries", http.MethodGet, "401"))

	for _, id := range []string{"1", "2"} {
		resp := call(t, ts, client, facAdminClaims, http.MethodGet, "/api/libraries/"+id, nil)
		require.Equal(t, http.StatusOK, resp.status, resp.body)
	}
	resp := call(t, ts, client, residentClaims, http.MethodGet, "/api/libraries?visibility=hidden", nil)
	require.Equal(t, http.StatusUnauthorized, resp.status, resp.body)

	assert.Equal(t, ok+2, testutil.ToFloat64(responseStatus.WithLabelValues(route, http.MethodGet, "200")), "one series for every library")
	assert.Equal(t, refused+1, testutil.ToFloat64(errorCount.WithLabelValues("/api/libraries", http.MethodGet, "401")))

	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	assert.Contains(t, rec.Body.String(), `http_requests_total{path="/api/libraries/{id}"}`)
}

// withFeatures returns the claims with every feature on except the ones left
// out.
func withFeatures(c *identity.Claims, off ...features.Access) *identity.Claims {
	cp := *c
	cp.FeatureAccess = []features.Access{}
	for _, f := range features.All {
		if !slices.Contains(off, f) {
			cp.FeatureAccess = append(cp.FeatureAccess, f)
		}
	}
	return &cp
}

// timeKey matches the JSON fields that hold a time that changes every run.
var timeKey = regexp.MustCompile(`(_at|_ts)$`)

// expectGolden compares body with testdata/routes/<name>.golden. A JSON body
// is compared after indenting it and replacing its times with "<time>".
func expectGolden(t *testing.T, name, body string) {
	t.Helper()
	got := normalizeBody(t, body)
	path := filepath.Join("testdata", "routes", name+".golden")
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing saved response; run with -update to create it")
	require.Equal(t, string(want), got)
}

func normalizeBody(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(maskTimes(v)))
	return buf.String()
}

// maskTimes replaces every time with "<time>", except a zero time, which is
// kept because it shows a field the route never set.
func maskTimes(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && timeKey.MatchString(k) && !strings.HasPrefix(s, "0001-01-01") {
				v[k] = "<time>"
			} else {
				v[k] = maskTimes(x)
			}
		}
	case []any:
		for i, x := range v {
			v[i] = maskTimes(x)
		}
	}
	return v
}
