package opencontent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-open-content/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

// newTestDB returns an empty in-memory database with Open Content's tables.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(testModels()...); err != nil {
		t.Fatal(err)
	}
	return db
}

// fakeCore stands in for the server's thumbnail endpoints: HEAD
// /api/photos/{name} and POST /upload.
type fakeCore struct {
	*httptest.Server
	// photos are the thumbnail names that already exist.
	photos map[string]bool

	mu      sync.Mutex
	uploads []upload
}

// upload is one thumbnail posted to /upload.
type upload struct {
	Filename string
	Fields   map[string]string
	Size     int
}

// newFakeCore starts a fakeCore and points APP_URL at it.
func newFakeCore(t *testing.T, photos ...string) *fakeCore {
	t.Helper()
	fc := &fakeCore{photos: map[string]bool{}}
	for _, p := range photos {
		fc.photos[p] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /api/photos/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !fc.photos[r.PathValue("name")] {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		fields := map[string]string{}
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
		fc.mu.Lock()
		fc.uploads = append(fc.uploads, upload{Filename: header.Filename, Fields: fields, Size: int(header.Size)})
		fc.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":    map[string]string{"url": "/api/photos/" + header.Filename},
			"message": "uploaded",
		})
	})
	fc.Server = httptest.NewServer(mux)
	t.Cleanup(fc.Close)
	t.Setenv("APP_URL", fc.URL)
	return fc
}

func (fc *fakeCore) Uploads() []upload {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]upload(nil), fc.uploads...)
}

// image returns a fake image of n bytes.
func image(n int) []byte {
	return []byte(strings.Repeat("x", n))
}

// testModels is Open Content's tables, for the SQLite test databases.
func testModels() []any {
	return []any{
		&models.HelpfulLink{},
		&models.OpenContentProvider{},
		&models.OpenContentUrl{},
		&models.OpenContentActivity{},
		&models.Library{},
		&models.FacilityVisibilityStatus{},
		&models.Video{},
		&models.VideoDownloadAttempt{},
		&models.OpenContentFavorite{},
		&models.Tag{},
		&models.OpenContentTag{},
	}
}

// fakeKV is the library_paths bucket in memory: Get, Put and Create, as
// NATS answers them.
type fakeKV struct {
	nats.KeyValue
	mu     sync.Mutex
	values map[string][]byte
}

func newFakeKV() *fakeKV { return &fakeKV{values: map[string][]byte{}} }

type fakeEntry struct {
	nats.KeyValueEntry
	value []byte
}

func (e fakeEntry) Value() []byte { return e.value }

func (kv *fakeKV) Get(key string) (nats.KeyValueEntry, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	v, ok := kv.values[key]
	if !ok {
		return nil, nats.ErrKeyNotFound
	}
	return fakeEntry{value: v}, nil
}

func (kv *fakeKV) Put(key string, value []byte) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.values[key] = value
	return 1, nil
}

func (kv *fakeKV) Create(key string, value []byte) (uint64, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if _, ok := kv.values[key]; ok {
		return 0, nats.ErrKeyExists
	}
	kv.values[key] = value
	return 1, nil
}
