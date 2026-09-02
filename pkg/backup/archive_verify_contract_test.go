package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestPublishedArchiveVerificationAuthenticatesEverySlotAndChunk(t *testing.T) {
	ctx := context.Background()
	store, want := buildPublishedArchive(t, "bk-verification")

	metadata, err := backup.LoadPublishedArchiveMetadata(ctx, store, want.ID)
	if err != nil {
		t.Fatalf("LoadPublishedArchiveMetadata(): %v", err)
	}
	if metadata.ID != want.ID || len(metadata.Slots) != backup.DefaultHashSlotCount {
		t.Fatalf("metadata = %#v", metadata)
	}
	if store.chunkOpens != 0 {
		t.Fatalf("metadata load opened %d chunks, want 0", store.chunkOpens)
	}

	verified, err := backup.VerifyPublishedArchive(ctx, store, want.ID)
	if err != nil {
		t.Fatalf("VerifyPublishedArchive(): %v", err)
	}
	if verified.ID != want.ID || store.chunkOpens != backup.DefaultHashSlotCount {
		t.Fatalf("verified ID/chunk opens = %q/%d", verified.ID, store.chunkOpens)
	}

	// Metadata discovery intentionally remains possible without reading payloads,
	// while the full verifier must notice a missing immutable chunk.
	missingKey := "backups/" + want.ID + "/slots/137/meta-000001.zst"
	delete(store.objects, missingKey)
	if _, err := backup.LoadPublishedArchiveMetadata(ctx, store, want.ID); err != nil {
		t.Fatalf("metadata load after deleting chunk: %v", err)
	}
	if _, err := backup.VerifyPublishedArchive(ctx, store, want.ID); !errors.Is(err, backup.ErrObjectNotFound) {
		t.Fatalf("VerifyPublishedArchive(missing chunk) error = %v", err)
	}
}

func TestStoredSlotVerificationRejectsReferenceAndPayloadCorruption(t *testing.T) {
	ctx := context.Background()
	store, archiveManifest := buildPublishedArchive(t, "bk-slot-corruption")
	want := archiveManifest.Slots[7]

	actual, manifest, err := backup.LoadStoredSlot(ctx, store, archiveManifest.ID, 7, false)
	if err != nil {
		t.Fatalf("LoadStoredSlot(): %v", err)
	}
	if actual != want || manifest.HashSlot != 7 {
		t.Fatalf("actual/manifest = %#v/%#v", actual, manifest)
	}

	mismatched := want
	mismatched.Records++
	if _, _, err := backup.LoadStoredSlotReference(
		ctx, store, archiveManifest.ID, mismatched, false,
	); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadStoredSlotReference(mismatch) error = %v", err)
	}

	chunkKey := "backups/" + archiveManifest.ID + "/slots/007/meta-000001.zst"
	store.reportedBytes[chunkKey]++
	if _, _, err := backup.LoadStoredSlotReference(
		ctx, store, archiveManifest.ID, want, true,
	); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadStoredSlotReference(size mismatch) error = %v", err)
	}
	store.reportedBytes[chunkKey]--

	store.objects[chunkKey][len(store.objects[chunkKey])/2] ^= 0xff
	if _, _, err := backup.LoadStoredSlotReference(
		ctx, store, archiveManifest.ID, want, true,
	); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadStoredSlotReference(corrupt chunk) error = %v", err)
	}

	if _, _, err := backup.LoadStoredSlot(ctx, nil, archiveManifest.ID, 7, false); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadStoredSlot(nil store) error = %v", err)
	}
	if _, _, err := backup.LoadStoredSlot(ctx, store, archiveManifest.ID, 256, false); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadStoredSlot(out-of-range) error = %v", err)
	}

	invalidReference := want
	invalidReference.ManifestKey = "../manifest.json"
	if _, _, err := backup.LoadStoredSlotReference(
		ctx, store, archiveManifest.ID, invalidReference, false,
	); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadStoredSlotReference(unsafe key) error = %v", err)
	}
}

func TestPublishedArchiveMetadataRejectsCorruptionMarkerAndIdentityMismatch(t *testing.T) {
	ctx := context.Background()
	store, manifest := buildPublishedArchive(t, "bk-publication")
	root := "backups/" + manifest.ID + "/"

	store.objects[root+"CORRUPT"] = []byte("operator quarantine")
	store.reportedBytes[root+"CORRUPT"] = uint64(len(store.objects[root+"CORRUPT"]))
	if _, err := backup.LoadPublishedArchiveMetadata(ctx, store, manifest.ID); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadPublishedArchiveMetadata(CORRUPT) error = %v", err)
	}
	delete(store.objects, root+"CORRUPT")
	delete(store.reportedBytes, root+"CORRUPT")

	store.openErrors[root+"CORRUPT"] = errors.New("object store unavailable")
	if _, err := backup.LoadPublishedArchiveMetadata(ctx, store, manifest.ID); err == nil || errors.Is(err, backup.ErrObjectNotFound) {
		t.Fatalf("LoadPublishedArchiveMetadata(store failure) error = %v", err)
	}
	delete(store.openErrors, root+"CORRUPT")

	other := manifest
	other.ID = "bk-other"
	body, err := backup.MarshalArchiveManifest(other)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(other): %v", err)
	}
	marker, err := backup.NewCompleteMarker(body)
	if err != nil {
		t.Fatalf("NewCompleteMarker(other): %v", err)
	}
	markerBody, err := backup.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(other): %v", err)
	}
	store.objects[root+"manifest.json"] = body
	store.reportedBytes[root+"manifest.json"] = uint64(len(body))
	store.objects[root+"COMPLETE"] = markerBody
	store.reportedBytes[root+"COMPLETE"] = uint64(len(markerBody))
	if _, err := backup.LoadPublishedArchiveMetadata(ctx, store, manifest.ID); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadPublishedArchiveMetadata(ID mismatch) error = %v", err)
	}

	if _, err := backup.LoadPublishedArchiveMetadata(ctx, nil, manifest.ID); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadPublishedArchiveMetadata(nil store) error = %v", err)
	}
}

func TestReadStoredObjectEnforcesDeclaredBoundsAndIOErrors(t *testing.T) {
	ctx := context.Background()
	readFailure := errors.New("read failed")
	closeFailure := errors.New("close failed")

	tests := []struct {
		name      string
		configure func(*memoryArchiveStore)
		maxBytes  uint64
		wantErr   error
	}{
		{
			name: "empty object",
			configure: func(store *memoryArchiveStore) {
				store.objects["object"] = nil
				store.reportedBytes["object"] = 0
			},
			maxBytes: 8,
			wantErr:  backup.ErrObjectCorrupt,
		},
		{
			name: "declared object exceeds bound",
			configure: func(store *memoryArchiveStore) {
				store.objects["object"] = []byte("123456789")
				store.reportedBytes["object"] = 9
			},
			maxBytes: 8,
			wantErr:  backup.ErrObjectCorrupt,
		},
		{
			name: "declared size differs from body",
			configure: func(store *memoryArchiveStore) {
				store.objects["object"] = []byte("abc")
				store.reportedBytes["object"] = 2
			},
			maxBytes: 8,
			wantErr:  backup.ErrObjectCorrupt,
		},
		{
			name: "read failure",
			configure: func(store *memoryArchiveStore) {
				store.objects["object"] = []byte("abc")
				store.reportedBytes["object"] = 3
				store.readErrors["object"] = readFailure
			},
			maxBytes: 8,
			wantErr:  readFailure,
		},
		{
			name: "close failure",
			configure: func(store *memoryArchiveStore) {
				store.objects["object"] = []byte("abc")
				store.reportedBytes["object"] = 3
				store.closeErrors["object"] = closeFailure
			},
			maxBytes: 8,
			wantErr:  closeFailure,
		},
		{
			name: "open failure",
			configure: func(store *memoryArchiveStore) {
				store.openErrors["object"] = backup.ErrObjectNotFound
			},
			maxBytes: 8,
			wantErr:  backup.ErrObjectNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryArchiveStore()
			test.configure(store)
			_, err := backup.ReadStoredObject(ctx, store, "object", test.maxBytes)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadStoredObject() error = %v, want %v", err, test.wantErr)
			}
		})
	}

	store := newMemoryArchiveStore()
	store.objects["object"] = []byte("abc")
	store.reportedBytes["object"] = 3
	body, err := backup.ReadStoredObject(ctx, store, "object", 3)
	if err != nil || string(body) != "abc" {
		t.Fatalf("ReadStoredObject(valid) = %q, %v", body, err)
	}
}

func buildPublishedArchive(t *testing.T, backupID string) (*memoryArchiveStore, backup.ArchiveManifest) {
	t.Helper()
	store := newMemoryArchiveStore()
	payload := []byte("portable slot metadata\n")
	var compressed bytes.Buffer
	descriptor, err := backup.EncodeChunk(&compressed, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("EncodeChunk(): %v", err)
	}

	references := make([]backup.SlotReference, backup.DefaultHashSlotCount)
	for slot := range references {
		relativeChunkKey := fmt.Sprintf("slots/%03d/meta-000001.zst", slot)
		slotManifest := backup.SlotManifest{
			Format:   backup.SlotManifestFormat,
			Version:  backup.SlotManifestVersion,
			HashSlot: uint16(slot),
			Cut: backup.SlotCut{
				PhysicalSlotID:       uint32(slot + 1),
				LeaderTerm:           3,
				AppliedTerm:          3,
				ConfigurationVersion: 4,
				AppliedIndex:         9,
				CapturedAtUnixMillis: 1_800_000_000_100,
			},
			Chunks: []backup.ChunkReference{{
				Kind:       backup.ChunkKindMetadata,
				Sequence:   1,
				Stream:     0,
				Part:       1,
				Final:      true,
				Key:        relativeChunkKey,
				Descriptor: descriptor,
				Records:    1,
			}},
			LogicalBytes: descriptor.LogicalBytes,
			StoredBytes:  descriptor.StoredBytes,
			Records:      1,
		}
		slotBody, marshalErr := backup.MarshalSlotManifest(slotManifest)
		if marshalErr != nil {
			t.Fatalf("MarshalSlotManifest(%d): %v", slot, marshalErr)
		}
		relativeManifestKey := fmt.Sprintf("slots/%03d/manifest.json", slot)
		manifestKey := "backups/" + backupID + "/" + relativeManifestKey
		chunkKey := "backups/" + backupID + "/" + relativeChunkKey
		store.add(manifestKey, slotBody)
		store.add(chunkKey, compressed.Bytes())
		sum := sha256.Sum256(slotBody)
		references[slot] = backup.SlotReference{
			HashSlot:       uint16(slot),
			ManifestKey:    relativeManifestKey,
			ManifestSHA256: hex.EncodeToString(sum[:]),
			LogicalBytes:   descriptor.LogicalBytes,
			StoredBytes:    descriptor.StoredBytes,
			Records:        1,
		}
	}

	manifest := backup.ArchiveManifest{
		Format:                backup.ArchiveFormat,
		Version:               backup.ArchiveVersion,
		ID:                    backupID,
		Trigger:               backup.TriggerManual,
		SourceClusterID:       "cluster-verification",
		SourceApplication:     "wukongim-test",
		HashSlotCount:         backup.DefaultHashSlotCount,
		StartedAtUnixMillis:   1_800_000_000_000,
		CompletedAtUnixMillis: 1_800_000_000_300,
		CutStartedUnixMillis:  1_800_000_000_050,
		CutEndedUnixMillis:    1_800_000_000_200,
		Compression:           backup.CompressionZstd,
		Checksum:              backup.ChecksumSHA256,
		LogicalBytes:          descriptor.LogicalBytes * backup.DefaultHashSlotCount,
		StoredBytes:           descriptor.StoredBytes * backup.DefaultHashSlotCount,
		Records:               backup.DefaultHashSlotCount,
		Slots:                 references,
	}
	manifestBody, err := backup.MarshalArchiveManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	marker, err := backup.NewCompleteMarker(manifestBody)
	if err != nil {
		t.Fatalf("NewCompleteMarker(): %v", err)
	}
	markerBody, err := backup.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(): %v", err)
	}
	root := "backups/" + backupID + "/"
	store.add(root+"manifest.json", manifestBody)
	store.add(root+"COMPLETE", markerBody)
	return store, manifest
}

type memoryArchiveStore struct {
	objects       map[string][]byte
	reportedBytes map[string]uint64
	openErrors    map[string]error
	readErrors    map[string]error
	closeErrors   map[string]error
	putError      error
	putCollision  []byte
	chunkOpens    int
}

func newMemoryArchiveStore() *memoryArchiveStore {
	return &memoryArchiveStore{
		objects:       make(map[string][]byte),
		reportedBytes: make(map[string]uint64),
		openErrors:    make(map[string]error),
		readErrors:    make(map[string]error),
		closeErrors:   make(map[string]error),
	}
}

func (s *memoryArchiveStore) add(key string, body []byte) {
	s.objects[key] = bytes.Clone(body)
	s.reportedBytes[key] = uint64(len(body))
}

func (s *memoryArchiveStore) Put(_ context.Context, object backup.PutObject) error {
	if s.putError != nil {
		err := s.putError
		s.putError = nil
		if errors.Is(err, backup.ErrObjectExists) && s.putCollision != nil {
			s.add(object.Key, s.putCollision)
		}
		return err
	}
	if object.IfAbsent {
		if _, exists := s.objects[object.Key]; exists {
			return backup.ErrObjectExists
		}
	}
	body, err := io.ReadAll(object.Body)
	if err != nil {
		return err
	}
	if uint64(len(body)) != object.ExpectedBytes {
		return backup.ErrInvalidObject
	}
	s.add(object.Key, body)
	return nil
}

func (s *memoryArchiveStore) Open(_ context.Context, key string) (io.ReadCloser, backup.ArchiveObject, error) {
	if err := s.openErrors[key]; err != nil {
		return nil, backup.ArchiveObject{}, err
	}
	body, ok := s.objects[key]
	if !ok {
		return nil, backup.ArchiveObject{}, backup.ErrObjectNotFound
	}
	if len(key) >= 4 && key[len(key)-4:] == ".zst" {
		s.chunkOpens++
	}
	return &faultReadCloser{
			Reader:   bytes.NewReader(body),
			readErr:  s.readErrors[key],
			closeErr: s.closeErrors[key],
		}, backup.ArchiveObject{
			Key:      key,
			Bytes:    s.reportedBytes[key],
			Modified: time.Unix(1_800_000_000, 0).UTC(),
		}, nil
}

func (s *memoryArchiveStore) List(_ context.Context, _ string) ([]backup.ArchiveObject, error) {
	return nil, nil
}

func (s *memoryArchiveStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	delete(s.reportedBytes, key)
	return nil
}

func (s *memoryArchiveStore) DeletePrefix(_ context.Context, _ string) error {
	return nil
}

type faultReadCloser struct {
	*bytes.Reader
	readErr  error
	closeErr error
	failed   bool
}

func (r *faultReadCloser) Read(body []byte) (int, error) {
	if r.readErr != nil && !r.failed {
		r.failed = true
		return 0, r.readErr
	}
	return r.Reader.Read(body)
}

func (r *faultReadCloser) Close() error {
	return r.closeErr
}
