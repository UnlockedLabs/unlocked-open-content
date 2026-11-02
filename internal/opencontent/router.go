package opencontent

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/sirupsen/logrus"
)

// The router puts each route behind the same checks core ran on it before
// the move (EN-205), in the same order and with the same answers: the login
// session and CSRF token, then the department admin check, then the admin
// check, then the route's features.

// route is one HTTP route and who may call it.
type route struct {
	Pattern string
	Handler http.Handler
	// Access is who may call the route. The zero value is anyUser.
	Access accessLevel
	// Features turns the route off unless every listed feature is enabled for
	// the caller.
	Features []features.Access
}

// accessLevel is who may call a route.
type accessLevel int

const (
	// anyUser is any logged-in user.
	anyUser accessLevel = iota
	// admin is any admin: facility, department or system.
	admin
	// deptAdmin is department and system admins: the admins who can switch
	// facility.
	deptAdmin
)

// newRouter serves routes, each behind requireLogin and its access and
// feature checks. requireLogin checks the session and puts the caller's
// identity.Claims in the request's context, as identity.SessionChecker's
// Require does.
func newRouter(routes []route, requireLogin func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.Pattern, requireLogin(guard(rt)))
	}
	return securityHeaders(mux)
}

// guard runs rt's access and feature checks before its handler.
func guard(rt route) http.Handler {
	h := requireFeatures(rt.Handler, rt.Features)
	switch rt.Access {
	case anyUser:
		return h
	case admin:
		return requireAdmin(h)
	case deptAdmin:
		return requireDeptAdmin(requireAdmin(h))
	default:
		// Routes are built once at startup, so an unknown level stops the
		// server instead of leaving a route open.
		panic(fmt.Sprintf("route %q: unknown access level %d", rt.Pattern, rt.Access))
	}
}

// requireDeptAdmin passes department and system admins. Its answer is core's
// route resolver's: plain text, not JSON.
func requireDeptAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFrom(r)
		if !claims.CanSwitchFacility() {
			logrus.WithFields(logrus.Fields{
				"handler":     "authMiddleware",
				"method":      r.Method,
				"path":        r.URL.Path,
				"user_id":     claims.UserID,
				"facility_id": claims.FacilityID,
			}).Warn("route resolver denied access to resource")
			http.Error(w, "User is not allowed to view this resource", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !claimsFrom(r).IsAdmin() {
			errorResponse(w, http.StatusUnauthorized, "Unauthorized - not admin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireFeatures(next http.Handler, required []features.Access) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFrom(r)
		if !claims.HasFeature(required...) {
			logrus.WithFields(logrus.Fields{
				"handler":           "checkFeatureAccessMiddleware",
				"method":            r.Method,
				"path":              r.URL.Path,
				"required_features": required,
				"user_id":           claims.UserID,
				"facility_id":       claims.FacilityID,
				"feature_access":    claims.FeatureAccess,
			}).Warn("feature not enabled for user")
			errorResponse(w, http.StatusUnauthorized, "Feature not enabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets core's security headers. The proxies' pages open in
// the app's iframe, so only they may be framed, and only by the app.
func securityHeaders(next http.Handler) http.Handler {
	dev := os.Getenv("APP_ENV") == "dev"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/proxy/") {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !dev {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
