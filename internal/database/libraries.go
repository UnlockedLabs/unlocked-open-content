package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

// Retrieves either a paginated list of libraries or all libraries based upon the given parameters.
// visibility - can either be featured, visible, hidden, or all
func (db *DB) GetAllLibraries(args *models.QueryContext, visibility string) ([]models.LibraryResponse, error) {
	var (
		criteria string
		id       uint
	)
	libraries := make([]models.LibraryResponse, 0, args.PerPage)
	//added the below to display featuring flags for all admins per facility
	selectIsFavoriteOrIsFeatured := `
		libraries.*,
		CASE WHEN fvs.visibility_status IS NULL THEN false
			ELSE fvs.visibility_status
		END AS visibility_status,
		` + visibleFacilityCountSubquery("libraries") + `,
		EXISTS (
			SELECT 1
			FROM open_content_favorites ff
			WHERE ff.content_id = libraries.id
				AND ff.open_content_provider_id = libraries.open_content_provider_id
				AND ff.open_content_url_id IS NULL
				AND ff.facility_id = ?
		) AS is_featured,
		EXISTS (
			SELECT 1
			FROM open_content_favorites f
			WHERE f.content_id = libraries.id
				AND f.open_content_provider_id = libraries.open_content_provider_id
            AND f.open_content_url_id IS NULL
			  AND %s) AS is_favorited`

	if args.IsAdmin {
		criteria, id = "f.facility_id = ?", args.FacilityID
	} else {
		criteria, id = "f.user_id = ?", args.UserID
	}
	tx := db.WithContext(args.Ctx).Model(&models.Library{}).Preload("OpenContentProvider").Select(fmt.Sprintf(selectIsFavoriteOrIsFeatured, criteria), args.FacilityID, id)
	tx = tx.Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
		and fvs.content_id = libraries.id
		and fvs.facility_id = ?`, args.FacilityID)
	visibility = strings.ToLower(visibility)

	isFeatured := false
	switch visibility {
	case "featured":
		// Join with open_content_favorites, ensuring facility_id is not null (admin-specific)
		tx = tx.Joins(`JOIN open_content_favorites f
			ON f.content_id = libraries.id
			AND f.open_content_provider_id = libraries.open_content_provider_id
			AND f.facility_id IS NOT NULL`).Where("f.facility_id = ? AND fvs.visibility_status = true", args.FacilityID)
		isFeatured = true
	case "visible":
		tx = tx.Where("fvs.visibility_status = true")
	case "hidden":
		tx = tx.Where("(fvs.visibility_status = false OR fvs.visibility_status IS NULL)")
	case "all":
	default:
		tx = tx.Where("fvs.visibility_status = true")
	}
	var search string
	if args.Search != "" {
		search = args.SearchQuery()
		tx = tx.Where("LOWER(libraries.title) LIKE ? OR LOWER(libraries.description) LIKE ?", search, search)
	}
	if len(args.Tags) > 0 {
		tx = tx.Joins("JOIN open_content_tags t ON t.content_id = libraries.id").Where("t.tag_id IN (?) AND t.open_content_provider_id = libraries.open_content_provider_id", args.Tags)
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "libraries")
	}

	switch args.OrderBy {
	case "most_popular":
		tx = tx.Select(`
			libraries.*,
			CASE WHEN fvs.visibility_status IS NULL THEN false
				ELSE fvs.visibility_status
			END AS visibility_status,
			COUNT(f.id) AS favorite_count,
			EXISTS (
				SELECT 1
				FROM open_content_favorites f
				WHERE f.content_id = libraries.id
					AND f.open_content_provider_id = libraries.open_content_provider_id
					AND f.user_id = ?
			) AS is_favorited`, args.UserID)
		if !isFeatured {
			tx = tx.Joins(`JOIN open_content_favorites f
				ON f.content_id = libraries.id
				AND f.open_content_provider_id = libraries.open_content_provider_id
				AND f.facility_id IS NULL`)
		}
		tx = tx.Group("libraries.id, fvs.visibility_status").Order("favorite_count DESC")
	default:
		tx = tx.Order(args.OrderClause("libraries.created_at desc"))
	}
	if !args.All {
		tx = tx.Limit(args.PerPage).Offset(args.CalcOffset())
	}
	if err := tx.Find(&libraries).Error; err != nil {
		return nil, newGetRecordsDBError(err, "libraries")
	}
	if len(libraries) > 0 {
		libraryIDs := make([]uint, len(libraries))
		for i, lib := range libraries {
			libraryIDs[i] = lib.ID
		}
		type tagRow struct {
			ContentID uint
			TagName   string
		}
		var rows []tagRow
		if err := db.WithContext(args.Ctx).Raw(
			`SELECT oct.content_id, t.name AS tag_name
			 FROM open_content_tags oct
			 JOIN tags t ON t.id = oct.tag_id
			 WHERE oct.content_id IN ?`, libraryIDs,
		).Scan(&rows).Error; err != nil {
			log.Warnf("failed to fetch tags for libraries: %v", err)
		} else {
			tagMap := make(map[uint][]string)
			for _, r := range rows {
				tagMap[r.ContentID] = append(tagMap[r.ContentID], r.TagName)
			}
			for i := range libraries {
				if tags, ok := tagMap[libraries[i].ID]; ok {
					libraries[i].Tags = tags
				}
			}
		}
	}
	return libraries, nil
}

func (db *DB) GetLibraryByID(id int) (*models.Library, error) {
	var library models.Library
	if err := db.Preload("OpenContentProvider").First(&library, "id = ?", id).Error; err != nil {
		log.Errorln("Unable to find library with that ID")
		return nil, newNotFoundDBError(err, "libraries")
	}
	return &library, nil
}

func (db *DB) OpenContentTitleSearch(args *models.QueryContext) ([]models.OpenContentItem, error) {
	var items []models.OpenContentItem
	searchQuery := `SELECT
			'video' AS content_type,
			v.id as content_id,
			v.title,
			CONCAT('/viewer/videos/', v.id) as url,
			v.thumbnail_url,
			v.description,
			(CASE WHEN fvs.visibility_status is null then false else fvs.visibility_status END) as visibility_status,
			v.open_content_provider_id,
			NULL AS provider_name,
			v.channel_title,
			v.created_at
			FROM videos v
        LEFT OUTER JOIN facility_visibility_statuses fvs
            on fvs.open_content_provider_id = v.open_content_provider_id
            and fvs.content_id = v.id
            and fvs.facility_id = ?
        WHERE fvs.visibility_status = true
				and to_tsvector('english', concat_ws(' ', v.title, v.description, v.channel_title)) @@ plainto_tsquery('english', ?)
		   UNION ALL
		SELECT
			'library' AS content_type,
			l.id as content_id,
			l.title,
			CONCAT('/api/proxy/libraries/', l.id) as url,
			l.thumbnail_url,
			l.description,
			CASE WHEN fvs.visibility_status IS NULL THEN false
				ELSE fvs.visibility_status
			END AS visibility_status,
			l.open_content_provider_id,
			'kiwix' AS provider_name,
			NULL AS channel_title,
			l.created_at
		FROM libraries l
		left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = l.open_content_provider_id
			and fvs.content_id = l.id
			and fvs.facility_id = ?
		WHERE fvs.visibility_status = true
			and to_tsvector('english', concat_ws(' ', l.title, l.description)) @@ plainto_tsquery('english', ?)`

	tx := db.WithContext(args.Ctx).Raw(searchQuery, args.FacilityID, args.Search, args.FacilityID, args.Search)
	if err := tx.Scan(&items).Error; err != nil {
		log.Errorln("Unable to perform content search")
		return nil, newNotFoundDBError(err, "content search")
	}
	return items, nil
}

func (db *DB) GetLibrariesByIDsAndLang(ids []int, language string) ([]models.Library, error) {
	var libraries []models.Library
	tx := db.Preload("OpenContentProvider").Where("id in ?", ids)
	if len(ids) > 1 {
		tx.Where("language = ?", language)
	}
	if err := tx.Find(&libraries).Error; err != nil {
		log.Errorln("unable to find libraries with these IDs with language as ", language)
		return nil, newNotFoundDBError(err, "libraries")
	}
	return libraries, nil
}

// Retrieves all libraries by langauge using the given paramenters. A way to find out the different language values is to execute
// an SQL query against the libraries table to see the different language values (column name is language).
// language - the language of the library to retrieve
func (db *DB) GetAllLibrariesByLang(args *models.QueryContext, language string) ([]models.Library, error) {
	var libraries []models.Library
	tx := db.WithContext(args.Ctx).Model(&models.Library{}).Preload("OpenContentProvider").Select("libraries.*").
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
			and fvs.content_id = libraries.id
			and fvs.facility_id = ?`, args.FacilityID).
		Where("fvs.visibility_status = true and libraries.language = ?", language)
	if err := tx.Find(&libraries).Error; err != nil {
		log.Errorln("unable to find libraries that are visible with language as ", language)
		return nil, newNotFoundDBError(err, "libraries")
	}
	return libraries, nil
}

func (db *DB) ToggleVisibilityAndRetrieveLibrary(id int, args *models.QueryContext) (*models.Library, error) {
	var library models.Library
	query := db.WithContext(args.Ctx).Model(&models.Library{}).Preload("OpenContentProvider").
		Select("libraries.*, fvs.visibility_status").
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
			and fvs.content_id = libraries.id
			and fvs.facility_id = ?`, args.FacilityID)
	if err := query.Find(&library, "id = ?", id).Error; err != nil {
		log.Errorln("Unable to find library with that ID")
		return nil, newNotFoundDBError(err, "libraries")
	}
	visibility := library.GetFacilityVisibilityStatus(args.FacilityID)
	visibility.VisibilityStatus = !visibility.VisibilityStatus

	updateMap := map[string]any{
		"visibility_status": visibility.VisibilityStatus,
	}
	if args.UserID != 0 {
		updateMap["update_user_id"] = args.UserID
	}
	if err := db.WithContext(args.Ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "facility_id"}, {Name: "open_content_provider_id"}, {Name: "content_id"}},
		DoUpdates: clause.Assignments(updateMap),
	}).Create(&visibility).Error; err != nil {
		return nil, newUpdateDBError(err, "libraries")
	}
	library.VisibilityStatus = visibility.VisibilityStatus
	return &library, nil
}

func (db *DB) GetLibraryFacilityVisibility(args *models.QueryContext, id int) ([]models.ContentFacilityVisibility, error) {
	var library models.Library
	if err := db.WithContext(args.Ctx).First(&library, "id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "libraries")
	}
	return db.getContentFacilityVisibility(args, library.ID, library.OpenContentProviderID)
}

// FindLibraryProxy is the library the proxy forwards to, with its visibility
// at facilityID.
func (db *DB) FindLibraryProxy(id string, facilityID uint) (*models.Library, bool) {
	var library models.Library
	query := db.Model(&models.Library{}).Preload("OpenContentProvider").
		Select("libraries.*, fvs.visibility_status").
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
					and fvs.content_id = libraries.id
					and fvs.facility_id = ?`, facilityID)
	if query.First(&library, "id = ?", id).RowsAffected == 0 {
		return nil, false
	}
	return &library, true
}

func (db *DB) GetTags() ([]models.Tag, error) {
	var tags []models.Tag
	if err := db.Model(&models.Tag{}).Find(&tags).Error; err != nil {
		return nil, newNotFoundDBError(err, "tags")
	}
	return tags, nil
}

// UpsertLibrary saves lib, or updates the library with lib's external ID.
func (db *DB) UpsertLibrary(ctx context.Context, lib *models.Library) error {
	if err := db.WithContext(ctx).
		Where(&models.Library{ExternalID: lib.ExternalID}).
		Assign(models.Library{
			Url:          lib.Url,
			Title:        lib.Title,
			Description:  lib.Description,
			Language:     lib.Language,
			ThumbnailUrl: lib.ThumbnailUrl,
		}).
		FirstOrCreate(lib).Error; err != nil {
		return newUpdateDBError(err, "libraries")
	}
	return nil
}

// DeleteLibrariesNotIn deletes the provider's libraries whose external ID
// isn't in externalIDs, and returns how many it deleted.
func (db *DB) DeleteLibrariesNotIn(ctx context.Context, providerID uint, externalIDs []string) (int64, error) {
	tx := db.WithContext(ctx).Where("open_content_provider_id = ?", providerID).Delete(&models.Library{}, "external_id NOT IN (?)", externalIDs)
	if tx.Error != nil {
		return 0, newDeleteDBError(tx.Error, "libraries")
	}
	return tx.RowsAffected, nil
}

func (db *DB) GetTopFacilityLibraries(args *models.QueryContext, id int, perPage int, start, end *time.Time) ([]models.OpenContentItem, error) {
	libraries := make([]models.OpenContentItem, 0, perPage)
	tx := db.WithContext(args.Ctx).Table("open_content_activities oca").
		Select("l.title, l.url, l.thumbnail_url as thumbnail_url, l.open_content_provider_id, l.id as content_id, 'library' as type, count(l.id) as visits").
		Joins("JOIN libraries l on l.id = oca.content_id AND l.open_content_provider_id = oca.open_content_provider_id and l.deleted_at is null").
		Where("oca.facility_id = ?", id)
	if start != nil && end != nil {
		tx = tx.Where("oca.request_ts >= ? AND oca.request_ts < ?", *start, *end)
	}
	if err := tx.
		Group("l.title, l.url, l.thumbnail_url, l.open_content_provider_id, l.id").
		Order("visits DESC").
		Limit(perPage).
		Find(&libraries).
		Error; err != nil {
		return nil, newGetRecordsDBError(err, "libraries")
	}
	return libraries, nil
}
