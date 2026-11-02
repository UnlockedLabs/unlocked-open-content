package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newHandler(t *testing.T, prefix string, v Version) http.Handler {
	t.Helper()
	h, err := NewHandler(prefix, v)
	if err != nil {
		t.Fatalf("NewHandler(%q) error = %v", prefix, err)
	}
	return h
}

func TestVersionRoute(t *testing.T) {
	v := Version{Version: "1.2.3", Commit: "abc1234", Branch: "main"}
	tests := []struct {
		name       string
		prefix     string
		method     string
		path       string
		wantStatus int
	}{
		{name: "no prefix", prefix: "", method: http.MethodGet, path: "/api/version", wantStatus: http.StatusOK},
		{name: "prefix", prefix: "/example", method: http.MethodGet, path: "/example/api/version", wantStatus: http.StatusOK},
		{name: "prefix with trailing slash", prefix: "/example/", method: http.MethodGet, path: "/example/api/version", wantStatus: http.StatusOK},
		{name: "prefix without leading slash", prefix: "example", method: http.MethodGet, path: "/example/api/version", wantStatus: http.StatusOK},
		{name: "outside the prefix", prefix: "/example", method: http.MethodGet, path: "/api/version", wantStatus: http.StatusNotFound},
		{name: "wrong method", prefix: "/example", method: http.MethodPost, path: "/example/api/version", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newHandler(t, tt.prefix, v).ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var got Version
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got != v {
				t.Fatalf("body = %+v, want %+v", got, v)
			}
		})
	}
}

func TestHealthcheckRoute(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "under the prefix", path: "/example/api/healthcheck", wantStatus: http.StatusOK},
		{name: "outside the prefix", path: "/api/healthcheck", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newHandler(t, "/example", Version{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && w.Body.String() != "OK" {
				t.Fatalf("body = %q, want %q", w.Body.String(), "OK")
			}
		})
	}
}

func TestCleanPrefix(t *testing.T) {
	tests := []struct {
		prefix  string
		want    string
		wantErr bool
	}{
		{prefix: "", want: ""},
		{prefix: "/", want: ""},
		{prefix: "example", want: "/example"},
		{prefix: "/example", want: "/example"},
		{prefix: "/example/", want: "/example"},
		{prefix: "/open-content/v1.2_x~y", want: "/open-content/v1.2_x~y"},
		{prefix: "/ex{x}", wantErr: true},
		{prefix: "/a b", wantErr: true},
		{prefix: " /example", wantErr: true},
		{prefix: "/a//b", wantErr: true},
		{prefix: "/a/../b", wantErr: true},
		{prefix: "/.", wantErr: true},
		{prefix: "/a?b", wantErr: true},
		{prefix: "/a%2Fb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			got, err := cleanPrefix(tt.prefix)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("cleanPrefix(%q) = %q, want an error", tt.prefix, got)
				}
				if _, err := NewHandler(tt.prefix, Version{}); err == nil {
					t.Fatalf("NewHandler(%q) error = nil, want one", tt.prefix)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("cleanPrefix(%q) = %q, %v, want %q", tt.prefix, got, err, tt.want)
			}
		})
	}
}

func TestServe(t *testing.T) {
	t.Run("bad prefix", func(t *testing.T) {
		err := Serve(context.Background(), "127.0.0.1:0", "/ex{x}", Version{})
		if err == nil || !strings.Contains(err.Error(), "path prefix") {
			t.Fatalf("Serve() error = %v, want a path prefix error", err)
		}
	})
	t.Run("address in use", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		err = Serve(context.Background(), ln.Addr().String(), "/example", Version{})
		if err == nil || !strings.Contains(err.Error(), "listen on") {
			t.Fatalf("Serve() error = %v, want a listen error", err)
		}
	})
	t.Run("stops cleanly when ctx is canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := Serve(ctx, "127.0.0.1:0", "/example", Version{}); err != nil {
			t.Fatalf("Serve() error = %v, want nil", err)
		}
	})
}

func TestVersionFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Version
	}{
		{
			name: "set",
			env:  map[string]string{"APP_VERSION": "1.2.3", "GIT_COMMIT": "abc1234", "GIT_BRANCH": "main"},
			want: Version{Version: "1.2.3", Commit: "abc1234", Branch: "main"},
		},
		{
			name: "unset",
			env:  map[string]string{"APP_VERSION": "", "GIT_COMMIT": "", "GIT_BRANCH": ""},
			want: Version{Version: "unknown", Commit: "unknown", Branch: "unknown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, val := range tt.env {
				t.Setenv(k, val)
			}
			if got := VersionFromEnv(); got != tt.want {
				t.Fatalf("VersionFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
