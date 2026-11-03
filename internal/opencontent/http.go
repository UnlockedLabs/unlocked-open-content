package opencontent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// The response, error, paging and request helpers Open Content's handlers
// use. They are copies of core's (EN-188), so every response is the same
// JSON as in core.

type resource[T any] struct {
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type paginatedResource[T any] struct {
	Message string                `json:"message"`
	Data    []T                   `json:"data"`
	Meta    models.PaginationMeta `json:"meta,omitempty"`
}

func writePaginatedResponse[T any](w http.ResponseWriter, status int, data []T, meta models.PaginationMeta) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := paginatedResource[T]{Message: "", Data: data, Meta: meta}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

func writeJsonResponse[T any](w http.ResponseWriter, status int, data T) error {
	if any(data) == nil {
		w.WriteHeader(status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if str, ok := any(data).(string); ok {
		resp := resource[struct{}]{Message: str, Data: struct{}{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return newResponseServiceError(err)
		}
		return nil
	}
	resp := resource[T]{Message: "Data fetched successfully", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return newResponseServiceError(err)
	}
	return nil
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resource[any]{Message: message}); err != nil {
		logrus.Error("error writing error response: ", err)
		http.Error(w, message, status)
	}
}

// handlerFunc is a route handler that returns its error for handle to write.
type handlerFunc func(w http.ResponseWriter, r *http.Request, log sLog) error

// handle turns h into an http.Handler that writes h's error as core's
// handleError does. An admin's write also gets core's admin audit line, with
// the same fields, after h runs.
func handle(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := sLog{f: logrus.Fields{"module": "opencontent", "method": r.Method, "path": r.URL.Path}}
		claims := claimsFrom(r)
		audit := claims.IsAdmin() && r.Method != http.MethodGet
		if audit {
			log.add("admin_id", claims.UserID)
			log.add("username", claims.Username)
			log.add("role", claims.Role)
			log.add("session_id", claims.SessionID)
			log.add("facility_id", claims.FacilityID)
			log.add("facility_name", claims.FacilityName)
			log.add("ip_address", clientIP(r))
		}
		if err := h(w, r, log); err != nil {
			var svcErr serviceError
			if errors.As(err, &svcErr) {
				errorResponse(w, svcErr.Status, svcErr.Message)
				log.error("Error occurred is ", svcErr.Err)
			} else {
				errorResponse(w, http.StatusInternalServerError, err.Error())
				log.error("Error occurred is ", err.Error())
			}
		}
		if audit {
			log.f["audit"] = "admin_action"
			logrus.WithFields(log.f).Println()
		}
	})
}

// clientIP is the caller's address: the first X-Forwarded-For entry, which
// the ingress sets, else the connection's.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip, _, _ := strings.Cut(fwd, ",")
		return ip
	}
	return r.RemoteAddr
}

// sLog adds fields to a request's log lines, like core's sLog.
type sLog struct {
	f logrus.Fields
}

// auditDetails names the admin action for the admin audit line.
func (slog sLog) auditDetails(action string) {
	slog.add("action", action)
}

// add adds a field to the request's log lines, including the admin audit
// line.
func (slog sLog) add(key string, value any) {
	slog.f[key] = value
}

func (slog sLog) info(args ...any) {
	logrus.WithFields(slog.f).Info(args...)
}

func (slog sLog) infof(format string, args ...any) {
	logrus.WithFields(slog.f).Infof(format, args...)
}

func (slog sLog) warn(args ...any) {
	logrus.WithFields(slog.f).Warn(args...)
}

func (slog sLog) warnf(format string, args ...any) {
	logrus.WithFields(slog.f).Warnf(format, args...)
}

func (slog sLog) error(args ...any) {
	logrus.WithFields(slog.f).Error(args...)
}

// serviceError is an error with the status and message a handler answers
// with.
type serviceError struct {
	Status  int
	Message string
	Err     error
}

func (svcErr serviceError) Error() string {
	return fmt.Sprintf("service error: %s", svcErr.Err.Error())
}

func newServiceError(err error, status int, msg string) serviceError {
	svcErr := serviceError{Status: status, Message: msg, Err: err}
	if dbErr, ok := err.(dbError); ok {
		svcErr.Err = errors.Join(errors.New(dbErr.Message), dbErr.InternalErr)
	}
	return svcErr
}

func newBadRequestServiceError(err error, msg string) serviceError {
	return newServiceError(err, http.StatusBadRequest, msg)
}

func newResponseServiceError(err error) serviceError {
	return newServiceError(err, http.StatusInternalServerError, "error writing response")
}

func newJSONReqBodyServiceError(err error) serviceError {
	return newServiceError(err, http.StatusBadRequest, "error reading json request")
}

func newUnauthorizedServiceError() serviceError {
	return newServiceError(errors.New("user unauthorized"), http.StatusUnauthorized, "Unauthorized")
}

func newInvalidIdServiceError(err error, idName string) serviceError {
	return newServiceError(err, http.StatusBadRequest, fmt.Sprintf("invalid %s", idName))
}

func newInvalidQueryParamServiceError(err error, paramName string) serviceError {
	return newServiceError(err, http.StatusBadRequest, fmt.Sprintf("invalid %s parameter", paramName))
}

func newForbiddenServiceError(err error, msg string) serviceError {
	return newServiceError(err, http.StatusForbidden, msg)
}

func newInternalServerServiceError(err error, msg string) serviceError {
	return newServiceError(err, http.StatusInternalServerError, msg)
}

// newDatabaseServiceError answers with a dbError's status and message, and
// with a 500 and the error's text for any other error.
func newDatabaseServiceError(err error) serviceError {
	if dbErr, ok := err.(dbError); ok {
		return serviceError{Status: dbErr.Status, Message: dbErr.Message, Err: dbErr.InternalErr}
	}
	return serviceError{Status: http.StatusInternalServerError, Message: err.Error(), Err: err}
}

// dbError is a failed query, with the status and message to answer with.
type dbError struct {
	Status      int
	Message     string
	InternalErr error
}

func (e dbError) Error() string {
	return fmt.Sprintf("db error: %s", e.InternalErr.Error())
}

func newDBError(err error, msg string) dbError {
	dbErr := dbError{Message: msg, InternalErr: err}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, gorm.ErrInvalidData),
		errors.Is(err, gorm.ErrEmptySlice),
		errors.Is(err, gorm.ErrInvalidField),
		errors.Is(err, gorm.ErrInvalidValueOfLength):
		dbErr.Status = http.StatusBadRequest
	case errors.Is(err, gorm.ErrPrimaryKeyRequired),
		errors.Is(err, gorm.ErrForeignKeyViolated),
		errors.Is(err, gorm.ErrDuplicatedKey),
		errors.Is(err, gorm.ErrCheckConstraintViolated):
		dbErr.Status = http.StatusUnprocessableEntity
	default:
		dbErr.Status = http.StatusInternalServerError
	}
	return dbErr
}

func newGetRecordsDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error getting %s", table))
}

func newDeleteDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error deleting %s", table))
}

func newCreateDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error creating %s", table))
}

func newUpdateDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("error updating %s", table))
}

func newNotFoundDBError(err error, table string) dbError {
	return newDBError(err, fmt.Sprintf("%s not found", table))
}

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
