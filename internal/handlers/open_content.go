package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

func (srv *server) handleIndexOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	only := r.URL.Query().Get("all")
	var all bool
	if userIsAdmin(r) && strings.ToLower(strings.TrimSpace(only)) == "true" {
		all = true
	}
	content, err := srv.db.GetOpenContent(all)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, content)
}

func (srv *server) handleGetUserFavoriteOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	favorites, err := srv.db.GetUserFavorites(&args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, favorites, args.IntoMeta())
}

func (srv *server) handleGetUserFavoriteOpenContentGroupings(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	favorites, err := srv.db.GetUserFavoriteGroupings(&args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, favorites)
}

func (srv *server) handleBookmarkOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	var requestBody struct {
		Name                  string `json:"name,omitempty"`
		ContentURL            string `json:"content_url,omitempty"`
		OpenContentProviderId uint   `json:"open_content_provider_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	contentID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "Content ID")
	}
	contentParams := models.OpenContentParams{
		UserID:                claimsFrom(r).UserID,
		ContentID:             uint(contentID),
		Name:                  requestBody.Name,
		ContentURL:            requestBody.ContentURL,
		OpenContentProviderID: requestBody.OpenContentProviderId,
	}
	err = srv.db.BookmarkOpenContent(&contentParams)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Bookmark toggled successfully")
}

func (srv *server) handleRequestOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newBadRequestServiceError(err, "error reading content requests")
	}
	claims := claimsFrom(r)
	username := claims.Username
	facilityName := claims.FacilityName
	subject := "Content Request - " + username + " - " + facilityName
	bodyHTML := getBodyHTML(username, facilityName, req.Content)
	err := srv.sendEmail(r.Context(), subject, "Content requests recieved for UnlockEd", bodyHTML)
	if err != nil {
		return newBadRequestServiceError(err, "error sending email")
	}
	return writeJsonResponse(w, http.StatusOK, "content requests sent")
}

func getBodyHTML(username, facility, bodyText string) string {
	pasteableString := `=SPLIT("` + username + `|` + facility + `|` + bodyText + `", "|")`
	return fmt.Sprintf(`<div style="font-family: Arial, sans-serif; font-size: 16px;">
        <p>Content Request from %s at %s</p>
        <p>We have received the following information:</p>
		<h3>Username</h3>
		<p>%s</p>
		<h3>Facility Name</h3>
		<p>%s</p>
		<h3>Content Request</h3>
		<p>%s</p>
		<p>Pasteable string for quick import into csv file:</p>
		<p>%s</p>
    </div>`, html.EscapeString(username), html.EscapeString(facility), html.EscapeString(username), html.EscapeString(facility), html.EscapeString(bodyText), pasteableString)
}
