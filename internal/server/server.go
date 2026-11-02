// Package server is the product's HTTP API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Version is what GET <prefix>/api/version reports, the same JSON as core's
// and the tutor's /api/version.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Branch  string `json:"branch"`
}

// VersionFromEnv reads the version, commit and branch the Dockerfile bakes
// into the image from its build args. Each is "unknown" when it isn't set.
func VersionFromEnv() Version {
	return Version{
		Version: envOr("APP_VERSION", "unknown"),
		Commit:  envOr("GIT_COMMIT", "unknown"),
		Branch:  envOr("GIT_BRANCH", "unknown"),
	}
}

// NewHandler returns the API's routes, all under prefix: the path ingress
// sends this product's requests to, such as "/example". An empty prefix
// serves from the root. See cleanPrefix for what prefix may be.
func NewHandler(prefix string, v Version) (http.Handler, error) {
	prefix, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	// The platform chart's readiness probe. Same path and body as core's.
	mux.HandleFunc("GET "+prefix+"/api/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("OK")); err != nil {
			slog.Error("write healthcheck response", "err", err)
		}
	})
	mux.HandleFunc("GET "+prefix+"/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			slog.Error("write version response", "err", err)
		}
	})
	return mux, nil
}

// prefixSegment is one part of a path prefix: letters, digits and the other
// characters a URL path never escapes. Anything else could change what the
// route patterns mean, such as "{" (a wildcard) or a missing leading "/" (a
// host name).
var prefixSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// cleanPrefix gives prefix one leading "/" and no trailing one, so "example",
// "/example" and "/example/" all serve under "/example", and "" and "/" serve
// from the root. A prefix with any other character, an empty part ("//") or a
// "." or ".." part is an error.
func cleanPrefix(prefix string) (string, error) {
	trimmed := strings.Trim(prefix, "/")
	if trimmed == "" {
		return "", nil
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if !prefixSegment.MatchString(seg) || seg == "." || seg == ".." {
			return "", fmt.Errorf("path prefix %q: each part between slashes must be letters, digits, '-', '.', '_' or '~'", prefix)
		}
	}
	return "/" + trimmed, nil
}

// Serve runs the API on addr until ctx is canceled, then shuts it down,
// giving requests in flight up to 10 seconds to finish. A bad prefix or an
// address it can't listen on is an error before it serves anything.
func Serve(ctx context.Context, addr, prefix string, v Version) error {
	prefix, err := cleanPrefix(prefix)
	if err != nil {
		return err
	}
	handler, err := NewHandler(prefix, v)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.Serve(ln) }()
	slog.Info("serving", "addr", ln.Addr().String(), "prefix", prefix, "version", v.Version)

	select {
	case err := <-errs:
		return fmt.Errorf("serve on %s: %w", addr, err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-errs; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", addr, err)
	}
	slog.Info("stopped serving")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
