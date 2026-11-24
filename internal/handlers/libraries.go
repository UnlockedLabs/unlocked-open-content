package handlers

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

// Retrieves either a paginated list of libraries or all libraries based upon the HTTP request parameters.
// Query Parameters:
// page - the page number for pagination
// perPage - the number of libraries to display on page
// search - the title or patial title of the libraries to search for
// order_by - (title|created_at|most_popular) the order in which the results are returned
// visibility - can either be featured, visible, hidden, or all
// all - true or false on whether or not to return all libraries without pagination
// categories - the tag ids to filter the libraries by
func (srv *server) handleIndexLibraries(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	showHidden := "visible"
	if !userIsAdmin(r) && r.URL.Query().Get("visibility") == "hidden" {
		return newUnauthorizedServiceError()
	} else if !userIsAdmin(r) && r.URL.Query().Get("visibility") == "featured" {
		showHidden = "featured"
	} else if userIsAdmin(r) {
		showHidden = r.URL.Query().Get("visibility")
	}
	if len(args.Tags) > 0 {
		log.add("category_ids", strings.Join(args.Tags, ","))
		log.info("Libraries are being filtered by categories")
	}
	libraries, err := srv.db.GetAllLibraries(&args, showHidden)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, libraries, args.IntoMeta())
}

func (srv *server) handleGetLibrary(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "library id")
	}
	library, err := srv.db.GetLibraryByID(id)
	if err != nil {
		log.add("library_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, library)
}

func (srv *server) handleGetQuerySuggestions(w http.ResponseWriter, r *http.Request, log sLog) error {
	query := r.URL.Query().Get("query")
	if query == "" {
		return newBadRequestServiceError(errors.New("query parameter is required"), "query parameter is required")
	}
	log.add("search_query", query)
	escapedQuery := url.QueryEscape(query)
	searchUrl := fmt.Sprintf("https://suggestqueries.google.com/complete/search?safe=on&client=firefox&q=%s", escapedQuery)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchUrl, nil)
	if err != nil {
		return newInternalServerServiceError(err, "unable to create new request to google_suggest")
	}
	resp, err := srv.client.Do(req)
	if err != nil {
		return newInternalServerServiceError(err, "error executing request to google_suggest")
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.error("error closing response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return newBadRequestServiceError(errors.New("api call to google suggest failed"), "response contained unexpected status code from google suggest")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return newInternalServerServiceError(err, "error reading body of response")
	}
	_, err = w.Write(body)
	return err
}

// searchScope is which kinds of content a search looks in: the repeated
// "types" param (library: library titles, content: inside library pages,
// video, link), all of them when it is absent. Videos and helpful links also
// need the caller's sub-feature, as their own lists do.
func searchScope(r *http.Request) models.SearchScope {
	types := r.URL.Query()["types"]
	want := func(t string) bool { return len(types) == 0 || slices.Contains(types, t) }
	claims := claimsFrom(r)
	return models.SearchScope{
		Content:   want("content"),
		Libraries: want("library"),
		Videos:    want("video") && claims.HasFeature(features.UploadVideo),
		Links:     want("link") && claims.HasFeature(features.HelpfulLinks),
	}
}

// handleSearchOpenContent answers a search with one ordered list: the title
// matches (libraries, then videos, then helpful links) followed by the Kiwix
// hits in Kiwix's own order. Page N is the slice [(N-1)*per_page, N*per_page)
// of that list, and the total is the title matches plus the Kiwix total, the
// same on every page. A search by library_id (the library viewer) has no
// title matches.
func (srv *server) handleSearchOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	page, perPage := getPaginationInfo(r)
	// capped so the page math below can't overflow, and Kiwix is never asked
	// for a huge page
	perPage = min(perPage, 100)
	page = min(page, 10000)
	search := r.URL.Query().Get("search")
	log.add("search", search)
	log.info("Executing open content search")
	var (
		err       error
		libraries []models.Library
	)
	titleSearch := make([]models.OpenContentItem, 0, 1)
	ids := r.URL.Query()["library_id"]
	libraryIDs := make([]int, 0, len(ids))
	for _, id := range ids {
		if libID, err := strconv.Atoi(id); err == nil {
			libraryIDs = append(libraryIDs, libID)
		}
	}
	// if we are on a library viewer page, we want to search
	// only the included library, so we omit title search
	queryCtx := facilityScopedQueryContext(r)
	scope := searchScope(r)
	if len(libraryIDs) == 0 {
		titleSearch, err = srv.db.OpenContentTitleSearch(&queryCtx, scope)
		if err != nil {
			log.error("error performing title search on open content")
		}
	}
	switch {
	case !scope.Content:
		// inside-library results not asked for: Kiwix is not searched
	case len(libraryIDs) > 0:
		libraries, err = srv.db.GetLibrariesByIDsAndLang(&queryCtx, libraryIDs, "eng", !userIsAdmin(r))
	default:
		libraries, err = srv.db.GetAllLibrariesByLang(&queryCtx, "eng")
	}
	if err != nil {
		log.add("library_ids", libraryIDs)
		return newDatabaseServiceError(err)
	}
	// the page is a slice of titles then Kiwix hits
	titleCount := len(titleSearch)
	offset := (page - 1) * perPage
	pageTitles := titleSearch[min(offset, titleCount):min(offset+perPage, titleCount)]
	kiwixStart := max(0, offset-titleCount)
	kiwixWanted := perPage - len(pageTitles)

	var (
		kiwixItems []models.SearchResultItem
		kiwixTotal int64
	)
	if len(libraries) > 0 {
		kiwixItems, kiwixTotal, err = srv.searchKiwix(r.Context(), log, libraries, search, kiwixStart, max(kiwixWanted, 1))
		if err != nil {
			return err
		}
		if kiwixWanted == 0 {
			kiwixItems = nil // only the total was needed
		}
	}
	total := int64(titleCount) + kiwixTotal
	rss := models.RSS{}
	channel := rss.SerializeSearchMeta(offset+1, perPage, int(total), search)
	channel.AppendTitleSearchResults(pageTitles)
	channel.Items = append(channel.Items, kiwixItems...)
	paginationData := models.NewPaginationInfo(page, perPage, total)
	return writePaginatedResponse(w, http.StatusOK, []*models.OpenContentSearchResult{channel}, paginationData)
}

// searchKiwix searches the libraries' pages: start is the 0-based index of
// the first hit (Kiwix's start is 0-based) and length how many to return. It
// returns the hits in Kiwix's order and the total number of hits.
func (srv *server) searchKiwix(ctx context.Context, log sLog, libraries []models.Library, search string, start, length int) ([]models.SearchResultItem, int64, error) {
	queryParams := url.Values{}
	for _, library := range libraries {
		queryParams.Add("books.name", path.Base(library.Url))
	}
	queryParams.Add("format", "xml")
	queryParams.Add("pattern", search)
	kiwixSearchURL := fmt.Sprintf("%s/search?start=%d&pageLength=%d&%s", models.KiwixLibraryUrl, start, length, queryParams.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, kiwixSearchURL, nil)
	log.add("kiwix_search_url", kiwixSearchURL)
	if err != nil {
		return nil, 0, newInternalServerServiceError(err, "unable to create new request to kiwix")
	}
	resp, err := srv.client.Do(request)
	if err != nil {
		return nil, 0, newInternalServerServiceError(err, "error executing kiwix search request")
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.error("error closing response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		log.add("status_code", resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, newInternalServerServiceError(err, "executing request returned unexpected status, and failed to read error from its response")
		}
		log.add("kiwix_error", string(body))
		return nil, 0, newBadRequestServiceError(errors.New("api call to kiwix failed"), "response contained unexpected status code from kiwix")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, newInternalServerServiceError(err, "error reading body of response")
	}
	var rss models.RSS
	if err = xml.Unmarshal(body, &rss); err != nil {
		return nil, 0, newInternalServerServiceError(err, "error parsing response body into XML")
	}
	total, err := strconv.ParseInt(strings.ReplaceAll(rss.Channel.TotalResults, ",", ""), 10, 64)
	if err != nil {
		return nil, 0, newInternalServerServiceError(err, "error parsing the total results value into an int64")
	}
	return rss.SerializeSearchResults(libraries).Items, total, nil
}

func (srv *server) handleToggleLibraryVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "library id")
	}
	library, err := srv.db.ToggleVisibilityAndRetrieveLibrary(id, &args)
	if err != nil {
		log.add("library_id", id)
		return newDatabaseServiceError(err)
	}
	srv.cacheLibraryVisibility(library, []uint{args.FacilityID}, log)
	return writeJsonResponse(w, http.StatusOK, "Library visibility updated successfully")
}

func (srv *server) handleGetLibraryFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "library id")
	}
	visibilities, err := srv.db.GetLibraryFacilityVisibility(&args, id)
	if err != nil {
		log.add("library_id", id)
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, visibilities)
}

func (srv *server) handleSetLibraryFacilityVisibility(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "library id")
	}
	var req setFacilityVisibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(req.FacilityIDs) == 0 {
		return newBadRequestServiceError(errors.New("facility_ids required"), "facility_ids required")
	}
	library, err := srv.setLibraryVisibility(&args, id, req.FacilityIDs, req.VisibilityStatus)
	if err != nil {
		log.add("library_id", id)
		return newDatabaseServiceError(err)
	}
	srv.cacheLibraryVisibility(library, req.FacilityIDs, log)
	return writeJsonResponse(w, http.StatusOK, "Library visibility updated successfully")
}

func (srv *server) handleToggleFavoriteLibrary(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	libraryID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInternalServerServiceError(err, "error converting content id to int")
	}
	var facilityID *uint = nil
	if userIsAdmin(r) {
		// an admin toggling this will save the facilityID as a 'featured' library for that facility
		facilityID = &claims.FacilityID
	}
	library, err := srv.db.GetLibraryByID(libraryID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if _, err := srv.db.FavoriteOpenContent(libraryID, library.OpenContentProviderID, claims.UserID, facilityID); err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, "Favorite toggled successfully")
}

func (srv *server) handleGetTags(w http.ResponseWriter, r *http.Request, log sLog) error {
	categories, err := srv.db.GetTags()
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, categories)
}
