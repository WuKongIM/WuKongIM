package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArchiveFinalizerPublishesCompleteBeforeMakingTheArchiveDiscoverable(t *testing.T) {
	store := newContractArchiveStore()
	job := completeBackupJob(t, store, "backup-publication")
	finalizer := newArchiveFinalizer(t)

	if err := finalizer.Publish(context.Background(), store, job); err != nil {
		t.Fatalf("Publish(): %v", err)
	}
	manifestPosition := keyPosition(store.puts, "backups/backup-publication/manifest.json")
	completePosition := keyPosition(store.puts, "backups/backup-publication/COMPLETE")
	catalogPosition := keyPosition(store.puts, "catalog/backup-publication")
	if manifestPosition < 0 || completePosition <= manifestPosition ||
		catalogPosition <= completePosition {
		t.Fatalf("publication order = %#v", store.puts)
	}
	manifest, err := backupartifact.VerifyPublishedArchive(
		context.Background(), store, job.ID,
	)
	if err != nil {
		t.Fatalf("VerifyPublishedArchive(): %v", err)
	}
	if manifest.ID != job.ID || manifest.HashSlotCount != backupcontract.HashSlotCount ||
		len(manifest.Slots) != backupcontract.HashSlotCount {
		t.Fatalf("published manifest = %+v", manifest)
	}
}

func TestArchiveFinalizerDoesNotPublishCatalogWhenCompleteMarkerWriteFails(t *testing.T) {
	store := newContractArchiveStore()
	job := completeBackupJob(t, store, "backup-incomplete-publication")
	store.failPutKey = "backups/backup-incomplete-publication/COMPLETE"
	finalizer := newArchiveFinalizer(t)

	if err := finalizer.Publish(context.Background(), store, job); err == nil {
		t.Fatal("Publish() error = nil")
	}
	for _, key := range []string{
		"backups/backup-incomplete-publication/COMPLETE",
		"catalog/backup-incomplete-publication",
	} {
		reader, _, err := store.Open(context.Background(), key)
		if reader != nil {
			_ = reader.Close()
		}
		if !errors.Is(err, backupartifact.ErrObjectNotFound) {
			t.Fatalf("Open(%s) error = %v, want not found", key, err)
		}
	}
}

func TestArchiveFinalizerRejectsIncompleteOrMisorderedSlotProgressBeforeRepositoryMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*backupcontract.BackupJob)
	}{
		{
			name: "missing Slot",
			mutate: func(job *backupcontract.BackupJob) {
				job.Slots = job.Slots[:backupcontract.HashSlotCount-1]
			},
		},
		{
			name: "duplicate Slot identity",
			mutate: func(job *backupcontract.BackupJob) {
				job.Slots[1].HashSlot = 0
			},
		},
		{
			name: "unfinished Slot",
			mutate: func(job *backupcontract.BackupJob) {
				job.Slots[1].Status = backupcontract.SlotStatusRunning
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newContractArchiveStore()
			finalizer := newArchiveFinalizer(t)
			job := backupProgressOnlyJob("backup-incomplete-job")
			test.mutate(&job)

			if err := finalizer.Publish(context.Background(), store, job); err == nil {
				t.Fatal("Publish() error = nil")
			}
			if len(store.puts) != 0 {
				t.Fatalf(
					"repository writes = %#v, want none for invalid progress",
					store.puts,
				)
			}
		})
	}
}

func newArchiveFinalizer(t *testing.T) *backupinfra.ArchiveFinalizer {
	t.Helper()
	finalizer, err := backupinfra.NewArchiveFinalizer(
		backupinfra.ArchiveFinalizerOptions{
			ClusterID: "cluster-contract", Application: "backup-contract-test",
			Now: func() time.Time {
				return time.UnixMilli(1_800_000_001_000).UTC()
			},
		},
	)
	if err != nil {
		t.Fatalf("NewArchiveFinalizer(): %v", err)
	}
	return finalizer
}

func completeBackupJob(
	t *testing.T,
	store *contractArchiveStore,
	backupID string,
) backupcontract.BackupJob {
	t.Helper()
	logical := []byte("portable metadata snapshot")
	var compressed bytes.Buffer
	descriptor, err := backupartifact.EncodeChunk(
		&compressed, bytes.NewReader(logical),
	)
	if err != nil {
		t.Fatalf("EncodeChunk(): %v", err)
	}
	job := backupcontract.BackupJob{
		ID: backupID, Trigger: backupcontract.TriggerManual,
		StartedAtUnixMillis: 1_800_000_000_000,
		UpdatedUnixMillis:   1_800_000_001_000,
		Slots:               make([]backupcontract.SlotProgress, backupcontract.HashSlotCount),
	}
	for hashSlot := 0; hashSlot < backupcontract.HashSlotCount; hashSlot++ {
		chunkKey := fmt.Sprintf(
			"slots/%03d/attempts/00000001-contract/meta-000001.zst",
			hashSlot,
		)
		putContractObject(
			t, store, "backups/"+backupID+"/"+chunkKey,
			compressed.Bytes(), false,
		)
		manifest := backupartifact.SlotManifest{
			Format:   backupartifact.SlotManifestFormat,
			Version:  backupartifact.SlotManifestVersion,
			HashSlot: uint16(hashSlot),
			Cut: backupartifact.SlotCut{
				PhysicalSlotID: 1, LeaderTerm: 2, AppliedTerm: 2,
				ConfigurationVersion: 3, AppliedIndex: 4,
				CapturedAtUnixMillis: 1_800_000_000_100 + int64(hashSlot),
			},
			Chunks: []backupartifact.ChunkReference{{
				Kind:     backupartifact.ChunkKindMetadata,
				Sequence: 1, Stream: 0, Part: 1, Final: true,
				Key: chunkKey, Descriptor: descriptor, Records: 1,
			}},
			LogicalBytes: descriptor.LogicalBytes,
			StoredBytes:  descriptor.StoredBytes,
			Records:      1,
		}
		body, err := backupartifact.MarshalSlotManifest(manifest)
		if err != nil {
			t.Fatalf("MarshalSlotManifest(%d): %v", hashSlot, err)
		}
		manifestKey := fmt.Sprintf(
			"slots/%03d/attempts/00000001-contract/manifest.json",
			hashSlot,
		)
		putContractObject(
			t, store, "backups/"+backupID+"/"+manifestKey, body, false,
		)
		sum := sha256.Sum256(body)
		job.Slots[hashSlot] = backupcontract.SlotProgress{
			HashSlot: uint16(hashSlot), Status: backupcontract.SlotStatusComplete,
			Attempt: 1, OwnerNodeID: 1, OwnerTerm: 2,
			ManifestKey:    manifestKey,
			ManifestSHA256: hex.EncodeToString(sum[:]),
			LogicalBytes:   descriptor.LogicalBytes,
			StoredBytes:    descriptor.StoredBytes, Records: 1,
		}
	}
	return job
}

func backupProgressOnlyJob(backupID string) backupcontract.BackupJob {
	job := backupcontract.BackupJob{
		ID: backupID, Trigger: backupcontract.TriggerManual,
		StartedAtUnixMillis: 1_800_000_000_000,
		UpdatedUnixMillis:   1_800_000_001_000,
		Slots:               make([]backupcontract.SlotProgress, backupcontract.HashSlotCount),
	}
	for hashSlot := range job.Slots {
		job.Slots[hashSlot] = backupcontract.SlotProgress{
			HashSlot: uint16(hashSlot), Status: backupcontract.SlotStatusComplete,
		}
	}
	return job
}

func putContractObject(
	t *testing.T,
	store backupartifact.ArchiveStore,
	key string,
	body []byte,
	ifAbsent bool,
) {
	t.Helper()
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: key, Body: bytes.NewReader(body), ExpectedBytes: uint64(len(body)),
		IfAbsent: ifAbsent,
	}); err != nil {
		t.Fatalf("Put(%s): %v", key, err)
	}
}

func keyPosition(keys []string, key string) int {
	for index, candidate := range keys {
		if candidate == key {
			return index
		}
	}
	return -1
}

type contractArchiveObject struct {
	body     []byte
	modified time.Time
}

type contractArchiveStore struct {
	mu         sync.Mutex
	objects    map[string]contractArchiveObject
	puts       []string
	failPutKey string
}

func newContractArchiveStore() *contractArchiveStore {
	return &contractArchiveStore{
		objects: make(map[string]contractArchiveObject),
	}
}

func (s *contractArchiveStore) Put(
	ctx context.Context,
	object backupartifact.PutObject,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if object.Key == s.failPutKey {
		return errors.New("injected publication failure")
	}
	body, err := io.ReadAll(object.Body)
	if err != nil {
		return err
	}
	if uint64(len(body)) != object.ExpectedBytes {
		return backupartifact.ErrInvalidObject
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[object.Key]; exists && object.IfAbsent {
		return backupartifact.ErrObjectExists
	}
	s.objects[object.Key] = contractArchiveObject{
		body: append([]byte(nil), body...), modified: time.Now().UTC(),
	}
	s.puts = append(s.puts, object.Key)
	return nil
}

func (s *contractArchiveStore) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, backupartifact.ArchiveObject{}, err
	}
	s.mu.Lock()
	object, exists := s.objects[key]
	s.mu.Unlock()
	if !exists {
		return nil, backupartifact.ArchiveObject{}, backupartifact.ErrObjectNotFound
	}
	body := append([]byte(nil), object.body...)
	return io.NopCloser(bytes.NewReader(body)), backupartifact.ArchiveObject{
		Key: key, Bytes: uint64(len(body)), Modified: object.modified,
	}, nil
}

func (s *contractArchiveStore) List(
	ctx context.Context,
	prefix string,
) ([]backupartifact.ArchiveObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := make([]backupartifact.ArchiveObject, 0)
	for key, object := range s.objects {
		if key != prefix && !strings.HasPrefix(key, prefix+"/") {
			continue
		}
		objects = append(objects, backupartifact.ArchiveObject{
			Key: key, Bytes: uint64(len(object.body)), Modified: object.modified,
		})
	}
	sort.Slice(objects, func(left, right int) bool {
		return objects[left].Key < objects[right].Key
	})
	return objects, nil
}

func (s *contractArchiveStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (s *contractArchiveStore) DeletePrefix(
	ctx context.Context,
	prefix string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	for key := range s.objects {
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			delete(s.objects, key)
		}
	}
	s.mu.Unlock()
	return nil
}

var _ backupartifact.ArchiveStore = (*contractArchiveStore)(nil)
