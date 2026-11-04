package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
)

// claimsFrom is the caller of a request. The router serves only requests the
// session check has passed, so the claims are always there.
func claimsFrom(r *http.Request) *identity.Claims {
	c, _ := identity.FromContext(r.Context())
	return c
}

func userIsAdmin(r *http.Request) bool {
	return claimsFrom(r).IsAdmin()
}

// getQueryContext resolves the facility scope for a request: a switch-capable
// admin (sysadmin/dept admin) gets the ?facility_id they're viewing, or 0
// (statewide) when none is selected; a facility admin is always pinned to their
// own facility.
func getQueryContext(r *http.Request) models.QueryContext {
	var facilityID, userID uint
	claims := claimsFrom(r)
	f, err := strconv.Atoi(r.URL.Query().Get("facility_id"))
	switch {
	case err == nil && claims.CanSwitchFacility():
		facilityID = uint(f)
	case claims.CanSwitchFacility():
		facilityID = 0
	default:
		facilityID = claims.FacilityID
	}
	u, err := strconv.Atoi(r.URL.Query().Get("user_id"))
	if err != nil {
		userID = claims.UserID
	} else {
		userID = uint(u)
	}
	page, perPage := getPaginationInfo(r)
	orderBy := strings.ToLower(r.URL.Query().Get("order_by"))
	order := strings.ToLower(r.URL.Query().Get("order"))
	if orderBy != "" && order == "" {
		orderBySplit := strings.Fields(orderBy)
		if len(orderBySplit) > 1 {
			orderBy = orderBySplit[0]
			order = orderBySplit[1]
		}
	}
	return models.QueryContext{
		Params:        r.URL.Query(),
		Ctx:           r.Context(),
		Page:          page,
		PerPage:       perPage,
		FacilityID:    facilityID,
		UserID:        userID,
		OrderBy:       orderBy,
		Order:         order,
		IsAdmin:       claims.IsAdmin(),
		Search:        strings.TrimSpace(strings.ToLower(r.URL.Query().Get("search"))),
		Tags:          r.URL.Query()["tags"],
		All:           r.URL.Query().Get("all") == "true",
		FeatureAccess: claims.FeatureAccess,
	}
}

// facilityScopedQueryContext is for reads whose results are per facility. A
// statewide admin has no single facility (FacilityID == 0), so these fall back
// to the admin's home facility rather than matching no facility at all.
func facilityScopedQueryContext(r *http.Request) models.QueryContext {
	args := getQueryContext(r)
	if args.FacilityID == 0 {
		args.FacilityID = claimsFrom(r).FacilityID
	}
	return args
}

func getPaginationInfo(r *http.Request) (int, int) {
	page := r.URL.Query().Get("page")
	perPage := r.URL.Query().Get("per_page")
	if page == "" {
		page = "1"
	}
	if perPage == "" {
		perPage = "20"
	}
	intPage, err := strconv.Atoi(page)
	if err != nil || intPage < 1 {
		intPage = 1
	}
	intPerPage, err := strconv.Atoi(perPage)
	if err != nil || intPerPage < 1 {
		intPerPage = 20
	}
	return intPage, intPerPage
}

const metricsDateFormat = "2006-01-02"

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// resolvePresetRange resolves a preset range key (e.g. "30d") against the
// server clock, returning the inclusive start day and an exclusive end bound
// (midnight after today, so all of "today" is included).
func resolvePresetRange(key string) (time.Time, time.Time, bool) {
	now := time.Now()
	today := startOfDay(now)
	endExclusive := today.AddDate(0, 0, 1)
	switch key {
	case "7d":
		return today.AddDate(0, 0, -6), endExclusive, true
	case "30d":
		return today.AddDate(0, 0, -29), endExclusive, true
	case "90d":
		return today.AddDate(0, 0, -89), endExclusive, true
	case "ytd":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()), endExclusive, true
	default:
		return time.Time{}, time.Time{}, false
	}
}

// parseDateRangeRequest reads a request's date range: all_time, a preset
// range, or start_date and end_date (the last 30 days when neither is set).
func parseDateRangeRequest(r *http.Request) (*time.Time, *time.Time, error) {
	q := r.URL.Query()
	if q.Get("all_time") == "true" {
		return nil, nil, nil
	}
	if rangeKey := strings.ToLower(q.Get("range")); rangeKey != "" && rangeKey != "custom" {
		start, end, ok := resolvePresetRange(rangeKey)
		if !ok {
			return nil, nil, newInvalidQueryParamServiceError(fmt.Errorf("unknown range %q", rangeKey), "range")
		}
		return &start, &end, nil
	}
	startStr := q.Get("start_date")
	endStr := q.Get("end_date")
	if startStr == "" && endStr == "" {
		now := time.Now()
		startDay := startOfDay(now.AddDate(0, 0, -30))
		endDay := startOfDay(now).AddDate(0, 0, 1)
		return &startDay, &endDay, nil
	}
	if startStr == "" || endStr == "" {
		return nil, nil, newBadRequestServiceError(fmt.Errorf("start date and end date are both required"), "date range")
	}
	start, err := time.Parse(metricsDateFormat, startStr)
	if err != nil {
		return nil, nil, newInvalidQueryParamServiceError(err, "start_date")
	}
	end, err := time.Parse(metricsDateFormat, endStr)
	if err != nil {
		return nil, nil, newInvalidQueryParamServiceError(err, "end_date")
	}
	if end.Before(start) {
		return nil, nil, newBadRequestServiceError(fmt.Errorf("end date must be on or after start date"), "date range")
	}
	// Clamp a future end date to the server's current day rather than
	// rejecting it.
	todayMidnight := startOfDay(time.Now())
	if end.After(todayMidnight) {
		end = todayMidnight
	}
	endExclusive := end.AddDate(0, 0, 1)
	return &start, &endExclusive, nil
}
