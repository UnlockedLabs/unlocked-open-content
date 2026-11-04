package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/sirupsen/logrus"
)

// The response and log helpers Open Content's handlers use, with their errors
// (service_error.go) and request readers (request.go). They are copies of
// core's (EN-188), so every response is the same JSON as in core.

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
