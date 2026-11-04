package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/UnlockedLabs/unlocked-open-content/internal/tasks"
)

func (srv *server) handleGetVideos(w http.ResponseWriter, r *http.Request, log sLog) error {
	user := claimsFrom(r)
	visibility := r.URL.Query().Get("visibility")

	if !user.IsAdmin() {
		visibility = "visible"
	}
	args := facilityScopedQueryContext(r)
	videos, err := srv.db.GetAllVideos(&args, visibility)
	if err != nil {
		return newInternalServerServiceError(err, "error fetching videos")
	}
	return writePaginatedResponse(w, http.StatusOK, videos, args.IntoMeta())
}

func (srv *server) handleGetVideoById(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newBadRequestServiceError(err, "error reading video id")
	}
	user := claimsFrom(r)
	video, err := srv.db.GetVideoByID(id, user.FacilityID)
	if err != nil {
		return newInternalServerServiceError(err, "error fetching video")
	}
	if !user.IsAdmin() && !video.VisibilityStatus || video.Availability != models.VideoAvailable {
		return newForbiddenServiceError(errors.New("video not visible"), "you are not authorized to view this content")
	}
	videoViewerUrl := fmt.Sprintf("/viewer/videos/%d", video.ID)
	activity := models.OpenContentActivity{
		OpenContentProviderID: video.OpenContentProviderID,
		FacilityID:            user.FacilityID,
		UserID:                user.UserID,
		ContentID:             video.ID,
	}
	if !user.IsAdmin() {
		srv.createContentActivityAndNotifyWS(videoViewerUrl, &activity)
	}
	return writeJsonResponse(w, http.StatusOK, video)
}

const (
	FeatureVideoAction     = "feature"
	ToggleVisibilityAction = "visibility"
	RetryVideoAction       = "retry"
)

func (srv *server) handleVideoAction(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	userID, facilityID := claims.UserID, claims.FacilityID
	vidId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "video_id")
	}
	video, err := srv.db.GetVideoByID(vidId, claims.FacilityID)
	if err != nil {
		return newInvalidIdServiceError(err, "video_id")
	}

	switch r.PathValue("action") {
	case FeatureVideoAction: // this is an admin only action, so pass the facilityID to 'feature' the content
		isFavorited, err := srv.db.FavoriteOpenContent(vidId, video.OpenContentProviderID, userID, &facilityID)
		if err != nil {
			return newInternalServerServiceError(err, "error favoriting video")
		}
		msg := ""
		if isFavorited {
			msg = "video added to featured list"
		} else {
			msg = "video removed from featured list"
		}
		log.auditDetails("video_featured")
		return writeJsonResponse(w, http.StatusOK, msg)

	case ToggleVisibilityAction:
		if err = srv.db.ToggleVideoVisibility(r.Context(), vidId, facilityID); err != nil {
			return newInternalServerServiceError(err, "error toggling video visibility")
		}
		log.auditDetails("visibility_toggled")
		return writeJsonResponse(w, http.StatusOK, "video visibility toggled")

	case RetryVideoAction:
		log.auditDetails("retry_video_download")
		if len(video.Attempts) >= models.MAX_DOWNLOAD_ATTEMPTS {
			return newBadRequestServiceError(errors.New("max attempts reached"), "max download attempts reached, please remove video and try again")
		}
		msg, err := tasks.RetryManualDownloadMsg(video)
		if err != nil {
			return newInternalServerServiceError(err, "error marshalling video")
		}
		if err := srv.nats.PublishMsg(msg); err != nil {
			return newInternalServerServiceError(err, "error publishing retry job")
		}
		return writeJsonResponse(w, http.StatusOK, "retry job published, please wait...")
	}
	return newBadRequestServiceError(errors.New("invalid action"), "invalid action")
}

func (srv *server) handleGetVideoFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "video id")
	}
	visibilities, err := srv.db.GetVideoFacilityVisibility(&args, id)
	if err != nil {
		log.add("video_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, visibilities)
}

func (srv *server) handleSetVideoFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "video id")
	}
	var req setFacilityVisibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.FacilityIDs) == 0 {
		return newBadRequestServiceError(errors.New("facility_ids required"), "facility_ids required")
	}
	if err := srv.setVideoVisibility(&args, id, req.FacilityIDs, req.VisibilityStatus); err != nil {
		log.add("video_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Video visibility updated successfully")
}

func (srv *server) handlePostVideos(w http.ResponseWriter, r *http.Request, log sLog) error {
	var video struct {
		VideoUrls []string `json:"video_urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&video); err != nil {
		return newBadRequestServiceError(err, "error reading video")
	}
	if len(video.VideoUrls) == 0 {
		return newBadRequestServiceError(errors.New("no video URLs provided"), "video_urls is required")
	}
	for _, videoURL := range video.VideoUrls {
		if err := models.ValidateVideoURL(videoURL); err != nil {
			return newBadRequestServiceError(err, "invalid video URL")
		}
	}
	provider, err := srv.db.GetVideoProvider()
	if err != nil {
		return newInternalServerServiceError(err, "error fetching video provider")
	}
	msg, err := tasks.AddVideosMsg(video.VideoUrls, provider)
	if err != nil {
		return newInternalServerServiceError(err, "error publishing add_video job")
	}
	if err = srv.nats.PublishMsg(msg); err != nil {
		return newInternalServerServiceError(err, "error publishing video")
	}
	return writeJsonResponse(w, http.StatusCreated, "videos added, processing")
}

func (srv *server) handleDeleteVideo(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newBadRequestServiceError(err, "error reading video id")
	}
	if err = srv.db.DeleteVideo(id); err != nil {
		return newInternalServerServiceError(err, "error deleting video")
	}
	return writeJsonResponse(w, http.StatusOK, "video deleted")
}

func (srv *server) handleFavoriteVideo(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	userID := claims.UserID

	vidId, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "video_id")
	}
	video, err := srv.db.GetVideoByID(vidId, claims.FacilityID)
	if err != nil {
		return newInvalidIdServiceError(err, "video_id")
	}
	isFavorited, err := srv.db.FavoriteOpenContent(vidId, video.OpenContentProviderID, userID, nil)
	if err != nil {
		return newInternalServerServiceError(err, "error favoriting video")
	}
	msg := ""
	if isFavorited {
		msg = "video added to favorites"
	} else {
		msg = "video removed from favorites"
	}
	return writeJsonResponse(w, http.StatusOK, msg)
}
