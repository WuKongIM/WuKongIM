package backup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestEnsureRepositoryCreatesAndReusesOneClusterLineage(t *testing.T) {
	ctx := context.Background()
	store := newMemoryArchiveStore()

	created, err := backup.EnsureRepository(ctx, store, "cluster-a", 1_800_000_000_000)
	if err != nil {
		t.Fatalf("EnsureRepository(create): %v", err)
	}
	if created.SourceClusterID != "cluster-a" ||
		created.HashSlotCount != backup.DefaultHashSlotCount {
		t.Fatalf("created marker = %#v", created)
	}

	loaded, err := backup.EnsureRepository(ctx, store, "cluster-a", 1_900_000_000_000)
	if err != nil {
		t.Fatalf("EnsureRepository(load): %v", err)
	}
	if loaded != created {
		t.Fatalf("loaded marker = %#v, want %#v", loaded, created)
	}

	if _, err := backup.EnsureRepository(ctx, store, "cluster-b", 1_900_000_000_000); !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("EnsureRepository(other cluster) error = %v", err)
	}
}

func TestEnsureRepositoryHandlesConcurrentFirstWriter(t *testing.T) {
	ctx := context.Background()
	winner := backup.RepositoryMarker{
		Format:              backup.RepositoryFormat,
		Version:             backup.RepositoryVersion,
		SourceClusterID:     "cluster-a",
		HashSlotCount:       backup.DefaultHashSlotCount,
		CreatedAtUnixMillis: 1_800_000_000_001,
	}
	winnerBody, err := backup.MarshalRepositoryMarker(winner)
	if err != nil {
		t.Fatalf("MarshalRepositoryMarker(): %v", err)
	}
	store := newMemoryArchiveStore()
	store.putError = backup.ErrObjectExists
	store.putCollision = winnerBody

	got, err := backup.EnsureRepository(ctx, store, "cluster-a", 1_800_000_000_000)
	if err != nil {
		t.Fatalf("EnsureRepository(collision): %v", err)
	}
	if got != winner {
		t.Fatalf("marker = %#v, want collision winner %#v", got, winner)
	}
}

func TestEnsureRepositoryRejectsInvalidAndCorruptState(t *testing.T) {
	ctx := context.Background()
	storageFailure := errors.New("storage failed")
	readFailure := errors.New("read failed")
	closeFailure := errors.New("close failed")

	tests := []struct {
		name      string
		store     backup.ArchiveStore
		clusterID string
		now       int64
		wantErr   error
	}{
		{name: "nil store", clusterID: "cluster-a", now: 1, wantErr: backup.ErrInvalidObject},
		{name: "empty cluster", store: newMemoryArchiveStore(), now: 1, wantErr: backup.ErrInvalidObject},
		{name: "invalid time", store: newMemoryArchiveStore(), clusterID: "cluster-a", wantErr: backup.ErrInvalidObject},
		{
			name: "open failure",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.openErrors[backup.RepositoryMarkerKey] = storageFailure
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: storageFailure,
		},
		{
			name: "empty marker",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.objects[backup.RepositoryMarkerKey] = nil
				store.reportedBytes[backup.RepositoryMarkerKey] = 0
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: backup.ErrObjectCorrupt,
		},
		{
			name: "oversized marker",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.objects[backup.RepositoryMarkerKey] = []byte("small body")
				store.reportedBytes[backup.RepositoryMarkerKey] = (64 << 10) + 1
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: backup.ErrObjectCorrupt,
		},
		{
			name: "short body",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.objects[backup.RepositoryMarkerKey] = []byte("{}")
				store.reportedBytes[backup.RepositoryMarkerKey] = 3
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: backup.ErrObjectCorrupt,
		},
		{
			name: "read failure",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.add(backup.RepositoryMarkerKey, []byte("{}"))
				store.readErrors[backup.RepositoryMarkerKey] = readFailure
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: readFailure,
		},
		{
			name: "close failure",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.add(backup.RepositoryMarkerKey, []byte("{}"))
				store.closeErrors[backup.RepositoryMarkerKey] = closeFailure
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: closeFailure,
		},
		{
			name: "invalid marker JSON",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.add(backup.RepositoryMarkerKey, []byte("not-json"))
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "put failure",
			store: func() *memoryArchiveStore {
				store := newMemoryArchiveStore()
				store.putError = storageFailure
				return store
			}(),
			clusterID: "cluster-a", now: 1, wantErr: storageFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := backup.EnsureRepository(ctx, test.store, test.clusterID, test.now)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("EnsureRepository() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
