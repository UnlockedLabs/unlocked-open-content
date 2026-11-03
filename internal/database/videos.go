package database

import (
	"context"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"gorm.io/gorm/clause"
)

func (db *DB) GetVideoByID(id int, facilityId uint) (*models.Video, error) {
	var video models.Video
	query := db.Model(&models.Video{}).Preload("Attempts").
		Select("videos.*, fvs.visibility_status").
		Joins(`left outer join facility_visibility_statuses fvs on
			fvs.open_content_provider_id = videos.open_content_provider_id and
			fvs.content_id = videos.id and
			fvs.facility_id = ?`, facilityId)

	if err := query.First(&video, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "videos")
	}
	return &video, nil
}

// FindVideoProxy is the video the proxy serves. A non-admin only gets a
// video that is available and visible at their facility.
func (db *DB) FindVideoProxy(id string, facilityID uint, isAdmin bool) (*models.Video, error) {
	var video models.Video
	tx := db.Model(&models.Video{}).
		Select(`videos.*,
            (CASE WHEN fvs.visibility_status IS NULL THEN false ELSE fvs.visibility_status END) AS visibility_status`).
		Joins(`LEFT OUTER JOIN facility_visibility_statuses fvs
            ON fvs.open_content_provider_id = videos.open_content_provider_id
            AND fvs.content_id = videos.id
            AND fvs.facility_id = ?`, facilityID).
		Where("videos.id = ?", id)
	if isAdmin {
		tx = tx.First(&video)
	} else {
		tx = tx.First(&video, "fvs.visibility_status = true AND availability = 'available'")
	}
	if err := tx.Error; err != nil {
		return nil, err
	}
	return &video, nil
}

func (db *DB) CountVideos(ctx context.Context) (int64, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&models.Video{}).Count(&count).Error; err != nil {
		return 0, newGetRecordsDBError(err, "videos")
	}
	return count, nil
}

// GetSavedVideoExternalIDs is the external IDs in externalIDs that have a
// saved video.
func (db *DB) GetSavedVideoExternalIDs(ctx context.Context, externalIDs []string) ([]string, error) {
	var saved []string
	if err := db.WithContext(ctx).Model(&models.Video{}).Where("external_id IN ?", externalIDs).Pluck("external_id", &saved).Error; err != nil {
		return nil, newGetRecordsDBError(err, "videos")
	}
	return saved, nil
}

// GetVideosNotIn is the saved videos whose external ID isn't in externalIDs.
func (db *DB) GetVideosNotIn(ctx context.Context, externalIDs []string) ([]models.Video, error) {
	var videos []models.Video
	if err := db.WithContext(ctx).Model(&models.Video{}).Where("external_id NOT IN ?", externalIDs).Find(&videos).Error; err != nil {
		return nil, newGetRecordsDBError(err, "videos")
	}
	return videos, nil
}

// GetUnavailableVideos is the videos still processing or that failed.
func (db *DB) GetUnavailableVideos(ctx context.Context) ([]models.Video, error) {
	videos := make([]models.Video, 0)
	if err := db.WithContext(ctx).Model(&models.Video{}).Find(&videos, "availability <> 'available'").Error; err != nil {
		return nil, newGetRecordsDBError(err, "videos")
	}
	return videos, nil
}

// GetVideoWithAttempts is a video and its download attempts.
func (db *DB) GetVideoWithAttempts(ctx context.Context, id int) (*models.Video, error) {
	var video models.Video
	if err := db.WithContext(ctx).Preload("Attempts").First(&video, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "videos")
	}
	return &video, nil
}

func (db *DB) VideoExternalIDExists(ctx context.Context, externalID string) (bool, error) {
	return db.videoExists(ctx, "external_id = ?", externalID)
}

func (db *DB) VideoURLExists(ctx context.Context, url string) (bool, error) {
	return db.videoExists(ctx, "url = ?", url)
}

func (db *DB) videoExists(ctx context.Context, query string, arg string) (bool, error) {
	var exists bool
	if err := db.WithContext(ctx).Model(&models.Video{}).Select("count(*) > 0").Where(query, arg).Scan(&exists).Error; err != nil {
		return false, newGetRecordsDBError(err, "videos")
	}
	return exists, nil
}

func (db *DB) CreateVideo(ctx context.Context, video *models.Video) error {
	if err := db.WithContext(ctx).Create(video).Error; err != nil {
		return newCreateDBError(err, "videos")
	}
	return nil
}

func (db *DB) SaveVideo(ctx context.Context, video *models.Video) error {
	if err := db.WithContext(ctx).Save(video).Error; err != nil {
		return newUpdateDBError(err, "videos")
	}
	return nil
}

func (db *DB) CountVideoDownloadAttempts(ctx context.Context, videoID uint) (int64, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&models.VideoDownloadAttempt{}).Where("video_id = ?", videoID).Count(&count).Error; err != nil {
		return 0, newGetRecordsDBError(err, "video_download_attempts")
	}
	return count, nil
}

func (db *DB) CreateVideoDownloadAttempt(ctx context.Context, attempt *models.VideoDownloadAttempt) error {
	if err := db.WithContext(ctx).Create(attempt).Error; err != nil {
		return newCreateDBError(err, "video_download_attempts")
	}
	return nil
}

func (db *DB) GetAllVideos(args *models.QueryContext, visibility string) ([]models.VideoResponse, error) {
	var videos []models.VideoResponse
	tx := db.WithContext(args.Ctx).Model(&models.Video{}).Preload("Attempts").Select(`
	videos.*,
        CASE WHEN fvs.visibility_status is null then false
        else fvs.visibility_status
    end as visibility_status,
	`+visibleFacilityCountSubquery("videos")+`,
	EXISTS (
		SELECT 1
		FROM open_content_favorites f
		WHERE f.content_id = videos.id
		  AND f.open_content_provider_id = videos.open_content_provider_id
		  AND f.user_id = ?
	) AS is_favorited,
	EXISTS (
		SELECT 1
		FROM open_content_favorites ff
		WHERE ff.content_id = videos.id
		  AND ff.open_content_provider_id = videos.open_content_provider_id
		  AND ff.facility_id = ?
	) AS is_featured`, args.UserID, args.FacilityID).
		Joins(`left join facility_visibility_statuses fvs
        on fvs.open_content_provider_id = videos.open_content_provider_id
        and fvs.content_id = videos.id
        and fvs.facility_id = ?`, args.FacilityID)

	switch visibility {
	case "visible":
		tx = tx.Where("COALESCE(fvs.visibility_status, false) = ?", true)
	case "hidden":
		tx = tx.Where("COALESCE(fvs.visibility_status, false) = ?", false)
	}
	if args.Search != "" {
		args.Search = "%" + args.Search + "%"
		tx = tx.Where("LOWER(title) LIKE ? OR LOWER(channel_title) LIKE ?", args.Search, args.Search)
	}
	switch args.OrderBy {
	case "most_popular":
		tx = tx.Joins("LEFT JOIN open_content_favorites f ON f.content_id = videos.id AND f.open_content_provider_id = videos.open_content_provider_id").
			Group("videos.id, fvs.visibility_status").Order("COUNT(f.id) DESC")
	default:
		tx = tx.Order(args.OrderClause("videos.created_at desc"))
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "videos")
	}
	if err := tx.Offset(args.CalcOffset()).Limit(args.PerPage).Find(&videos).Error; err != nil {
		return nil, newGetRecordsDBError(err, "videos")
	}
	return videos, nil
}

func (db *DB) ToggleVideoVisibility(ctx context.Context, id int, facilityId uint) error {
	var video models.Video
	query := db.WithContext(ctx).Model(&models.Video{}).
		Select("videos.*, fvs.visibility_status").
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = videos.open_content_provider_id
			and fvs.content_id = videos.id
			and fvs.facility_id = ?`, facilityId)
	if err := query.Find(&video, "videos.id = ?", id).Error; err != nil {
		return newNotFoundDBError(err, "videos")
	}
	visibility := video.GetFacilityVisibilityStatus(facilityId)
	visibility.VisibilityStatus = !visibility.VisibilityStatus
	updateMap := map[string]any{
		"visibility_status": visibility.VisibilityStatus,
	}
	if c, ok := identity.FromContext(ctx); ok {
		updateMap["update_user_id"] = c.UserID
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "facility_id"}, {Name: "open_content_provider_id"}, {Name: "content_id"}},
		DoUpdates: clause.Assignments(updateMap),
	}).Create(&visibility).Error; err != nil {
		return newUpdateDBError(err, "video visibility")
	}

	return nil
}

func (db *DB) GetVideoFacilityVisibility(args *models.QueryContext, id int) ([]models.ContentFacilityVisibility, error) {
	var video models.Video
	if err := db.WithContext(args.Ctx).First(&video, "id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "videos")
	}
	return db.getContentFacilityVisibility(args, video.ID, video.OpenContentProviderID)
}

func (db *DB) DeleteVideo(id int) error {
	vid := models.Video{}
	vid.ID = uint(id)
	if err := db.Delete(&vid).Error; err != nil {
		return newCreateDBError(err, "videos")
	}
	return nil
}
