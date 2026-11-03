package opencontent

import (
	"sync"
	"testing"

	"github.com/UnlockedLabs/unlocked-open-content/internal/database"
	"github.com/UnlockedLabs/unlocked-open-content/internal/testutil"
	"github.com/nats-io/nats.go"
)

// newTestDB returns an empty in-memory database with Open Content's tables.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	return database.NewDB(testutil.NewDB(t))
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
