package database

import (
	"context"
	"database/sql"

	api "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"gorm.io/gorm"
)

// The queries behind core's questions (api.OpenContent): a resident's
// dashboard, a facility's content, and the Knowledge Center.

func (db *DB) GetTopLibraries(ctx context.Context, userID, facilityID uint) ([]api.ContentUsage, error) {
	libraries := make([]api.ContentUsage, 0, 5)
	query := db.WithContext(ctx).Table("libraries lib ").
		Select(`lib.id as content_id,lib.title,
			lib.url,
			lib.thumbnail_url,
			CASE WHEN fvs.visibility_status IS NULL THEN false
				ELSE fvs.visibility_status
			END AS visibility_status,
			lib.open_content_provider_id,
			CASE WHEN ocf.facility_id IS NOT NULL AND ocf.facility_id = u.facility_id THEN true
				ELSE false
			END as is_featured,
			SUM(EXTRACT(EPOCH FROM oca.duration) / 3600) AS total_hours,
			SUM(EXTRACT(EPOCH FROM oca.duration) / 60) AS total_minutes
		`).
		Joins(`join open_content_providers ocp ON ocp.id = lib.open_content_provider_id
				AND ocp.currently_enabled = TRUE
				AND ocp.deleted_at IS NULL`).
		Joins(`join open_content_activities oca on oca.open_content_provider_id = ocp.id
				and oca.content_id = lib.id`).
		Joins(`join users u on u.id = oca.user_id
				and u.id = ?`, userID).
		Joins(`left outer join open_content_favorites ocf on ocf.open_content_provider_id = ocp.id
				and ocf.content_id = lib.id
				and ocf.facility_id IS NOT NULL`).
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = lib.open_content_provider_id
			and fvs.content_id = lib.id
			and fvs.facility_id = ?`, facilityID).
		Where("oca.user_id = ?", userID).
		Group("lib.title, lib.url, lib.thumbnail_url, fvs.visibility_status, lib.open_content_provider_id, ocf.facility_id, u.facility_id, lib.id").
		Order("8 desc").
		Limit(5)
	if err := query.Find(&libraries).Error; err != nil {
		return nil, err
	}
	return libraries, nil
}

func (db *DB) GetRecentVideos(ctx context.Context, userID uint) ([]api.ContentUsage, error) {
	videos := make([]api.ContentUsage, 0, 5)
	query := `with RecentVideos as (
		select oca.content_id
		from videos vid
		join open_content_providers ocp on ocp.id = vid.open_content_provider_id
			and ocp.currently_enabled = true
			and ocp.deleted_at IS NULL
		join open_content_activities oca on oca.content_id = vid.id
			and oca.open_content_provider_id = ocp.id
		where oca.user_id = ?
		and oca.request_ts = (
			select max(sub_oca.request_ts)
			from open_content_activities sub_oca
			where sub_oca.content_id = oca.content_id
			and sub_oca.user_id = oca.user_id
		)
		order by oca.request_ts desc
		limit 5
	),
	VideoWatchTime as (
		select oca.content_id,
		SUM(EXTRACT(EPOCH FROM oca.duration) / 60) AS total_minutes
		from open_content_activities oca
		join open_content_providers ocp on ocp.id = oca.open_content_provider_id
			and ocp.currently_enabled = true
			and ocp.deleted_at IS NULL
		join videos vid on vid.id = oca.content_id
			and vid.open_content_provider_id = ocp.id
		where oca.content_id in (select content_id from RecentVideos)
			and oca.user_id = ?
		group by oca.content_id
	)
	select vid.title, vid.url,
		vid.thumbnail_url,vid.description,
		vid.channel_title,
		vwt.total_minutes,
		vwt.total_minutes / 60 as total_hours
	from videos vid
	join VideoWatchTime vwt on vwt.content_id = vid.id
	order by vwt.total_minutes desc
	limit 5`

	if err := db.WithContext(ctx).Raw(query, userID, userID).Scan(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

func (db *DB) CountResourcesAccessed(ctx context.Context, userID uint) (int64, error) {
	var total int64
	if err := db.WithContext(ctx).
		Table("open_content_activities oca").
		Select("COUNT(DISTINCT (oca.open_content_provider_id, oca.content_id)) AS total_resources_accessed").
		Joins("inner join open_content_providers ocp ON ocp.id = oca.open_content_provider_id AND ocp.deleted_at IS NULL").
		Where("oca.user_id = ?", userID).
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (db *DB) GetActivityEngagement(ctx context.Context, userID uint) (api.ActivityEngagement, error) {
	var engagement api.ActivityEngagement
	err := db.WithContext(ctx).
		Table("open_content_activities oca").
		Select(`COUNT(DISTINCT CAST(oca.request_ts AS DATE)) AS total_active_days_monthly,
		AVG(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600) AS total_hours_active_monthly,
		SUM(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600) / 4 AS total_hours_active_weekly,
		SUM(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 60) / 4 AS total_minutes_active_weekly,
		SUM(
			CASE
				WHEN oca.request_ts >= date_trunc('week', CURRENT_DATE)
				THEN EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600
				ELSE 0
			END
		) AS total_hours_engaged,
		SUM(
			CASE
				WHEN oca.request_ts >= date_trunc('week', CURRENT_DATE)
				THEN EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 60
				ELSE 0
			END
		) AS total_minutes_engaged`).
		Where("oca.user_id = ?", userID).
		Scan(&engagement).Error
	return engagement, err
}

func (db *DB) GetFacilityContent(ctx context.Context, facilityIDs []uint) (map[uint]api.FacilityContent, error) {
	content := make(map[uint]api.FacilityContent)
	if len(facilityIDs) == 0 {
		return content, nil
	}
	counts := []struct {
		table string
		set   func(*api.FacilityContent, int64)
	}{
		{"open_content_activities", func(c *api.FacilityContent, n int64) { c.Activities = n }},
		{"open_content_favorites", func(c *api.FacilityContent, n int64) { c.Favorites = n }},
		{"facility_visibility_statuses", func(c *api.FacilityContent, n int64) { c.VisibilityStatuses = n }},
	}
	for _, count := range counts {
		var rows []struct {
			FacilityID uint
			Count      int64
		}
		if err := db.WithContext(ctx).
			Table(count.table).
			Select("facility_id, COUNT(*) AS count").
			Where("facility_id IN ?", facilityIDs).
			Group("facility_id").
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			c := content[row.FacilityID]
			count.set(&c, row.Count)
			content[row.FacilityID] = c
		}
	}
	return content, nil
}

// Knowledge Center

// activityScope is the visits in w.
func (db *DB) activityScope(ctx context.Context, w api.Window) *gorm.DB {
	tx := db.WithContext(ctx).Model(&models.OpenContentActivity{})
	if w.FacilityID != nil {
		tx = tx.Where("facility_id = ?", *w.FacilityID)
	}
	if w.Start != nil && w.End != nil {
		tx = tx.Where("request_ts >= ? AND request_ts < ?", *w.Start, *w.End)
	}
	return tx
}

// CountActivities is how many visits w has.
func (db *DB) CountActivities(ctx context.Context, w api.Window) (int64, error) {
	var total int64
	err := db.activityScope(ctx, w).Count(&total).Error
	return total, err
}

// CountActiveUsers is how many users visited in w.
func (db *DB) CountActiveUsers(ctx context.Context, w api.Window) (int64, error) {
	var unique int64
	err := db.activityScope(ctx, w).Distinct("user_id").Count(&unique).Error
	return unique, err
}

// GetVisitsPerUser is how many visits each user made in w.
func (db *DB) GetVisitsPerUser(ctx context.Context, w api.Window) ([]int64, error) {
	type userCount struct {
		UserID uint
		Cnt    int64
	}
	counts := make([]userCount, 0)
	if err := db.activityScope(ctx, w).
		Select("user_id, count(*) as cnt").
		Group("user_id").
		Find(&counts).Error; err != nil {
		return nil, err
	}
	visits := make([]int64, 0, len(counts))
	for _, row := range counts {
		visits = append(visits, row.Cnt)
	}
	return visits, nil
}

// GetAvgSessionMinutes is the average length of w's visits that ended after
// they started. The database averages them, so the answer never loads the
// visits themselves, which over all time and every facility is the whole
// table.
func (db *DB) GetAvgSessionMinutes(ctx context.Context, w api.Window) (float64, error) {
	minutes := "EXTRACT(EPOCH FROM (stop_ts - request_ts)) / 60"
	if db.Name() == "sqlite" {
		// The tests' SQLite has no EXTRACT or intervals.
		minutes = "(julianday(stop_ts) - julianday(request_ts)) * 1440"
	}
	var avg sql.NullFloat64
	if err := db.activityScope(ctx, w).
		Select("AVG(" + minutes + ")").
		Where("stop_ts > request_ts").
		Scan(&avg).Error; err != nil {
		return 0, err
	}
	return avg.Float64, nil
}

func (db *DB) GetLibraryViewsByCategory(ctx context.Context, w api.Window) ([]api.CategoryViews, error) {
	views := make([]api.CategoryViews, 0)
	tx := db.WithContext(ctx).Table("open_content_activities oca").
		Select("t.name as category, count(oca.id) as views").
		Joins("JOIN open_content_tags oct ON oct.content_id = oca.content_id AND oct.open_content_provider_id = oca.open_content_provider_id").
		Joins("JOIN tags t ON t.id = oct.tag_id")
	if w.FacilityID != nil {
		tx = tx.Where("oca.facility_id = ?", *w.FacilityID)
	}
	if w.Start != nil && w.End != nil {
		tx = tx.Where("oca.request_ts >= ? AND oca.request_ts < ?", *w.Start, *w.End)
	}
	if err := tx.Group("t.name").Order("views DESC, t.name ASC").Scan(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

// GetTopContentVisits is the most visited content in table (libraries or
// videos) in w, at most limit of them.
func (db *DB) GetTopContentVisits(ctx context.Context, table string, w api.Window, limit int) ([]models.ContentVisits, error) {
	alias := table[:1]
	visits := make([]models.ContentVisits, 0, limit)
	tx := db.WithContext(ctx).Table("open_content_activities oca").
		Select(alias + ".id as content_id, " + alias + ".title as title, count(oca.id) as visits").
		Joins("JOIN " + table + " " + alias + " ON " + alias + ".id = oca.content_id AND " + alias + ".open_content_provider_id = oca.open_content_provider_id AND " + alias + ".deleted_at IS NULL")
	if w.FacilityID != nil {
		tx = tx.Where("oca.facility_id = ?", *w.FacilityID)
	}
	if w.Start != nil && w.End != nil {
		tx = tx.Where("oca.request_ts >= ? AND oca.request_ts < ?", *w.Start, *w.End)
	}
	if err := tx.Group(alias + ".id, " + alias + ".title").
		Order("visits DESC, " + alias + ".title ASC").
		Limit(limit).
		Scan(&visits).Error; err != nil {
		return nil, err
	}
	return visits, nil
}

// GetContentVisits is how many visits each of ids in table (libraries or
// videos) had in w, which has a start and an end.
func (db *DB) GetContentVisits(ctx context.Context, table string, ids []uint, w api.Window) (map[uint]int64, error) {
	alias := table[:1]
	rows := make([]models.ContentVisits, 0, len(ids))
	tx := db.WithContext(ctx).Table("open_content_activities oca").
		Select(alias+".id as content_id, count(oca.id) as visits").
		Joins("JOIN "+table+" "+alias+" ON "+alias+".id = oca.content_id AND "+alias+".open_content_provider_id = oca.open_content_provider_id AND "+alias+".deleted_at IS NULL").
		Where(alias+".id IN ?", ids).
		Where("oca.request_ts >= ? AND oca.request_ts < ?", *w.Start, *w.End)
	if w.FacilityID != nil {
		tx = tx.Where("oca.facility_id = ?", *w.FacilityID)
	}
	if err := tx.Group(alias + ".id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	visits := make(map[uint]int64, len(rows))
	for _, row := range rows {
		visits[row.ContentID] = row.Visits
	}
	return visits, nil
}
