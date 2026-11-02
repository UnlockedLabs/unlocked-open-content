package opencontent

import (
	"testing"
	"time"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/stretchr/testify/require"
)

// The Knowledge Center's numbers for a facility and window. This came from
// core's tests/integration/knowledge_center_test.go in EN-205, which checked
// them through core's page.
func TestKnowledgeCenterAnswers(t *testing.T) {
	db := newTestDB(t)
	const facilityID, otherFacilityID = 2, 3
	const u1, u2, u3 = 11, 12, 13

	kiwixID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('Kiwix', 'http://kiwix', false)`)
	youtubeID := insertID(t, db, `INSERT INTO open_content_providers (title, url, currently_enabled) VALUES ('YouTube', 'http://youtube', false)`)
	careerLibID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url) VALUES (?, 'Career Library', '/career')`, kiwixID)
	recoveryLibID := insertID(t, db, `INSERT INTO libraries (open_content_provider_id, title, url) VALUES (?, 'Recovery Library', '/recovery')`, kiwixID)
	weldingVideoID := insertID(t, db, `INSERT INTO videos (open_content_provider_id, title, url, external_id) VALUES (?, 'Welding Basics', '/welding', 'vid1')`, youtubeID)

	vocationalID := insertID(t, db, `INSERT INTO tags (name) VALUES ('Vocational')`)
	lifeSkillsID := insertID(t, db, `INSERT INTO tags (name) VALUES ('Life Skills')`)
	for _, tag := range [][2]uint{{vocationalID, careerLibID}, {lifeSkillsID, recoveryLibID}} {
		require.NoError(t, db.Exec(`INSERT INTO open_content_tags (tag_id, content_id, open_content_provider_id) VALUES (?, ?, ?)`,
			tag[0], tag[1], kiwixID).Error)
	}

	day := func(hour, min int) time.Time { return time.Date(2026, 1, 15, hour, min, 0, 0, time.UTC) }
	// The prior window for [01-15, 01-17) is [01-13, 01-15); seed on 01-14.
	priorDay := func(hour, min int) time.Time { return time.Date(2026, 1, 14, hour, min, 0, 0, time.UTC) }
	type activity struct {
		facilityID, userID, providerID, contentID uint
		requestTS, stopTS                         time.Time
	}
	for _, a := range []activity{
		// U1: career library twice and one video (3 interactions)
		{facilityID, u1, kiwixID, careerLibID, day(9, 0), day(9, 10)},
		{facilityID, u1, kiwixID, careerLibID, day(14, 0), day(14, 5)},
		{facilityID, u1, youtubeID, weldingVideoID, day(16, 0), day(16, 10)},
		// U2: recovery library once
		{facilityID, u2, kiwixID, recoveryLibID, day(10, 0), day(10, 20)},
		// U3: video once
		{facilityID, u3, youtubeID, weldingVideoID, day(11, 0), day(11, 30)},
		// The prior window: career once, video three times, so a total of 4
		// (recovery has none)
		{facilityID, u1, kiwixID, careerLibID, priorDay(9, 0), priorDay(9, 5)},
		{facilityID, u1, youtubeID, weldingVideoID, priorDay(9, 0), priorDay(9, 5)},
		{facilityID, u2, youtubeID, weldingVideoID, priorDay(10, 0), priorDay(10, 5)},
		{facilityID, u3, youtubeID, weldingVideoID, priorDay(11, 0), priorDay(11, 5)},
		// Another facility's activity, which never counts
		{otherFacilityID, 14, kiwixID, careerLibID, day(9, 0), day(9, 30)},
	} {
		require.NoError(t, db.Exec(`INSERT INTO open_content_activities
			(facility_id, user_id, open_content_provider_id, content_id, open_content_url_id, request_ts, stop_ts)
			VALUES (?, ?, ?, ?, 1, ?, ?)`, a.facilityID, a.userID, a.providerID, a.contentID, a.requestTS, a.stopTS).Error)
	}

	oc := NewAPI(db)
	ctx := t.Context()
	fid := uint(facilityID)
	start, end := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC)
	w := api.Window{Start: &start, End: &end, FacilityID: &fid}

	interactions, err := oc.Interactions(ctx, w)
	require.NoError(t, err)
	// The prior window's total is 4, so the change is (5-4)/4 = 25%.
	require.Equal(t, api.Interactions{Total: 5, Unique: 3, Change: 25}, interactions)

	avg, err := oc.AvgSessionMinutes(ctx, w)
	require.NoError(t, err)
	// The durations are 10, 5, 10, 20 and 30 minutes.
	require.InDelta(t, 15.0, avg, 0.01)

	repeat, err := oc.RepeatEngagement(ctx, w)
	require.NoError(t, err)
	// Interactions per resident: u1 3 (2 to 4), u2 1 and u3 1 (once).
	require.Equal(t, api.RepeatEngagement{Once: 2, TwoToFour: 1, FivePlus: 0}, repeat)

	categories, err := oc.LibraryViewsByCategory(ctx, w)
	require.NoError(t, err)
	require.Equal(t, []api.CategoryViews{{Category: "Vocational", Views: 2}, {Category: "Life Skills", Views: 1}}, categories)

	libraries, err := oc.TopLibraryVisits(ctx, w, 8)
	require.NoError(t, err)
	// Career had 1 visit before, so +100%; recovery had none, so 0%.
	require.Equal(t, []api.ContentVisits{{Title: "Career Library", Visits: 2, Change: 100}, {Title: "Recovery Library", Visits: 1, Change: 0}}, libraries)

	videos, err := oc.TopVideoVisits(ctx, w, 8)
	require.NoError(t, err)
	// Welding had 3 visits before: (2-3)/3 = -33%.
	require.Equal(t, []api.ContentVisits{{Title: "Welding Basics", Visits: 2, Change: -33}}, videos)
}

// Each facility's Open Content data, which blocks deleting it. This came from
// core's tests/integration/open_content_core_reads_test.go in EN-205.
func TestFacilityContentAnswers(t *testing.T) {
	db := newTestDB(t)
	const north, reading, favorites, visibility, empty = 2, 3, 4, 5, 6
	const resident, deptAdmin = 7, 8
	day := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO libraries (id, open_content_provider_id, external_id, title, language, description, url, thumbnail_url, created_at, updated_at)
			VALUES (1, 1, 'lib-algebra', 'Algebra', 'eng', 'Algebra for everyone', '/content/algebra', NULL, ?, ?)`, []any{day, day}},
		{`INSERT INTO open_content_urls (id, content_url) VALUES (1, '/api/proxy/libraries/1/content/algebra/A/Lesson_1')`, nil},
		{`INSERT INTO open_content_activities (open_content_provider_id, facility_id, user_id, content_id, open_content_url_id, request_ts, stop_ts) VALUES
			(1, ?, ?, 1, 1, ?, ?), (1, ?, ?, 1, 1, ?, ?)`,
			[]any{north, resident, day, day.Add(30 * time.Minute), reading, resident, day.Add(time.Hour), day.Add(90 * time.Minute)}},
		// A resident's own favorite has no facility, so it belongs to none.
		{`INSERT INTO open_content_favorites (user_id, content_id, open_content_provider_id, facility_id, created_at) VALUES
			(?, 1, 1, NULL, ?), (?, 1, 1, ?, ?), (?, 1, 1, ?, ?)`,
			[]any{resident, day, deptAdmin, north, day, deptAdmin, favorites, day}},
		{`INSERT INTO facility_visibility_statuses (facility_id, open_content_provider_id, content_id, visibility_status) VALUES
			(?, 1, 1, true), (?, 1, 1, true)`, []any{north, visibility}},
	} {
		require.NoError(t, db.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}

	content, err := NewAPI(db).FacilityContent(t.Context(), []uint{north, reading, favorites, visibility, empty})
	require.NoError(t, err)
	require.Equal(t, map[uint]api.FacilityContent{
		north:      {Activities: 1, Favorites: 1, VisibilityStatuses: 1},
		reading:    {Activities: 1},
		favorites:  {Favorites: 1},
		visibility: {VisibilityStatuses: 1},
	}, content)
}

// The average leaves out visits that never ended or end before they start,
// and is 0 with no visits, as it was when Go averaged the rows.
func TestAvgSessionMinutes(t *testing.T) {
	db := newTestDB(t)
	oc := NewAPI(db)
	avg, err := oc.AvgSessionMinutes(t.Context(), api.Window{})
	require.NoError(t, err)
	require.Zero(t, avg)

	start := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO open_content_activities (facility_id, user_id, open_content_provider_id, content_id, open_content_url_id, request_ts, stop_ts)
			VALUES (2, 1, 1, 1, 1, ?, ?), (2, 1, 1, 1, 1, ?, ?), (2, 1, 1, 1, 1, ?, ?)`,
			[]any{start, start.Add(10 * time.Minute), start, start.Add(30 * time.Minute), start, start.Add(-5 * time.Minute)}},
		{`INSERT INTO open_content_activities (facility_id, user_id, open_content_provider_id, content_id, open_content_url_id, request_ts)
			VALUES (2, 1, 1, 1, 1, ?)`, []any{start}},
	} {
		require.NoError(t, db.Exec(stmt.sql, stmt.args...).Error, stmt.sql)
	}
	avg, err = oc.AvgSessionMinutes(t.Context(), api.Window{})
	require.NoError(t, err)
	require.InDelta(t, 20.0, avg, 0.01)
}
