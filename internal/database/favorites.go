package database

import (
	"fmt"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	log "github.com/sirupsen/logrus"
)

func (db *DB) FavoriteOpenContent(contentID int, ocpID uint, userID uint, facilityID *uint) (bool, error) {
	fav := models.OpenContentFavorite{
		ContentID:             uint(contentID),
		OpenContentProviderID: ocpID,
		UserID:                userID,
		FacilityID:            facilityID,
	}
	//added user id to query below to isolate favorite by user when facility id is passed in
	tx := db.Where("content_id = ? AND open_content_provider_id = ?", contentID, ocpID).
		Where("open_content_url_id IS NULL OR name IS NULL OR name = ''")
	if facilityID != nil {
		tx = tx.Where("facility_id = ?", facilityID)
	} else {
		tx = tx.Where("user_id = ?", userID)
	}
	if tx.First(&models.OpenContentFavorite{}).RowsAffected > 0 {
		delTx := db.Where("content_id = ? AND open_content_provider_id = ?  ", contentID, ocpID).
			Where("open_content_url_id IS NULL OR name IS NULL OR name = ''")
		if facilityID != nil {
			delTx = delTx.Where("facility_id = ?", facilityID)
		} else {
			delTx = delTx.Where("user_id = ?", userID)
		}
		if err := delTx.Delete(&fav).Error; err != nil {
			return false, newDeleteDBError(err, "open_content_favorites")
		}
		return false, nil
	} else {
		if err := db.Create(&fav).Error; err != nil {
			return false, newCreateDBError(err, "open_content_favorites")
		}
		return true, nil
	}
}

// IsBookmarked reports whether the user has bookmarked the page an activity
// visited.
func (db *DB) IsBookmarked(activity *models.OpenContentActivity) bool {
	var bookmark models.OpenContentFavorite
	return db.Model(&models.OpenContentFavorite{}).Where("user_id = ? AND content_id = ? AND open_content_url_id = ?", activity.UserID, activity.ContentID, activity.OpenContentUrlID).First(&bookmark).RowsAffected > 0
}

// featureBranchGuard returns a SQL fragment that empties one branch of the
// favorites UNION when the caller's facility has that content type's sub-feature
// disabled. It is a constant, never user input, so it binds no arguments and
// leaves the surrounding query's positional argument list untouched.
func featureBranchGuard(args *models.QueryContext, feature features.Access) string {
	if args.HasFeature(feature) {
		return ""
	}
	return " AND FALSE"
}

// This method will at most ever return the most recent 30 favorites (10 Libraries, 10 Videos, 10 Helpful Links)
func (db *DB) GetUserFavoriteGroupings(args *models.QueryContext) ([]models.OpenContentItem, error) {
	favorites := make([]models.OpenContentItem, 0, 30)
	favoritesQuery := fmt.Sprintf(`WITH ordered_libraries AS (
		SELECT
			'library' AS content_type,
			f.content_id,
            COALESCE(NULLIF(f.name, ''), lib.title) as title,
            COALESCE(ocu.content_url, lib.url) AS url,
			lib.thumbnail_url,
			ocp.description,
			fvs.visibility_status,
			lib.open_content_provider_id,
			ocp.title AS provider_name,
			NULL AS channel_title,
			f.created_at,
			ROW_NUMBER() OVER (ORDER BY f.created_at DESC) AS row_num
		FROM open_content_favorites f
		JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
			AND ocp.currently_enabled = TRUE
			AND ocp.deleted_at IS NULL
		JOIN libraries lib ON lib.open_content_provider_id = ocp.id
			AND lib.id = f.content_id
		left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = lib.open_content_provider_id
				and fvs.content_id = lib.id
				and fvs.facility_id = ?
        LEFT JOIN open_content_urls ocu ON f.open_content_url_id = ocu.id
		WHERE f.user_id = ?
	),
	ordered_videos AS (
		SELECT
			'video' AS content_type,
			f.content_id,
			videos.title,
			videos.url,
			videos.thumbnail_url,
			videos.description,
    fvs.visibility_status,
			videos.open_content_provider_id,
			NULL AS provider_name,
			videos.channel_title,
			f.created_at,
			ROW_NUMBER() OVER (ORDER BY f.created_at DESC) AS row_num
		FROM open_content_favorites f
		JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
			AND ocp.currently_enabled = TRUE
			AND ocp.deleted_at IS NULL
		JOIN videos ON videos.open_content_provider_id = ocp.id
			AND videos.id = f.content_id
		left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = videos.open_content_provider_id
            and fvs.content_id = videos.id
            and fvs.facility_id = ?
		WHERE f.user_id = ?
			AND f.content_id IN (SELECT id FROM videos where availability = 'available')
			%s
	),
	ordered_helpful_links AS (
		SELECT
			'helpful_link' AS content_type,
			f.content_id AS content_id,
			hl.title,
			hl.url,
			hl.thumbnail_url,
			hl.description,
			COALESCE(fvs.visibility_status, false) AS visibility_status,
			hl.open_content_provider_id AS open_content_provider_id,
			NULL AS provider_name,
			NULL AS channel_title,
			f.created_at,
			ROW_NUMBER() OVER (ORDER BY f.created_at DESC) AS row_num
		FROM open_content_favorites f
		JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
				AND ocp.currently_enabled = TRUE
				AND ocp.deleted_at IS NULL
		JOIN helpful_links hl ON hl.open_content_provider_id = ocp.id
				AND hl.id = f.content_id
		left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = hl.open_content_provider_id
				and fvs.content_id = hl.id
				and fvs.facility_id = ?
		WHERE f.user_id = ?
			%s
	)
	SELECT
			content_type,
			content_id,
			title,
			url,
			thumbnail_url,
			description,
			visibility_status,
			open_content_provider_id,
			provider_name,
			channel_title,
			created_at
	FROM (
		SELECT * FROM ordered_libraries WHERE row_num <= 10
		UNION ALL
		SELECT * FROM ordered_videos WHERE row_num <= 10
		UNION ALL
		SELECT * FROM ordered_helpful_links WHERE row_num <= 10
	)`,
		featureBranchGuard(args, features.UploadVideo),
		featureBranchGuard(args, features.HelpfulLinks))
	if err := db.WithContext(args.Ctx).Raw(favoritesQuery, args.FacilityID, args.UserID, args.FacilityID, args.UserID, args.FacilityID, args.UserID).Scan(&favorites).Error; err != nil {
		return nil, err
	}
	return favorites, nil
}

func (db *DB) GetUserFavorites(args *models.QueryContext) ([]models.OpenContentItem, error) {
	var libSearchCond, videoSearchCond, hlSearchCond string
	var searchTerm string

	if args.Search != "" {
		searchTerm = args.SearchQuery()
		libSearchCond = "AND LOWER(lib.title) LIKE ?"
		videoSearchCond = "AND LOWER(videos.title) LIKE ?"
		hlSearchCond = "AND LOWER(hl.title) LIKE ?"
	}

	// A favorited video or helpful link must not come back when its sub-feature
	// is disabled for this facility — the route can only gate on the parent
	// (open_content), which has to stay on for libraries. Appended as a literal
	// rather than by dropping the UNION branch: the query binds its arguments
	// positionally, so changing the branch count would mean re-deriving the
	// whole arg list.
	videoSearchCond += featureBranchGuard(args, features.UploadVideo)
	hlSearchCond += featureBranchGuard(args, features.HelpfulLinks)

	countArgs := []any{args.UserID}
	if args.Search != "" {
		countArgs = append(countArgs, searchTerm)
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM (
		SELECT fav.content_id
		FROM open_content_favorites fav
		JOIN libraries lib ON lib.id = fav.content_id
		JOIN open_content_providers ocp ON ocp.id = lib.open_content_provider_id
			AND ocp.currently_enabled = true
			AND ocp.deleted_at IS NULL
		WHERE fav.user_id = ? %s
	) AS total_favorites`, libSearchCond)

	if err := db.WithContext(args.Ctx).Raw(countQuery, countArgs...).Scan(&args.Total).Error; err != nil {
		return nil, err
	}

	queryArgs := make([]any, 0, 8)

	// Libraries
	queryArgs = append(queryArgs, args.FacilityID, args.UserID)
	if args.Search != "" {
		queryArgs = append(queryArgs, searchTerm)
	}
	// Videos
	queryArgs = append(queryArgs, args.FacilityID, args.UserID)
	if args.Search != "" {
		queryArgs = append(queryArgs, searchTerm)
	}
	// Helpful links
	queryArgs = append(queryArgs, args.FacilityID, args.UserID)
	if args.Search != "" {
		queryArgs = append(queryArgs, searchTerm)
	}
	queryArgs = append(queryArgs, args.PerPage, args.CalcOffset())

	favoritesQuery := fmt.Sprintf(`SELECT
        content_type,
        content_id,
        title,
        url,
        thumbnail_url,
        description,
        visibility_status,
        open_content_provider_id,
        provider_name,
        channel_title,
        created_at
    FROM (
        -- Libraries block:
        SELECT
            'library' AS content_type,
            f.content_id,
            COALESCE(NULLIF(f.name, ''), lib.title) as title,
            COALESCE(ocu.content_url, lib.url) AS url,
            lib.thumbnail_url,
            ocp.description,
			fvs.visibility_status,
            lib.open_content_provider_id,
            ocp.title AS provider_name,
            NULL AS channel_title,
            f.created_at
        FROM open_content_favorites f
        JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
            AND ocp.currently_enabled = TRUE
            AND ocp.deleted_at IS NULL
        JOIN libraries lib ON lib.open_content_provider_id = ocp.id
            AND lib.id = f.content_id
		left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = lib.open_content_provider_id
				and fvs.content_id = lib.id
				and fvs.facility_id = ?
        LEFT JOIN open_content_urls ocu ON f.open_content_url_id = ocu.id
        WHERE f.user_id = ? %s

        UNION ALL

        -- Videos block:
        SELECT
            'video' AS content_type,
            f.content_id,
            videos.title,
            videos.url,
            videos.thumbnail_url,
            videos.description,
        fvs.visibility_status,
            videos.open_content_provider_id,
            NULL AS provider_name,
            videos.channel_title,
            f.created_at
        FROM open_content_favorites f
        JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
            AND ocp.currently_enabled = TRUE
            AND ocp.deleted_at IS NULL
        JOIN videos ON videos.open_content_provider_id = ocp.id
            AND videos.id = f.content_id
        left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = videos.open_content_provider_id
				and fvs.content_id = videos.id
				and fvs.facility_id = ?
        WHERE f.user_id = ? %s

        UNION ALL

        SELECT
            'helpful_link' AS content_type,
            f.content_id AS content_id,
            hl.title,
            hl.url,
            hl.thumbnail_url,
            hl.description,
            COALESCE(fvs.visibility_status, false) AS visibility_status,
            hl.open_content_provider_id AS open_content_provider_id,
            NULL AS provider_name,
            NULL AS channel_title,
            f.created_at
        FROM open_content_favorites f
        JOIN open_content_providers ocp ON ocp.id = f.open_content_provider_id
            AND ocp.currently_enabled = TRUE
            AND ocp.deleted_at IS NULL
        JOIN helpful_links hl ON hl.open_content_provider_id = ocp.id
            AND hl.id = f.content_id
        left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = hl.open_content_provider_id
            and fvs.content_id = hl.id
            and fvs.facility_id = ?
        WHERE f.user_id = ? %s
    ) AS all_favorites
    ORDER BY %s
	LIMIT ? OFFSET ?`, libSearchCond, videoSearchCond, hlSearchCond, args.OrderClause("all_favorites.created_at desc"))

	var favorites []models.OpenContentItem
	if err := db.WithContext(args.Ctx).Raw(favoritesQuery, queryArgs...).Scan(&favorites).Error; err != nil {
		return nil, err
	}

	return favorites, nil
}

func (db *DB) BookmarkOpenContent(params *models.OpenContentParams) error {
	if params.Name != "" {
		var activity models.OpenContentActivity
		if err := db.Model(&models.OpenContentActivity{}).
			Where("user_id = ? AND content_id = ? AND open_content_provider_id = ?",
				params.UserID, params.ContentID, params.OpenContentProviderID).
			Order("request_ts DESC").
			First(&activity).Error; err != nil {
			log.Infof("activity %v", activity)
			return newNotFoundDBError(err, "open_content_activities")
		}
		newFav := models.OpenContentFavorite{
			UserID:                params.UserID,
			ContentID:             params.ContentID,
			OpenContentProviderID: params.OpenContentProviderID,
			Name:                  models.NilIfBlank(params.Name),
			OpenContentUrlID:      &activity.OpenContentUrlID,
		}
		if err := db.Create(&newFav).Error; err != nil {
			return newNotFoundDBError(err, "open_content_favorites")
		}
	} else if params.ContentURL != "" {
		var url models.OpenContentUrl
		if err := db.Model(&models.OpenContentUrl{}).
			Where("content_url = ?", params.ContentURL).
			First(&url).Error; err != nil {
			log.Infof("No matching URL found for %s", params.ContentURL)
			return newNotFoundDBError(err, "open_content_urls")
		}
		if err := db.Where("user_id = ? AND content_id = ? AND open_content_url_id = ? AND open_content_provider_id = ?",
			params.UserID, params.ContentID, &url.ID, params.OpenContentProviderID).
			Delete(&models.OpenContentFavorite{}).Error; err != nil {
			return newNotFoundDBError(err, "open_content_favorites")
		}
	} else {
		return fmt.Errorf("invalid parameters: must provide either Name or ContentURL")
	}
	return nil
}
