package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/stretchr/testify/require"
)

// These tests came from core's tests/integration (library_facility_visibility,
// content_facility_visibility and favorites_feature_gating) in EN-205.

// The JSON shapes these tests read.
type facilityVisibility struct {
	FacilityID       uint   `json:"facility_id"`
	FacilityName     string `json:"facility_name"`
	VisibilityStatus bool   `json:"visibility_status"`
}

type contentListItem struct {
	ID                   uint `json:"id"`
	VisibilityStatus     bool `json:"visibility_status"`
	VisibleFacilityCount int  `json:"visible_facility_count"`
}

// Core's default facility is 1; seedCore adds the others after it.
const (
	facilityA = 2
	facilityB = 3
	facilityC = 4
)

// insertID runs an INSERT and returns the new row's id.
func insertID(t *testing.T, db *database.DB, sql string, args ...any) uint {
	t.Helper()
	var id uint
	require.NoError(t, db.Raw(sql+" RETURNING id", args...).Scan(&id).Error, sql)
	require.NotZero(t, id, sql)
	return id
}

// expectStatus fails the test unless resp has status.
func expectStatus(t *testing.T, resp routeResponse, status int) routeResponse {
	t.Helper()
	require.Equal(t, status, resp.status, resp.body)
	return resp
}

// statusAt is the visibility at facilityID in rows.
func statusAt(t *testing.T, rows []facilityVisibility, facilityID uint) bool {
	t.Helper()
	for _, row := range rows {
		if row.FacilityID == facilityID {
			return row.VisibilityStatus
		}
	}
	t.Fatalf("facility %d not found in response", facilityID)
	return false
}

func TestLibraryFacilityVisibility(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A", "Facility B", "Facility C")
	ts, client := newRouteServer(t, db)

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', false)`)
	libraryID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url) VALUES (?, 'Test Library', '/test')`, kiwixID)

	dept := &identity.Claims{UserID: 10, Role: users.DepartmentAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	fac := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	visibilityURL := fmt.Sprintf("/api/libraries/%d/facilities", libraryID)
	visibility := func(t *testing.T) []facilityVisibility {
		t.Helper()
		return data[[]facilityVisibility](t, expectStatus(t, call(t, ts, client, dept, http.MethodGet, visibilityURL, nil), http.StatusOK))
	}
	set := func(t *testing.T, c *identity.Claims, ids []uint, visible bool) routeResponse {
		t.Helper()
		return call(t, ts, client, c, http.MethodPut, visibilityURL, map[string]any{"facility_ids": ids, "visibility_status": visible})
	}

	t.Run("new library is hidden at all facilities", func(t *testing.T) {
		rows := visibility(t)
		require.GreaterOrEqual(t, len(rows), 3)
		for _, row := range rows {
			require.False(t, row.VisibilityStatus)
		}
	})

	t.Run("dept admin sets visibility for a subset of facilities", func(t *testing.T) {
		expectStatus(t, set(t, dept, []uint{facilityA, facilityB}, true), http.StatusOK)
		rows := visibility(t)
		require.True(t, statusAt(t, rows, facilityA))
		require.True(t, statusAt(t, rows, facilityB))
		require.False(t, statusAt(t, rows, facilityC))
	})

	t.Run("library list includes visible facility count", func(t *testing.T) {
		libs := data[[]contentListItem](t, expectStatus(t, call(t, ts, client, dept, http.MethodGet, "/api/libraries?visibility=all", nil), http.StatusOK))
		require.Len(t, libs, 1)
		require.Equal(t, 2, libs[0].VisibleFacilityCount)
	})

	t.Run("dept admin hides at all facilities", func(t *testing.T) {
		expectStatus(t, set(t, dept, []uint{facilityA, facilityB, facilityC}, false), http.StatusOK)
		for _, row := range visibility(t) {
			require.False(t, row.VisibilityStatus)
		}
	})

	t.Run("duplicate facility_ids are handled", func(t *testing.T) {
		expectStatus(t, set(t, dept, []uint{facilityA, facilityA}, true), http.StatusOK)
		require.True(t, statusAt(t, visibility(t), facilityA))
		expectStatus(t, set(t, dept, []uint{facilityA}, false), http.StatusOK)
	})

	t.Run("empty facility_ids is rejected", func(t *testing.T) {
		expectStatus(t, set(t, dept, []uint{}, true), http.StatusBadRequest)
	})

	t.Run("facility admin cannot access facility visibility endpoints", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, fac, http.MethodGet, visibilityURL, nil), http.StatusUnauthorized)
		expectStatus(t, set(t, fac, []uint{facilityA}, true), http.StatusUnauthorized)
	})

	t.Run("facility admin toggle still works for own facility", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, fac, http.MethodPut, fmt.Sprintf("/api/libraries/%d/toggle", libraryID), map[string]any{}), http.StatusOK)
		rows := visibility(t)
		require.True(t, statusAt(t, rows, facilityA))
		require.False(t, statusAt(t, rows, facilityB))
	})
}

// The library proxy's cache holds a library's visibility at each facility,
// so a resident only opens a library visible at their own facility, and an
// admin's change reaches every facility it was made for.
func TestLibraryProxyCache(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Facility A", "Facility B")
	srv := &server{db: db, notify: &fakeNotifier{}, client: &http.Client{}, libraryPaths: newFakeKV()}
	ts := httptest.NewServer(newRouter(srv.routes(), testLogin))
	t.Cleanup(ts.Close)
	client := ts.Client()

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', true)`)
	libraryID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url) VALUES (?, 'Test Library', '/test')`, kiwixID)
	id := strconv.Itoa(int(libraryID))
	dept := &identity.Claims{UserID: 10, Role: users.DepartmentAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	set := func(t *testing.T, ids []uint, visible bool) {
		t.Helper()
		expectStatus(t, call(t, ts, client, dept, http.MethodPut, "/api/libraries/"+id+"/facilities", map[string]any{"facility_ids": ids, "visibility_status": visible}), http.StatusOK)
	}
	visible := func(t *testing.T, facilityID uint) bool {
		t.Helper()
		params, ok := srv.libraryProxyParams(id, facilityID)
		require.True(t, ok)
		return params.VisibilityStatus
	}

	set(t, []uint{facilityA}, true)
	require.True(t, visible(t, facilityA))
	require.False(t, visible(t, facilityB), "hidden at B though cached as visible at A")

	t.Run("a change at another facility", func(t *testing.T) {
		set(t, []uint{facilityB}, true)
		require.True(t, visible(t, facilityB))
		require.True(t, visible(t, facilityA))
	})

	t.Run("a toggle at the admin's facility", func(t *testing.T) {
		fac := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
		expectStatus(t, call(t, ts, client, fac, http.MethodPut, "/api/libraries/"+id+"/toggle", map[string]any{}), http.StatusOK)
		require.False(t, visible(t, facilityA))
		require.True(t, visible(t, facilityB))
	})

	t.Run("a read from before an admin's change", func(t *testing.T) {
		srv.cacheLibraryProxy(libraryCacheKey(id, facilityA), &models.LibraryProxyPO{ID: uint(libraryID), VisibilityStatus: true})
		require.False(t, visible(t, facilityA))
	})
}

func TestVideoFacilityVisibility(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Video Facility A", "Video Facility B")
	ts, client := newRouteServer(t, db)

	youtubeID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('YouTube', 'http://youtube', false)`)
	videoID := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, url, external_id) VALUES (?, 'Test Video', '/vid', 'vid1')`, youtubeID)

	dept := &identity.Claims{UserID: 10, Role: users.DepartmentAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	fac := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	visibilityURL := fmt.Sprintf("/api/videos/%d/facilities", videoID)
	visibility := func(t *testing.T) []facilityVisibility {
		t.Helper()
		rows := data[[]facilityVisibility](t, expectStatus(t, call(t, ts, client, dept, http.MethodGet, visibilityURL, nil), http.StatusOK))
		require.GreaterOrEqual(t, len(rows), 2)
		return rows
	}

	t.Run("dept admin sets video visibility per facility", func(t *testing.T) {
		for _, row := range visibility(t) {
			require.False(t, row.VisibilityStatus)
		}
		expectStatus(t, call(t, ts, client, dept, http.MethodPut, visibilityURL, map[string]any{"facility_ids": []uint{facilityA}, "visibility_status": true}), http.StatusOK)
		for _, row := range visibility(t) {
			require.Equal(t, row.FacilityID == facilityA, row.VisibilityStatus)
		}
	})

	t.Run("video list includes visible facility count", func(t *testing.T) {
		vids := data[[]contentListItem](t, expectStatus(t, call(t, ts, client, dept, http.MethodGet, "/api/videos?visibility=all", nil), http.StatusOK))
		require.Len(t, vids, 1)
		require.Equal(t, 1, vids[0].VisibleFacilityCount)
	})

	t.Run("facility admin cannot access video facility endpoints", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, fac, http.MethodGet, visibilityURL, nil), http.StatusUnauthorized)
		expectStatus(t, call(t, ts, client, fac, http.MethodPut, visibilityURL, map[string]any{"facility_ids": []uint{facilityA}, "visibility_status": false}), http.StatusUnauthorized)
	})

	t.Run("facility admin toggle still works for own facility", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, fac, http.MethodPut, fmt.Sprintf("/api/videos/%d/visibility", videoID), map[string]any{}), http.StatusOK)
		for _, row := range visibility(t) {
			require.False(t, row.VisibilityStatus)
		}
	})
}

func TestHelpfulLinkFacilityVisibility(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Link Facility A", "Link Facility B")
	ts, client := newRouteServer(t, db)
	insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('HelpfulLinks', 'https://helpful_links', false)`)

	dept := &identity.Claims{UserID: 10, Role: users.DepartmentAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	facA := &identity.Claims{UserID: 11, Role: users.FacilityAdmin, FacilityID: facilityA, FeatureAccess: features.All}
	facB := &identity.Claims{UserID: 12, Role: users.FacilityAdmin, FacilityID: facilityB, FeatureAccess: features.All}

	t.Run("facility admin adds a link visible at own facility only", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, facA, http.MethodPut, "/api/helpful-links",
			map[string]any{"title": "Khan", "url": "https://khanacademy.org", "description": "learning"}), http.StatusCreated)
	})

	var link struct {
		ID                    uint
		OpenContentProviderID uint
	}
	require.NoError(t, db.Raw(`SELECT id, open_content_provider_id FROM helpful_links WHERE url = ?`, "https://khanacademy.org").Scan(&link).Error)
	require.NotZero(t, link.ID)
	visibilityURL := fmt.Sprintf("/api/helpful-links/facilities/%d", link.ID)
	visibility := func(t *testing.T) []facilityVisibility {
		t.Helper()
		rows := data[[]facilityVisibility](t, expectStatus(t, call(t, ts, client, dept, http.MethodGet, visibilityURL, nil), http.StatusOK))
		require.GreaterOrEqual(t, len(rows), 2)
		return rows
	}

	t.Run("link is visible at creator facility, hidden elsewhere", func(t *testing.T) {
		for _, row := range visibility(t) {
			require.Equal(t, row.FacilityID == facilityA, row.VisibilityStatus)
		}
	})

	t.Run("dept admin extends link visibility to another facility", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, dept, http.MethodPut, visibilityURL, map[string]any{"facility_ids": []uint{facilityB}, "visibility_status": true}), http.StatusOK)
		type linkList struct {
			Links []contentListItem `json:"helpful_links"`
		}
		resp := data[linkList](t, expectStatus(t, call(t, ts, client, facB, http.MethodGet, "/api/helpful-links?visibility=true", nil), http.StatusOK))
		require.Len(t, resp.Links, 1)
		require.True(t, resp.Links[0].VisibilityStatus)
		require.Equal(t, 2, resp.Links[0].VisibleFacilityCount)
	})

	t.Run("facility admin toggles link at own facility only", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, facA, http.MethodPut, fmt.Sprintf("/api/helpful-links/toggle/%d", link.ID), map[string]any{}), http.StatusOK)
		for _, row := range visibility(t) {
			require.Equal(t, row.FacilityID == facilityB, row.VisibilityStatus)
		}
	})

	t.Run("facility admin cannot access link facility endpoints", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, facA, http.MethodGet, visibilityURL, nil), http.StatusUnauthorized)
		expectStatus(t, call(t, ts, client, facA, http.MethodPut, visibilityURL, map[string]any{"facility_ids": []uint{facilityA}, "visibility_status": true}), http.StatusUnauthorized)
	})

	t.Run("deleting a link cleans up visibility rows", func(t *testing.T) {
		expectStatus(t, call(t, ts, client, facA, http.MethodDelete, fmt.Sprintf("/api/helpful-links/%d", link.ID), nil), http.StatusOK)
		var count int64
		require.NoError(t, db.Table("facility_visibility_statuses").
			Where("content_id = ? AND open_content_provider_id = ?", link.ID, link.OpenContentProviderID).
			Count(&count).Error)
		require.Zero(t, count)
	})
}

// A favorited video or helpful link must not come back from the favorites
// queries when its sub-feature is disabled for the caller's facility. Both
// routes are gated on the parent (open_content) only, which has to stay on
// for libraries, so without a per-content-type filter the card metadata leaks
// even though /api/videos and /api/helpful-links correctly 401.
func TestUserFavorites_RespectSubFeatureAccess(t *testing.T) {
	db := newTestDB(t)
	seedCore(t, db, "Favorites Gating Facility")
	ts, client := newRouteServer(t, db)
	const facilityID, userID = 2, 20

	// The favorites queries join on currently_enabled = TRUE, so these have to be
	// enabled or every branch comes back empty.
	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix-fav', true)`)
	youtubeID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('YouTube', 'http://youtube-fav', true)`)
	linksProviderID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('HelpfulLinks', 'https://helpful_links_fav', true)`)

	libraryID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url) VALUES (?, 'Fav Library', '/fav-library')`, kiwixID)
	videoID := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, url, external_id, availability) VALUES (?, 'Fav Video', '/fav-video', 'fav-vid-1', 'available')`, youtubeID)
	linkID := insertID(t, db, `INSERT INTO helpful_links (open_content_provider_id, title, url, description) VALUES (?, 'Fav Link', 'https://example.org/fav-link', 'a link')`, linksProviderID)

	for _, fav := range [][2]uint{{libraryID, kiwixID}, {videoID, youtubeID}, {linkID, linksProviderID}} {
		require.NoError(t, db.Exec(`INSERT INTO open_content_favorites (user_id, content_id, open_content_provider_id, facility_id, created_at) VALUES (?, ?, ?, ?, ?)`,
			userID, fav[0], fav[1], facilityID, time.Now()).Error)
	}

	type favorite struct {
		ContentType string `json:"content_type"`
	}
	types := func(items []favorite) map[string]bool {
		seen := map[string]bool{}
		for _, i := range items {
			seen[i.ContentType] = true
		}
		return seen
	}
	// favoritesWith calls both favorites routes as the user, with only the
	// given features on.
	favoritesWith := func(t *testing.T, on ...features.Access) (favs, groupings map[string]bool) {
		t.Helper()
		c := &identity.Claims{UserID: userID, Role: users.Student, FacilityID: facilityID, FeatureAccess: on}
		favs = types(data[[]favorite](t, expectStatus(t, call(t, ts, client, c, http.MethodGet, "/api/open-content/favorites?per_page=50", nil), http.StatusOK)))
		groupings = types(data[[]favorite](t, expectStatus(t, call(t, ts, client, c, http.MethodGet, "/api/open-content/favorite-groupings", nil), http.StatusOK)))
		return favs, groupings
	}

	all := []features.Access{features.OpenContent, features.UploadVideo, features.HelpfulLinks}

	t.Run("all sub-features on returns every content type", func(t *testing.T) {
		got, groupings := favoritesWith(t, all...)
		require.True(t, got["library"], "library must be present")
		require.True(t, got["video"], "video must be present")
		require.True(t, got["helpful_link"], "helpful link must be present")

		require.True(t, groupings["library"])
		require.True(t, groupings["video"])
		require.True(t, groupings["helpful_link"])
	})

	t.Run("upload_video off hides favorited videos but keeps libraries", func(t *testing.T) {
		got, groupings := favoritesWith(t, features.OpenContent, features.HelpfulLinks)
		require.False(t, got["video"], "a favorited video must not leak while upload_video is off")
		require.True(t, got["library"], "libraries must still come back")
		require.True(t, got["helpful_link"], "helpful links must still come back")

		require.False(t, groupings["video"])
		require.True(t, groupings["library"])
		require.True(t, groupings["helpful_link"])
	})

	t.Run("helpful_links off hides favorited links but keeps libraries", func(t *testing.T) {
		got, groupings := favoritesWith(t, features.OpenContent, features.UploadVideo)
		require.False(t, got["helpful_link"], "a favorited helpful link must not leak while helpful_links is off")
		require.True(t, got["library"])
		require.True(t, got["video"])

		require.False(t, groupings["helpful_link"])
		require.True(t, groupings["library"])
		require.True(t, groupings["video"])
	})

	t.Run("both sub-features off still returns libraries", func(t *testing.T) {
		got, _ := favoritesWith(t, features.OpenContent)
		require.True(t, got["library"], "over-filtering would break the parent feature entirely")
		require.False(t, got["video"])
		require.False(t, got["helpful_link"])
	})

	t.Run("hiding is not deleting - re-enabling restores the favorites", func(t *testing.T) {
		got, _ := favoritesWith(t, all...)
		require.True(t, got["video"], "the favorite row must survive being filtered out")
		require.True(t, got["helpful_link"])
	})
}
