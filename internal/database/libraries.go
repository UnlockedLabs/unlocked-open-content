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
		tx = tx.Order(args.OrderClause("libraries"))
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

// textMatch is a WHERE clause matching the search against the text in cols,
// and its argument. Postgres uses English full-text search, as the search
// always has; other databases (the SQLite tests) use a substring match.
func (db *DB) textMatch(search string, cols ...string) (string, any) {
	parts := make([]string, len(cols))
	for i, col := range cols {
		parts[i] = "COALESCE(" + col + ", '')"
	}
	text := strings.Join(parts, " || ' ' || ")
	if db.Name() == "postgres" {
		return "to_tsvector('english', " + text + ") @@ plainto_tsquery('english', ?)", search
	}
	return "LOWER(" + text + ") LIKE ?", "%" + strings.ToLower(search) + "%"
}

// OpenContentTitleSearch finds the libraries, videos and helpful links whose
// title or description matches args.Search and that are visible at
// args.FacilityID. scope picks which kinds to search; args.Tags (category ids)
// keeps only libraries in those categories and drops videos and links, which
// have no categories. Videos match on title and description, not channel. The
// result is ordered libraries, videos, links, each by title.
func (db *DB) OpenContentTitleSearch(args *models.QueryContext, scope models.SearchScope) ([]models.OpenContentItem, error) {
	items := []models.OpenContentItem{}
	visible := func(alias string) string {
		return `LEFT OUTER JOIN facility_visibility_statuses fvs
			ON fvs.open_content_provider_id = ` + alias + `.open_content_provider_id
			AND fvs.content_id = ` + alias + `.id
			AND fvs.facility_id = ?`
	}
	var (
		selects []string
		params  []any
	)
	if scope.Videos && len(args.Tags) == 0 {
		match, arg := db.textMatch(args.Search, "v.title", "v.description")
		selects = append(selects, `SELECT
			'video' AS content_type, v.id AS content_id, v.title,
			'/viewer/videos/' || v.id AS url,
			v.thumbnail_url, v.description, fvs.visibility_status AS visibility_status,
			v.open_content_provider_id, NULL AS provider_name, v.channel_title
		FROM videos v `+visible("v")+`
		WHERE v.deleted_at IS NULL AND fvs.visibility_status = true AND `+match)
		params = append(params, args.FacilityID, arg)
	}
	if scope.Libraries {
		match, arg := db.textMatch(args.Search, "l.title", "l.description")
		where := `l.deleted_at IS NULL AND fvs.visibility_status = true AND ` + match
		libParams := []any{args.FacilityID, arg}
		if len(args.Tags) > 0 {
			where += ` AND EXISTS (SELECT 1 FROM open_content_tags t WHERE t.content_id = l.id
				AND t.open_content_provider_id = l.open_content_provider_id AND t.tag_id IN ?)`
			libParams = append(libParams, args.Tags)
		}
		selects = append(selects, `SELECT
			'library' AS content_type, l.id AS content_id, l.title,
			'/api/proxy/libraries/' || l.id AS url,
			l.thumbnail_url, l.description, fvs.visibility_status AS visibility_status,
			l.open_content_provider_id, 'kiwix' AS provider_name, NULL AS channel_title
		FROM libraries l `+visible("l")+`
		WHERE `+where)
		params = append(params, libParams...)
	}
	if scope.Links && len(args.Tags) == 0 {
		match, arg := db.textMatch(args.Search, "hl.title", "hl.description")
		selects = append(selects, `SELECT
			'link' AS content_type, hl.id AS content_id, hl.title, hl.url,
			hl.thumbnail_url, hl.description, fvs.visibility_status AS visibility_status,
			hl.open_content_provider_id, NULL AS provider_name, NULL AS channel_title
		FROM helpful_links hl `+visible("hl")+`
		WHERE hl.deleted_at IS NULL AND fvs.visibility_status = true AND `+match)
		params = append(params, args.FacilityID, arg)
	}
	if len(selects) == 0 || args.Search == "" {
		return items, nil
	}
	// one stable order: libraries, then videos, then helpful links, each by title
	query := `SELECT * FROM (` + strings.Join(selects, " UNION ALL ") + `) AS matches
		ORDER BY CASE content_type WHEN 'library' THEN 0 WHEN 'video' THEN 1 ELSE 2 END,
		LOWER(title), content_id`
	tx := db.WithContext(args.Ctx).Raw(query, params...)
	if err := tx.Scan(&items).Error; err != nil {
		log.Errorln("Unable to perform content search")
		return nil, newNotFoundDBError(err, "content search")
	}
	return items, nil
}

// GetLibrariesByIDsAndLang finds the libraries with these ids. With onlyVisible
// set (residents), a library counts only when it is visible at args.FacilityID,
// so a search by library_id can't read a library hidden there. Admins may
// search a hidden library, as they may open it.
func (db *DB) GetLibrariesByIDsAndLang(args *models.QueryContext, ids []int, language string, onlyVisible bool) ([]models.Library, error) {
	var libraries []models.Library
	tx := db.WithContext(args.Ctx).Model(&models.Library{}).Preload("OpenContentProvider").Select("libraries.*").
		Where("libraries.id in ?", ids)
	if onlyVisible {
		tx = tx.Joins(`join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
			and fvs.content_id = libraries.id
			and fvs.facility_id = ?`, args.FacilityID).
			Where("fvs.visibility_status = true")
	}
	if len(ids) > 1 {
		tx = tx.Where("libraries.language = ?", language)
	}
	if len(args.Tags) > 0 {
		tx = tx.Where(`EXISTS (SELECT 1 FROM open_content_tags t WHERE t.content_id = libraries.id
			AND t.open_content_provider_id = libraries.open_content_provider_id AND t.tag_id IN ?)`, args.Tags)
	}
	if err := tx.Find(&libraries).Error; err != nil {
		log.Errorln("unable to find libraries with these IDs with language as ", language)
		return nil, newNotFoundDBError(err, "libraries")
	}
	return libraries, nil
}

// Retrieves all libraries by langauge using the given paramenters. args.Tags, when set, keeps only libraries
// in those categories. A way to find out the different language values is to execute
// an SQL query against the libraries table to see the different language values (column name is language).
// language - the language of the library to retrieve
func (db *DB) GetAllLibrariesByLang(args *models.QueryContext, language string) ([]models.Library, error) {
	var libraries []models.Library
	tx := db.WithContext(args.Ctx).Model(&models.Library{}).Preload("OpenContentProvider").Select("libraries.*").
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = libraries.open_content_provider_id
			and fvs.content_id = libraries.id
			and fvs.facility_id = ?`, args.FacilityID).
		Where("fvs.visibility_status = true and libraries.language = ?", language)
	if len(args.Tags) > 0 {
		tx = tx.Where(`EXISTS (SELECT 1 FROM open_content_tags t WHERE t.content_id = libraries.id
			AND t.open_content_provider_id = libraries.open_content_provider_id AND t.tag_id IN ?)`, args.Tags)
	}
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
