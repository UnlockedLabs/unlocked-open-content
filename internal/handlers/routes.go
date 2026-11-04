package handlers

import (
	"net/http"

	"github.com/UnlockedLabs/unlocked-contracts/features"
)

// routes is every Open Content route, with the access level and features it
// had in core (EN-192).
func (srv *server) routes() []route {
	oc := []features.Access{features.OpenContent}
	videos := []features.Access{features.UploadVideo}
	links := []features.Access{features.HelpfulLinks}
	return []route{
		// Libraries
		{Pattern: "GET /api/libraries", Handler: handle(srv.handleIndexLibraries), Features: oc},
		{Pattern: "GET /api/open-content/search", Handler: handle(srv.handleSearchOpenContent), Features: oc},
		{Pattern: "GET /api/open-content/suggestions", Handler: handle(srv.handleGetQuerySuggestions), Features: oc},
		{Pattern: "GET /api/libraries/{id}", Handler: handle(srv.handleGetLibrary), Features: oc},
		{Pattern: "PUT /api/libraries/{id}/favorite", Handler: handle(srv.handleToggleFavoriteLibrary), Features: oc},
		{Pattern: "PUT /api/libraries/{id}/toggle", Handler: handle(srv.handleToggleLibraryVisibility), Access: admin, Features: oc},
		{Pattern: "GET /api/libraries/{id}/facilities", Handler: handle(srv.handleGetLibraryFacilityVisibility), Access: deptAdmin, Features: oc},
		{Pattern: "PUT /api/libraries/{id}/facilities", Handler: handle(srv.handleSetLibraryFacilityVisibility), Access: deptAdmin, Features: oc},

		// Open content
		{Pattern: "GET /api/open-content", Handler: handle(srv.handleIndexOpenContent), Features: oc},
		{Pattern: "GET /api/open-content/favorites", Handler: handle(srv.handleGetUserFavoriteOpenContent), Features: oc},
		{Pattern: "PUT /api/open-content/{id}/bookmark", Handler: handle(srv.handleBookmarkOpenContent), Features: oc},
		{Pattern: "GET /api/open-content/favorite-groupings", Handler: handle(srv.handleGetUserFavoriteOpenContentGroupings), Features: oc},
		{Pattern: "POST /api/open-content/request-content", Handler: handle(srv.handleRequestOpenContent), Features: []features.Access{features.OpenContent, features.RequestContent}},

		// Activity
		{Pattern: "GET /api/open-content/activity", Handler: handle(srv.handleGetTopFacilityOpenContent), Features: oc},
		{Pattern: "GET /api/open-content/activity/{id}", Handler: handle(srv.handleGetTopUserOpenContent), Features: oc},
		{Pattern: "GET /api/libraries/activity", Handler: handle(srv.handleGetTopFacilityLibraries), Features: oc},

		// Videos
		{Pattern: "GET /api/videos", Handler: handle(srv.handleGetVideos), Features: videos},
		{Pattern: "GET /api/videos/{id}", Handler: handle(srv.handleGetVideoById), Features: videos},
		{Pattern: "PUT /api/videos/{id}/favorite", Handler: handle(srv.handleFavoriteVideo), Features: videos},
		{Pattern: "POST /api/videos", Handler: handle(srv.handlePostVideos), Access: admin, Features: videos},
		{Pattern: "PUT /api/videos/{id}/{action}", Handler: handle(srv.handleVideoAction), Access: admin, Features: videos},
		{Pattern: "GET /api/videos/{id}/facilities", Handler: handle(srv.handleGetVideoFacilityVisibility), Access: deptAdmin, Features: videos},
		{Pattern: "PUT /api/videos/{id}/facilities", Handler: handle(srv.handleSetVideoFacilityVisibility), Access: deptAdmin, Features: videos},
		{Pattern: "DELETE /api/videos/{id}", Handler: handle(srv.handleDeleteVideo), Access: admin, Features: videos},

		// Helpful links
		{Pattern: "GET /api/helpful-links", Handler: handle(srv.handleGetHelpfulLinks), Features: links},
		{Pattern: "PUT /api/helpful-links", Handler: handle(srv.handleAddHelpfulLink), Access: admin, Features: links},
		{Pattern: "PATCH /api/helpful-links/{id}/edit", Handler: handle(srv.handleEditLink), Access: admin, Features: links},
		{Pattern: "PUT /api/helpful-links/toggle/{id}", Handler: handle(srv.handleToggleVisibilityStatus), Access: admin, Features: links},
		{Pattern: "DELETE /api/helpful-links/{id}", Handler: handle(srv.handleDeleteLink), Access: admin, Features: links},
		{Pattern: "GET /api/helpful-links/facilities/{id}", Handler: handle(srv.handleGetLinkFacilityVisibility), Access: deptAdmin, Features: links},
		{Pattern: "PUT /api/helpful-links/facilities/{id}", Handler: handle(srv.handleSetLinkFacilityVisibility), Access: deptAdmin, Features: links},
		{Pattern: "PUT /api/helpful-links/activity/{id}", Handler: handle(srv.handleAddUserActivity), Features: links},
		{Pattern: "PUT /api/helpful-links/sort", Handler: handle(srv.changeSortOrder), Access: admin, Features: links},
		{Pattern: "PUT /api/helpful-links/favorite/{id}", Handler: handle(srv.handleFavoriteLink), Features: links},

		// Library categories. Any logged-in user, like before the move.
		{Pattern: "GET /api/tags", Handler: handle(srv.handleGetTags)},

		// Proxies. They check Open Content themselves and send a user without
		// it back to log in, so the routes list no features.
		{Pattern: "GET /api/proxy/libraries/{id}/", Handler: srv.libraryProxyMiddleware(http.HandlerFunc(srv.handleForwardKiwixProxy))},
		{Pattern: "GET /api/proxy/videos/{id}", Handler: srv.videoProxyMiddleware(http.HandlerFunc(srv.handleRedirectVideosS3))},
	}
}
