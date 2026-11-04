package handlers

import (
	"net/http"
	"strconv"
)

func (srv *server) handleGetTopFacilityOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	facilityId := claimsFrom(r).FacilityID
	topOpenContent, err := srv.db.GetTopFacilityOpenContent(int(facilityId))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, topOpenContent)
}

func (srv *server) handleGetTopUserOpenContent(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := facilityScopedQueryContext(r)
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "user ID")
	}
	topOpenContent, err := srv.db.GetTopUserOpenContent(id, &args)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, topOpenContent)
}

func (srv *server) handleGetTopFacilityLibraries(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	facilityId := claimsFrom(r).FacilityID
	_, perPage := getPaginationInfo(r)
	start, end, err := parseDateRangeRequest(r)
	if err != nil {
		return err
	}
	topLibraries, err := srv.db.GetTopFacilityLibraries(&args, int(facilityId), perPage, start, end)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	return writeJsonResponse(w, http.StatusOK, topLibraries)
}
