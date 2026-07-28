package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	// RepositoryMarkerKey is the fixed identity object at the archive repository root.
	RepositoryMarkerKey = "repository.json"
	// RepositoryFormat identifies a scheduled full-backup repository.
	RepositoryFormat = "wukongim-backup-repository"
	// RepositoryVersion is the only repository schema supported by this binary.
	RepositoryVersion uint32 = 1
)

// RepositoryMarker binds one repository prefix to exactly one cluster lineage.
type RepositoryMarker struct {
	Format              string `json:"format"`
	Version             uint32 `json:"version"`
	SourceClusterID     string `json:"source_cluster_id"`
	HashSlotCount       int    `json:"hash_slot_count"`
	CreatedAtUnixMillis int64  `json:"created_at_unix_ms"`
}

// MarshalRepositoryMarker validates and encodes canonical repository identity JSON.
func MarshalRepositoryMarker(marker RepositoryMarker) ([]byte, error) {
	if err := validateRepositoryMarker(marker); err != nil {
		return nil, err
	}
	body, err := json.Marshal(marker)
	if err != nil {
		return nil, fmt.Errorf("%w: encode repository marker: %v", ErrInvalidManifest, err)
	}
	return body, nil
}

// LoadRepositoryMarker strictly decodes canonical repository identity JSON.
func LoadRepositoryMarker(body []byte) (RepositoryMarker, error) {
	var marker RepositoryMarker
	if err := decodeStrictJSON(body, &marker); err != nil {
		return RepositoryMarker{}, fmt.Errorf("%w: decode repository marker: %v", ErrInvalidManifest, err)
	}
	if err := validateRepositoryMarker(marker); err != nil {
		return RepositoryMarker{}, err
	}
	canonical, err := json.Marshal(marker)
	if err != nil || !bytes.Equal(canonical, body) {
		return RepositoryMarker{}, fmt.Errorf("%w: repository marker is not canonical", ErrInvalidManifest)
	}
	return marker, nil
}

// EnsureRepository binds an empty repository to clusterID or validates its
// existing lineage marker.
func EnsureRepository(
	ctx context.Context,
	store ArchiveStore,
	clusterID string,
	nowUnixMillis int64,
) (RepositoryMarker, error) {
	if store == nil || clusterID == "" || nowUnixMillis <= 0 {
		return RepositoryMarker{}, fmt.Errorf("%w: repository identity is incomplete", ErrInvalidObject)
	}
	reader, object, err := store.Open(ctx, RepositoryMarkerKey)
	if err == nil {
		if object.Bytes == 0 || object.Bytes > 64<<10 {
			_ = reader.Close()
			return RepositoryMarker{}, fmt.Errorf("%w: repository marker size", ErrObjectCorrupt)
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return RepositoryMarker{}, errors.Join(readErr, closeErr)
		}
		if uint64(len(body)) != object.Bytes {
			return RepositoryMarker{}, fmt.Errorf("%w: repository marker size mismatch", ErrObjectCorrupt)
		}
		marker, loadErr := LoadRepositoryMarker(body)
		if loadErr != nil {
			return RepositoryMarker{}, loadErr
		}
		if marker.SourceClusterID != clusterID {
			return RepositoryMarker{}, fmt.Errorf(
				"%w: repository belongs to cluster %q",
				ErrRepositoryIncomplete, marker.SourceClusterID,
			)
		}
		return marker, nil
	}
	if !errors.Is(err, ErrObjectNotFound) {
		return RepositoryMarker{}, err
	}
	marker := RepositoryMarker{
		Format: RepositoryFormat, Version: RepositoryVersion,
		SourceClusterID: clusterID, HashSlotCount: DefaultHashSlotCount,
		CreatedAtUnixMillis: nowUnixMillis,
	}
	body, err := MarshalRepositoryMarker(marker)
	if err != nil {
		return RepositoryMarker{}, err
	}
	err = store.Put(ctx, PutObject{
		Key: RepositoryMarkerKey, Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	})
	if errors.Is(err, ErrObjectExists) {
		return EnsureRepository(ctx, store, clusterID, nowUnixMillis)
	}
	return marker, err
}

func validateRepositoryMarker(marker RepositoryMarker) error {
	if marker.Format != RepositoryFormat || marker.Version != RepositoryVersion ||
		marker.HashSlotCount != DefaultHashSlotCount ||
		marker.CreatedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: repository marker", ErrInvalidManifest)
	}
	if err := validateBackupIdentity(marker.SourceClusterID); err != nil {
		return fmt.Errorf("%w: source cluster id: %v", ErrInvalidManifest, err)
	}
	return nil
}
