package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const maxStoredManifestBytes = MaxSlotManifestBytes

// LoadStoredSlot validates one stored Slot manifest and optionally every
// compressed chunk, returning the exact top-level reference it represents.
func LoadStoredSlot(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
	hashSlot uint16,
	verifyChunks bool,
) (SlotReference, SlotManifest, error) {
	if store == nil || int(hashSlot) >= DefaultHashSlotCount {
		return SlotReference{}, SlotManifest{}, ErrInvalidObject
	}
	relativeManifestKey := fmt.Sprintf("slots/%03d/manifest.json", hashSlot)
	return loadStoredSlotAtKey(
		ctx, store, backupID, hashSlot, relativeManifestKey, verifyChunks,
	)
}

// LoadStoredSlotReference verifies the exact immutable attempt manifest
// recorded in durable Controller progress.
func LoadStoredSlotReference(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
	expected SlotReference,
	verifyChunks bool,
) (SlotReference, SlotManifest, error) {
	if store == nil || int(expected.HashSlot) >= DefaultHashSlotCount ||
		validateSlotManifestKey(expected.HashSlot, expected.ManifestKey) != nil ||
		validateSHA256(expected.ManifestSHA256) != nil {
		return SlotReference{}, SlotManifest{}, ErrInvalidObject
	}
	actual, manifest, err := loadStoredSlotAtKey(
		ctx, store, backupID, expected.HashSlot,
		expected.ManifestKey, verifyChunks,
	)
	if err != nil {
		return SlotReference{}, SlotManifest{}, err
	}
	if actual != expected {
		return SlotReference{}, SlotManifest{},
			fmt.Errorf("%w: Slot reference mismatch", ErrObjectCorrupt)
	}
	return actual, manifest, nil
}

func loadStoredSlotAtKey(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
	hashSlot uint16,
	relativeManifestKey string,
	verifyChunks bool,
) (SlotReference, SlotManifest, error) {
	body, err := ReadStoredObject(
		ctx, store, "backups/"+backupID+"/"+relativeManifestKey,
		maxStoredManifestBytes,
	)
	if err != nil {
		return SlotReference{}, SlotManifest{}, err
	}
	manifest, err := LoadSlotManifest(body)
	if err != nil {
		return SlotReference{}, SlotManifest{}, err
	}
	if manifest.HashSlot != hashSlot {
		return SlotReference{}, SlotManifest{},
			fmt.Errorf("%w: Hash Slot manifest mismatch", ErrObjectCorrupt)
	}
	if verifyChunks {
		for _, chunk := range manifest.Chunks {
			reader, object, err := store.Open(ctx, "backups/"+backupID+"/"+chunk.Key)
			if err != nil {
				return SlotReference{}, SlotManifest{}, err
			}
			if object.Bytes != chunk.Descriptor.StoredBytes {
				_ = reader.Close()
				return SlotReference{}, SlotManifest{},
					fmt.Errorf("%w: stored chunk size", ErrObjectCorrupt)
			}
			decodeErr := DecodeChunk(io.Discard, reader, chunk.Descriptor)
			closeErr := reader.Close()
			if decodeErr != nil || closeErr != nil {
				return SlotReference{}, SlotManifest{},
					errors.Join(decodeErr, closeErr)
			}
		}
	}
	sum := sha256.Sum256(body)
	return SlotReference{
		HashSlot:       hashSlot,
		ManifestKey:    relativeManifestKey,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		LogicalBytes:   manifest.LogicalBytes,
		StoredBytes:    manifest.StoredBytes,
		Records:        manifest.Records,
		MaxMessageID:   manifest.MaxMessageID,
	}, manifest, nil
}

// LoadPublishedArchiveMetadata validates the corruption marker, COMPLETE
// binding, and bounded top-level manifest without scanning Slot payloads.
func LoadPublishedArchiveMetadata(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
) (ArchiveManifest, error) {
	if store == nil || backupID == "" {
		return ArchiveManifest{}, ErrInvalidObject
	}
	root := "backups/" + backupID + "/"
	corrupt, _, corruptErr := store.Open(ctx, root+"CORRUPT")
	if corruptErr == nil {
		closeErr := corrupt.Close()
		return ArchiveManifest{}, errors.Join(
			fmt.Errorf("%w: archive is marked corrupt", ErrObjectCorrupt),
			closeErr,
		)
	}
	if !errors.Is(corruptErr, ErrObjectNotFound) {
		return ArchiveManifest{}, corruptErr
	}
	manifestBody, err := ReadStoredObject(
		ctx, store, root+"manifest.json", maxStoredManifestBytes,
	)
	if err != nil {
		return ArchiveManifest{}, err
	}
	markerBody, err := ReadStoredObject(
		ctx, store, root+"COMPLETE", maxStoredManifestBytes,
	)
	if err != nil {
		return ArchiveManifest{}, err
	}
	if _, err := LoadCompleteMarker(markerBody, manifestBody); err != nil {
		return ArchiveManifest{}, err
	}
	manifest, err := LoadArchiveManifest(manifestBody)
	if err != nil {
		return ArchiveManifest{}, err
	}
	if manifest.ID != backupID {
		return ArchiveManifest{}, fmt.Errorf("%w: archive ID mismatch", ErrObjectCorrupt)
	}
	return manifest, nil
}

// VerifyPublishedArchive verifies COMPLETE, the top-level manifest, every Slot
// reference, and every compressed chunk.
func VerifyPublishedArchive(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
) (ArchiveManifest, error) {
	manifest, err := LoadPublishedArchiveMetadata(ctx, store, backupID)
	if err != nil {
		return ArchiveManifest{}, err
	}
	for hashSlot, expected := range manifest.Slots {
		actual, _, err := LoadStoredSlotReference(
			ctx, store, backupID, expected, true,
		)
		if err != nil {
			return ArchiveManifest{}, err
		}
		if actual.HashSlot != uint16(hashSlot) {
			return ArchiveManifest{}, fmt.Errorf("%w: Slot reference mismatch", ErrObjectCorrupt)
		}
	}
	return manifest, nil
}

// ReadStoredObject reads one exact bounded repository object.
func ReadStoredObject(
	ctx context.Context,
	store ArchiveStore,
	key string,
	maxBytes uint64,
) ([]byte, error) {
	reader, object, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	if object.Bytes == 0 || object.Bytes > maxBytes {
		_ = reader.Close()
		return nil, fmt.Errorf("%w: object size", ErrObjectCorrupt)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if uint64(len(body)) != object.Bytes || uint64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: object size mismatch", ErrObjectCorrupt)
	}
	return body, nil
}
