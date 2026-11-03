package database

import (
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
)

func (db *DB) CreateContentActivity(urlString string, activity *models.OpenContentActivity) {
	url := models.OpenContentUrl{}
	if db.Where("content_url = ?", urlString).First(&url).RowsAffected == 0 {
		url.ContentURL = urlString
		if err := db.Create(&url).Error; err != nil {
			log.Warn("unable to create content url for activity")
			return
		}
	}
	activity.OpenContentUrlID = url.ID
	if err := db.Create(&activity).Error; err != nil {
		log.Warn("unable to create content activity for url, ", urlString)
	}
}

func (db *DB) UpdateOpenContentActivityStopTS(activityID int64) {
	if err := db.Model(&models.OpenContentActivity{}).Where("id = ?", activityID).Update("stop_ts", time.Now()).Error; err != nil {
		log.Errorf("error updating open content activity: %v", err)
	}
}

func (db *DB) GetTopFacilityOpenContent(id int) ([]models.OpenContentItem, error) {
	var content []models.OpenContentItem
	if err := db.Raw("? UNION ? ORDER BY visits DESC LIMIT 5",
		db.Select("fvs.visibility_status, v.title, v.url, v.thumbnail_url, v.open_content_provider_id, v.id as content_id, 'video' as content_type, count(v.id) as visits").
			Table("open_content_activities oca").
			Joins("LEFT JOIN videos v ON v.id = oca.content_id AND v.open_content_provider_id = oca.open_content_provider_id").
			Joins(`LEFT JOIN facility_visibility_statuses fvs on fvs.open_content_provider_id = v.open_content_provider_id
				and fvs.content_id = v.id
				and fvs.facility_id = ?`, id).
			Where("oca.facility_id = ? and fvs.visibility_status = TRUE", id).
			Group("fvs.visibility_status, v.title, v.url, v.thumbnail_url, v.open_content_provider_id, v.id").
			Having("count(v.id) > 0"),
		db.Select("fvs.visibility_status, l.title, l.url, l.thumbnail_url, l.open_content_provider_id, l.id as content_id, 'library' as content_type, count(l.id) as visits").
			Table("open_content_activities oca").
			Joins("LEFT JOIN libraries l on l.id = oca.content_id AND l.open_content_provider_id = oca.open_content_provider_id and l.deleted_at is null").
			Joins(`LEFT JOIN facility_visibility_statuses fvs on fvs.open_content_provider_id = l.open_content_provider_id
				and fvs.content_id = l.id
				and fvs.facility_id = ?`, id).
			Where("oca.facility_id = ? and fvs.visibility_status = TRUE", id).
			Group("fvs.visibility_status, l.title, l.url, l.thumbnail_url, l.open_content_provider_id, l.id").
			Having("count(l.id) > 0"),
	).Find(&content).Error; err != nil {
		return nil, newGetRecordsDBError(err, "open_content_items")
	}
	return content, nil
}

func (db *DB) GetTopUserOpenContent(id int, args *models.QueryContext) ([]models.OpenContentItem, error) {
	var content []models.OpenContentItem
	if err := db.WithContext(args.Ctx).Raw("? UNION ? ORDER BY visits DESC LIMIT 5",
		db.Select("fvs.visibility_status, v.title, v.url, v.thumbnail_url, v.open_content_provider_id, v.id as content_id, 'video' as content_type, count(v.id) as visits").
			Table("open_content_activities oca").
			Joins("LEFT JOIN videos v ON v.id = oca.content_id AND v.open_content_provider_id = oca.open_content_provider_id ").
			Joins("LEFT JOIN facility_visibility_statuses fvs ON fvs.open_content_provider_id = v.open_content_provider_id AND fvs.content_id = v.id AND fvs.facility_id = ?", args.FacilityID).
			Where("oca.user_id = ? and fvs.visibility_status = TRUE", id).
			Group("fvs.visibility_status, v.title, v.url, v.thumbnail_url, v.open_content_provider_id, v.id").
			Having("count(v.id) > 0"),
		db.Select("fvs.visibility_status, l.title, l.url, l.thumbnail_url, l.open_content_provider_id, l.id as content_id, 'library' as content_type, count(l.id) as visits").
			Table("open_content_activities oca").
			Joins("LEFT JOIN libraries l on l.id = oca.content_id AND l.open_content_provider_id = oca.open_content_provider_id and l.deleted_at is null").
			Joins(`LEFT JOIN facility_visibility_statuses fvs on fvs.open_content_provider_id = l.open_content_provider_id
				and fvs.content_id = l.id
				and fvs.facility_id = ?`, args.FacilityID).
			Where("oca.user_id = ? and fvs.visibility_status = TRUE", id).
			Group("fvs.visibility_status, l.title, l.url, l.thumbnail_url, l.open_content_provider_id, l.id").
			Having("count(l.id) > 0"),
	).Find(&content).Error; err != nil {
		return nil, newGetRecordsDBError(err, "open_content_items")
	}
	return content, nil
}
