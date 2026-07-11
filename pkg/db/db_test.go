package db_test

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func TestDefaultOptionsFillPathsAndDurability(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	if opts.MessagePath == "" || opts.MetaPath == "" {
		t.Fatalf("paths not filled: %#v", opts)
	}
	if opts.Commit.FlushWindow <= 0 {
		t.Fatalf("flush window not filled: %#v", opts.Commit)
	}
}

func TestErrorsSupportErrorsIs(t *testing.T) {
	if !errors.Is(fmt.Errorf("wrap: %w", db.ErrNotFound), db.ErrNotFound) {
		t.Fatal("ErrNotFound does not support errors.Is")
	}
}

func TestOpenNodeStoreCreatesPhysicalStores(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()
	for _, path := range []string{opts.MessagePath, opts.MetaPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

func TestOpenNodeStoreExposesMetaDomain(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()
	if store.Meta() == nil {
		t.Fatal("Meta() = nil")
	}
}

func TestNodeStoreMessagesSharesCanonicalRegistry(t *testing.T) {
	store, err := db.OpenNodeStore(db.DefaultNodeStoreOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()

	firstDB := store.Messages()
	secondDB := store.Messages()
	if firstDB == nil || firstDB != secondDB {
		t.Fatalf("Messages() returned distinct domains: %p and %p", firstDB, secondDB)
	}
	lease, err := firstDB.Channel("shared:1", msgdb.ChannelID{ID: "shared", Type: 1})
	if err != nil {
		t.Fatalf("first Channel(): %v", err)
	}
	defer lease.Close()
	if other, err := secondDB.Channel("shared:1", msgdb.ChannelID{ID: "other", Type: 1}); other != nil || !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("second Channel() = (%v, %v), want shared-registry conflict", other, err)
	}
}

func TestNodeStoreMetricsSnapshotIncludesPhysicalStores(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()

	snapshot := store.MetricsSnapshot()
	if len(snapshot.Stores) != 2 {
		t.Fatalf("stores = %#v, want message and meta", snapshot.Stores)
	}
	byStore := make(map[string]db.StorageEngineMetrics, len(snapshot.Stores))
	for _, storeSnapshot := range snapshot.Stores {
		byStore[storeSnapshot.Store] = storeSnapshot.Engine
	}
	for _, name := range []string{"message", "meta"} {
		engineMetrics, ok := byStore[name]
		if !ok {
			t.Fatalf("store %q missing from metrics snapshot: %#v", name, snapshot.Stores)
		}
		if engineMetrics.DiskSpaceUsageBytes == 0 {
			t.Fatalf("store %q DiskSpaceUsageBytes = 0, want physical usage", name)
		}
	}
}

func TestNodeStoreMetricsSnapshotConcurrentClose(t *testing.T) {
	store, err := db.OpenNodeStore(db.DefaultNodeStoreOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}

	const readers = 8
	start := make(chan struct{})
	ready := make(chan struct{}, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ready <- struct{}{}
			for j := 0; j < 2000; j++ {
				_ = store.MetricsSnapshot()
			}
		}()
	}
	close(start)
	for i := 0; i < readers; i++ {
		<-ready
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	wg.Wait()
}

func TestNodeStoreMetricsSnapshotConcurrentMessagesClose(t *testing.T) {
	store, err := db.OpenNodeStore(db.DefaultNodeStoreOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}

	const readers = 8
	start := make(chan struct{})
	ready := make(chan struct{}, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ready <- struct{}{}
			for j := 0; j < 2000; j++ {
				_ = store.MetricsSnapshot()
			}
		}()
	}
	close(start)
	for i := 0; i < readers; i++ {
		<-ready
	}
	if err := store.Messages().Close(); err != nil {
		t.Fatalf("Messages().Close(): %v", err)
	}
	wg.Wait()
	if err := store.Close(); err != nil {
		t.Fatalf("NodeStore.Close(): %v", err)
	}
}
