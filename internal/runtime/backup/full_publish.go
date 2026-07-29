package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxArchiveManifestBytes = 4 << 20

// PublishArchiveRequest identifies one completed Controller job.
type PublishArchiveRequest struct {
	ID                  string
	Trigger             backupartifact.Trigger
	SourceClusterID     string
	SourceApplication   string
	StartedUnixMillis   int64
	CompletedUnixMillis int64
	Slots               []backupartifact.SlotReference
}

// EnsureRepository binds an empty repository to the cluster or validates its
// existing lineage marker.
func EnsureRepository(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	clusterID string,
	nowUnixMillis int64,
) (backupartifact.RepositoryMarker, error) {
	return backupartifact.EnsureRepository(
		ctx, store, clusterID, nowUnixMillis,
	)
}

// PublishArchive verifies all 256 Slot artifacts, writes the top-level
// manifest, verifies it again through the store, then publishes COMPLETE.
func PublishArchive(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	request PublishArchiveRequest,
) (backupartifact.ArchiveManifest, error) {
	if store == nil {
		return backupartifact.ArchiveManifest{}, fmt.Errorf("backup runtime: archive store is required")
	}
	root := "backups/" + request.ID + "/"
	existingMarker, markerErr := backupartifact.ReadStoredObject(
		ctx, store, root+"COMPLETE", maxArchiveManifestBytes,
	)
	if markerErr == nil {
		manifest, err := backupartifact.VerifyPublishedArchive(ctx, store, request.ID)
		if err != nil {
			return backupartifact.ArchiveManifest{}, err
		}
		if manifest.Trigger != request.Trigger ||
			manifest.SourceClusterID != request.SourceClusterID ||
			manifest.SourceApplication != request.SourceApplication ||
			manifest.StartedAtUnixMillis != request.StartedUnixMillis ||
			manifest.CompletedAtUnixMillis != request.CompletedUnixMillis {
			return backupartifact.ArchiveManifest{},
				fmt.Errorf("backup runtime: published archive identity changed")
		}
		if err := publishCatalogEntry(ctx, store, request.ID, existingMarker); err != nil {
			return backupartifact.ArchiveManifest{}, err
		}
		return manifest, nil
	}
	if !errors.Is(markerErr, backupartifact.ErrObjectNotFound) {
		return backupartifact.ArchiveManifest{}, markerErr
	}
	if _, err := EnsureRepository(
		ctx, store, request.SourceClusterID, request.CompletedUnixMillis,
	); err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	manifest := backupartifact.ArchiveManifest{
		Format:                backupartifact.ArchiveFormat,
		Version:               backupartifact.ArchiveVersion,
		ID:                    request.ID,
		Trigger:               request.Trigger,
		SourceClusterID:       request.SourceClusterID,
		SourceApplication:     request.SourceApplication,
		HashSlotCount:         backupartifact.DefaultHashSlotCount,
		StartedAtUnixMillis:   request.StartedUnixMillis,
		CompletedAtUnixMillis: request.CompletedUnixMillis,
		Compression:           backupartifact.CompressionZstd,
		Checksum:              backupartifact.ChecksumSHA256,
		Slots:                 make([]backupartifact.SlotReference, backupartifact.DefaultHashSlotCount),
	}
	if len(request.Slots) != backupartifact.DefaultHashSlotCount {
		return backupartifact.ArchiveManifest{},
			fmt.Errorf("backup runtime: incomplete Slot references")
	}
	for hashSlot := 0; hashSlot < backupartifact.DefaultHashSlotCount; hashSlot++ {
		expected := request.Slots[hashSlot]
		if expected.HashSlot != uint16(hashSlot) {
			return backupartifact.ArchiveManifest{},
				fmt.Errorf("backup runtime: unordered Slot references")
		}
		reference, slotManifest, err := backupartifact.LoadStoredSlotReference(
			ctx, store, request.ID, expected, true,
		)
		if err != nil {
			return backupartifact.ArchiveManifest{}, err
		}
		manifest.Slots[hashSlot] = reference
		manifest.LogicalBytes += reference.LogicalBytes
		manifest.StoredBytes += reference.StoredBytes
		manifest.Records += reference.Records
		if reference.MaxMessageID > manifest.MaxMessageID {
			manifest.MaxMessageID = reference.MaxMessageID
		}
		if manifest.CutStartedUnixMillis == 0 ||
			slotManifest.Cut.CapturedAtUnixMillis < manifest.CutStartedUnixMillis {
			manifest.CutStartedUnixMillis = slotManifest.Cut.CapturedAtUnixMillis
		}
		if slotManifest.Cut.CapturedAtUnixMillis > manifest.CutEndedUnixMillis {
			manifest.CutEndedUnixMillis = slotManifest.Cut.CapturedAtUnixMillis
		}
	}
	body, err := backupartifact.MarshalArchiveManifest(manifest)
	if err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	if err := putImmutableObject(
		ctx, store, root+"manifest.json", body,
	); err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	loadedBody, err := backupartifact.ReadStoredObject(
		ctx, store, root+"manifest.json", maxArchiveManifestBytes,
	)
	if err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	if _, err := backupartifact.LoadArchiveManifest(loadedBody); err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	marker, err := backupartifact.NewCompleteMarker(loadedBody)
	if err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	markerBody, err := backupartifact.MarshalCompleteMarker(marker)
	if err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	if err := putImmutableObject(
		ctx, store, root+"COMPLETE", markerBody,
	); err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	if err := publishCatalogEntry(ctx, store, request.ID, markerBody); err != nil {
		return backupartifact.ArchiveManifest{}, err
	}
	return manifest, nil
}

func putImmutableObject(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	key string,
	body []byte,
) error {
	err := store.Put(ctx, backupartifact.PutObject{
		Key: key, Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	})
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		return err
	}
	existing, readErr := backupartifact.ReadStoredObject(
		ctx, store, key, maxArchiveManifestBytes,
	)
	if readErr != nil {
		return readErr
	}
	if !bytes.Equal(existing, body) {
		return fmt.Errorf("backup runtime: immutable object %q changed", key)
	}
	return nil
}

func publishCatalogEntry(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	backupID string,
	markerBody []byte,
) error {
	err := store.Put(ctx, backupartifact.PutObject{
		Key:           "catalog/" + backupID,
		Body:          bytes.NewReader(markerBody),
		ExpectedBytes: uint64(len(markerBody)),
		IfAbsent:      true,
	})
	if errors.Is(err, backupartifact.ErrObjectExists) {
		existing, readErr := backupartifact.ReadStoredObject(
			ctx, store, "catalog/"+backupID, maxArchiveManifestBytes,
		)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, markerBody) {
			return fmt.Errorf("backup runtime: catalog entry identity changed")
		}
		return nil
	}
	return err
}

// VerifyPublishedArchive rechecks the COMPLETE binding, all manifests, and
// every compressed chunk without mutating repository state.
func VerifyPublishedArchive(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	backupID string,
) (backupartifact.ArchiveManifest, error) {
	return backupartifact.VerifyPublishedArchive(ctx, store, backupID)
}
