package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/sirupsen/logrus"
)

// helpfulSortOrder is each facility's helpful-links sort order. It is kept in
// memory only, as it was in core.
var helpfulSortOrder = make(map[uint]string)

func (srv *server) changeSortOrder(w http.ResponseWriter, r *http.Request, log sLog) error {
	type reqBody struct {
		SortOrder string `json:"sort_order"`
	}
	var req reqBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	facilityID := claimsFrom(r).FacilityID
	helpfulSortOrder[facilityID] = req.SortOrder
	return writeJsonResponse(w, http.StatusOK, "Sort order changed successfully")
}

func (srv *server) handleGetHelpfulLinks(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	onlyVisible := r.URL.Query().Get("visibility") == "true"
	if !userIsAdmin(r) {
		onlyVisible = true
	}
	links, err := srv.db.GetHelpfulLinks(&args, onlyVisible)
	if err != nil {
		return newInternalServerServiceError(err, "error fetching helpful links")
	}
	respSort := struct {
		SortOrder string                   `json:"sort_order"`
		Links     []models.HelpfulLinkResp `json:"helpful_links"`
		Meta      models.PaginationMeta    `json:"meta"`
	}{SortOrder: helpfulSortOrder[claimsFrom(r).FacilityID], Links: links, Meta: args.IntoMeta()}
	return writeJsonResponse(w, http.StatusOK, respSort)
}

func (srv *server) handleAddHelpfulLink(w http.ResponseWriter, r *http.Request, log sLog) error {
	var link models.HelpfulLink
	err := json.NewDecoder(r.Body).Decode(&link)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	args := facilityScopedQueryContext(r)
	link.ThumbnailUrl = models.NilIfBlank(getFavicon(link.Url))
	log.infof("Adding helpful link icon %s", models.FormatNullableString(link.ThumbnailUrl))
	if err := srv.db.AddHelpfulLink(r.Context(), &args, &link); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusCreated, "Link added successfully")
}

func (srv *server) handleEditLink(w http.ResponseWriter, r *http.Request, log sLog) error {
	var link models.HelpfulLink
	err := json.NewDecoder(r.Body).Decode(&link)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Invalid id")
	}
	link.ThumbnailUrl = models.NilIfBlank(getFavicon(link.Url))
	if err = srv.db.EditLink(r.Context(), uint(id), link); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, link)
}

func (srv *server) handleToggleVisibilityStatus(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Invalid id")
	}
	log.infof("Toggling visibility status for link with id %d", id)
	args := facilityScopedQueryContext(r)
	if err := srv.db.ToggleVisibilityStatus(r.Context(), &args, id); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Visibility status toggled")
}

func (srv *server) handleGetLinkFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "link id")
	}
	visibilities, err := srv.db.GetHelpfulLinkFacilityVisibility(&args, id)
	if err != nil {
		log.add("link_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, visibilities)
}

func (srv *server) handleSetLinkFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "link id")
	}
	var req setFacilityVisibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.FacilityIDs) == 0 {
		return newBadRequestServiceError(errors.New("facility_ids required"), "facility_ids required")
	}
	if err := srv.setHelpfulLinkVisibility(&args, id, req.FacilityIDs, req.VisibilityStatus); err != nil {
		log.add("link_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Link visibility updated successfully")
}

func (srv *server) handleDeleteLink(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Invalid id")
	}
	if err := srv.db.DeleteLink(r.Context(), uint(id)); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Link deleted successfully")
}

func (srv *server) handleAddUserActivity(w http.ResponseWriter, r *http.Request, log sLog) error {
	var activity models.OpenContentActivity
	claims := claimsFrom(r)
	linkID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Invalid id")
	}
	link, err := srv.db.GetLinkFromId(uint(linkID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	openContentProviderID, err := srv.db.GetHelpfulLinkOpenContentProviderId()
	if err != nil {
		return newDatabaseServiceError(err)
	}

	activity.UserID = claims.UserID
	activity.FacilityID = claims.FacilityID
	activity.ContentID = link.ID
	activity.OpenContentProviderID = openContentProviderID
	srv.db.CreateContentActivity(link.Url, &activity)
	return writeJsonResponse(w, http.StatusOK, map[string]string{
		"url": link.Url,
	})
}

func (srv *server) handleFavoriteLink(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	userID := claims.UserID
	linkID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Invalid id")
	}
	facilityID := &claims.FacilityID
	if !userIsAdmin(r) {
		facilityID = nil
	}
	link, err := srv.db.GetLinkFromId(uint(linkID))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if _, err := srv.db.FavoriteOpenContent(int(linkID), link.OpenContentProviderID, uint(userID), facilityID); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Link favorite toggled successfully")
}

func getFavicon(link string) string {
	logrus.Printf("Getting favicon for link %s", link)
	parsed, err := url.Parse(link)
	if err != nil || parsed.Hostname() == "" {
		return "/ul-logo-d.svg"
	}
	domain := parsed.Hostname()
	return fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=64", domain)
}
