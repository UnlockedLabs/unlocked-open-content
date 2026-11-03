package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

func (db *DB) GetHelpfulLinks(args *models.QueryContext, onlyVisible bool) ([]models.HelpfulLinkResp, error) {
	links := make([]models.HelpfulLinkResp, 0, args.PerPage)

	subQuery := db.Table("open_content_favorites f").
		Select("1").
		Where(`f.content_id = helpful_links.id AND f.user_id = ?
			AND f.open_content_provider_id = helpful_links.open_content_provider_id`, args.UserID)
	featuredSubQuery := db.Table("open_content_favorites ff").
		Select("1").
		Where(`ff.content_id = helpful_links.id AND ff.facility_id = ?
			AND ff.open_content_provider_id = helpful_links.open_content_provider_id`, args.FacilityID)
	tx := db.Model(&models.HelpfulLink{}).
		Select(`helpful_links.*,
			COALESCE(fvs.visibility_status, false) AS visibility_status,
			`+visibleFacilityCountSubquery("helpful_links")+`,
			EXISTS(?) as is_favorited, EXISTS(?) as is_featured`, subQuery, featuredSubQuery).
		Joins(`LEFT JOIN facility_visibility_statuses fvs ON fvs.open_content_provider_id = helpful_links.open_content_provider_id
			AND fvs.content_id = helpful_links.id
			AND fvs.facility_id = ?`, args.FacilityID)

	if onlyVisible {
		tx = tx.Where("fvs.visibility_status = ?", true)
	}

	if args.Search != "" {
		tx.Where("LOWER(title) LIKE ?", args.SearchQuery())
	}
	switch strings.ToLower(args.OrderBy) {
	case "most_popular":
		tx = tx.Joins("LEFT JOIN open_content_favorites f ON f.content_id = helpful_links.id AND f.open_content_provider_id = helpful_links.open_content_provider_id").
			Group("helpful_links.id, fvs.visibility_status").Order("COUNT(f.id) DESC")
	default:
		tx = tx.Order(args.OrderClause("helpful_links.created_at desc"))
	}

	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "helpful_links")
	}
	if err := tx.Offset(args.CalcOffset()).Limit(args.PerPage).Find(&links).Error; err != nil {
		return nil, newGetRecordsDBError(err, "helpful_links")
	}
	return links, nil
}

func (db *DB) AddHelpfulLink(ctx context.Context, args *models.QueryContext, link *models.HelpfulLink) error {
	tx := db.WithContext(ctx)
	if tx.Where("url = ?", link.Url).First(&models.HelpfulLink{}).RowsAffected > 0 {
		return NewDBError(fmt.Errorf("link already exists"), "existing_helpful_link")
	}
	if err := tx.Create(link).Error; err != nil {
		return newCreateDBError(err, "helpful_links")
	}
	return db.UpsertFacilityVisibilityStatuses(args, []models.FacilityVisibilityStatus{{
		FacilityID:            args.FacilityID,
		OpenContentProviderID: link.OpenContentProviderID,
		ContentID:             link.ID,
		VisibilityStatus:      true,
	}}, true)
}

func (db *DB) DeleteLink(ctx context.Context, id uint) error {
	tx := db.WithContext(ctx)
	var link models.HelpfulLink
	if err := tx.Model(&models.HelpfulLink{}).Where("id = ?", id).First(&link).Error; err != nil {
		return newGetRecordsDBError(err, "helpful_links")
	}
	if err := tx.Unscoped().Delete(&link).Error; err != nil {
		return newDeleteDBError(err, "helpful_links")
	}
	if err := tx.Where("content_id = ? AND open_content_provider_id = ?", link.ID, link.OpenContentProviderID).
		Delete(&models.FacilityVisibilityStatus{}).Error; err != nil {
		return newDeleteDBError(err, "facility_visibility_statuses")
	}
	return nil
}

func (db *DB) EditLink(ctx context.Context, id uint, link models.HelpfulLink) error {
	if err := db.WithContext(ctx).Model(&models.HelpfulLink{}).Where("id = ?", id).Updates(link).Error; err != nil {
		return newUpdateDBError(err, "helpful_links")
	}
	return nil
}

func (db *DB) ToggleVisibilityStatus(ctx context.Context, args *models.QueryContext, id int) error {
	var link models.HelpfulLink
	if err := db.WithContext(ctx).Model(&models.HelpfulLink{}).
		Select(`helpful_links.*, COALESCE(fvs.visibility_status, false) AS visibility_status`).
		Joins(`LEFT JOIN facility_visibility_statuses fvs ON fvs.open_content_provider_id = helpful_links.open_content_provider_id
			AND fvs.content_id = helpful_links.id
			AND fvs.facility_id = ?`, args.FacilityID).
		Where("helpful_links.id = ?", id).First(&link).Error; err != nil {
		return newGetRecordsDBError(err, "helpful_links")
	}
	return db.UpsertFacilityVisibilityStatuses(args, []models.FacilityVisibilityStatus{{
		FacilityID:            args.FacilityID,
		OpenContentProviderID: link.OpenContentProviderID,
		ContentID:             link.ID,
		VisibilityStatus:      !link.VisibilityStatus,
	}}, !link.VisibilityStatus)
}

func (db *DB) GetHelpfulLinkFacilityVisibility(args *models.QueryContext, id int) ([]models.ContentFacilityVisibility, error) {
	var link models.HelpfulLink
	if err := db.WithContext(args.Ctx).First(&link, "id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "helpful_links")
	}
	return db.getContentFacilityVisibility(args, link.ID, link.OpenContentProviderID)
}

func (db *DB) GetLinkFromId(id uint) (*models.HelpfulLink, error) {
	var link models.HelpfulLink
	if err := db.Model(&models.HelpfulLink{}).Where("id = ?", id).First(&link).Error; err != nil {
		return nil, newGetRecordsDBError(err, "helpful_links")
	}
	return &link, nil
}
