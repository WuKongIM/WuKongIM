package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxCatalogObjectBytes = 4 << 20

// ArchiveHealth is the Manager-facing integrity state of one published archive.
type ArchiveHealth string

const (
	ArchiveHealthHealthy ArchiveHealth = "healthy"
	ArchiveHealthCorrupt ArchiveHealth = "corrupt"
)

// ArchiveSummary is one independent restorable full backup.
type ArchiveSummary struct {
	ID                    string        `json:"id"`
	Trigger               string        `json:"trigger"`
	SourceClusterID       string        `json:"source_cluster_id"`
	StartedAtUnixMillis   int64         `json:"started_at_unix_ms"`
	CompletedAtUnixMillis int64         `json:"completed_at_unix_ms"`
	LogicalBytes          uint64        `json:"logical_bytes"`
	StoredBytes           uint64        `json:"stored_bytes"`
	Records               uint64        `json:"records"`
	MaxMessageID          string        `json:"max_message_id"`
	Held                  bool          `json:"held"`
	HoldNote              string        `json:"hold_note,omitempty"`
	Health                ArchiveHealth `json:"health"`
	ErrorCode             string        `json:"error_code,omitempty"`
}

// ArchiveDetail returns the top-level manifest with its operator projection.
type ArchiveDetail struct {
	Archive  ArchiveSummary                 `json:"archive"`
	Manifest backupartifact.ArchiveManifest `json:"manifest"`
}

type archiveHold struct {
	HeldAtUnixMillis int64  `json:"held_at_unix_ms"`
	Note             string `json:"note,omitempty"`
}

type archiveCorrupt struct {
	MarkedAtUnixMillis int64  `json:"marked_at_unix_ms"`
	ErrorCode          string `json:"error_code"`
}

// ListArchives returns only archives made visible by a valid COMPLETE marker.
// Corrupt published metadata remains visible and clearly marked.
func ListArchives(
	ctx context.Context,
	store backupartifact.ArchiveStore,
) ([]ArchiveSummary, error) {
	if store == nil {
		return nil, ErrInvalidRequest
	}
	objects, err := store.List(ctx, "catalog")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for _, object := range objects {
		if !strings.HasPrefix(object.Key, "catalog/") {
			continue
		}
		id := strings.TrimPrefix(object.Key, "catalog/")
		if id == "" || strings.Contains(id, "/") {
			continue
		}
		ids = append(ids, id)
	}
	archives := make([]ArchiveSummary, 0, len(ids))
	for _, id := range ids {
		summary, _, loadErr := loadArchiveMetadata(ctx, store, id)
		if loadErr != nil {
			if !IsArchiveIntegrityFailure(loadErr) {
				return nil, loadErr
			}
			archives = append(archives, ArchiveSummary{
				ID: id, Health: ArchiveHealthCorrupt, ErrorCode: "archive_metadata_corrupt",
			})
			continue
		}
		archives = append(archives, summary)
	}
	sort.Slice(archives, func(i, j int) bool {
		if archives[i].CompletedAtUnixMillis == archives[j].CompletedAtUnixMillis {
			return archives[i].ID > archives[j].ID
		}
		return archives[i].CompletedAtUnixMillis > archives[j].CompletedAtUnixMillis
	})
	return archives, nil
}

// ArchiveByID loads one published archive without reading every chunk.
func ArchiveByID(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
) (ArchiveDetail, error) {
	summary, manifest, err := loadArchiveMetadata(ctx, store, archiveID)
	if err != nil {
		return ArchiveDetail{}, err
	}
	return ArchiveDetail{Archive: summary, Manifest: manifest}, nil
}

// VerifyArchive fully verifies every compressed chunk and returns its detail.
func VerifyArchive(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
) (ArchiveDetail, error) {
	if _, err := backupartifact.VerifyPublishedArchive(ctx, store, archiveID); err != nil {
		return ArchiveDetail{}, err
	}
	return ArchiveByID(ctx, store, archiveID)
}

// MarkArchiveCorrupt durably quarantines one published archive after an exact
// integrity failure.
func MarkArchiveCorrupt(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
	now time.Time,
) error {
	if archiveID == "" || strings.Contains(archiveID, "/") || now.IsZero() {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(archiveCorrupt{
		MarkedAtUnixMillis: now.UTC().UnixMilli(),
		ErrorCode:          "integrity_verification_failed",
	})
	if err != nil {
		return err
	}
	return store.Put(ctx, backupartifact.PutObject{
		Key:  "backups/" + archiveID + "/CORRUPT",
		Body: strings.NewReader(string(body)), ExpectedBytes: uint64(len(body)),
	})
}

// IsArchiveIntegrityFailure separates durable artifact damage from transient
// repository transport errors.
func IsArchiveIntegrityFailure(err error) bool {
	return errors.Is(err, backupartifact.ErrInvalidManifest) ||
		errors.Is(err, backupartifact.ErrUnsupportedVersion) ||
		errors.Is(err, backupartifact.ErrInvalidObject) ||
		errors.Is(err, backupartifact.ErrObjectCorrupt) ||
		errors.Is(err, backupartifact.ErrObjectNotFound) ||
		errors.Is(err, backupartifact.ErrRepositoryIncomplete)
}

// SetArchiveHold creates or removes the bounded retention hold marker.
func SetArchiveHold(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
	held bool,
	note string,
	now time.Time,
) (ArchiveSummary, error) {
	if strings.TrimSpace(archiveID) == "" || len(note) > 256 || now.IsZero() {
		return ArchiveSummary{}, ErrInvalidRequest
	}
	if _, _, err := loadArchiveMetadata(ctx, store, archiveID); err != nil {
		return ArchiveSummary{}, err
	}
	key := "backups/" + archiveID + "/HOLD"
	if held {
		body, err := json.Marshal(archiveHold{
			HeldAtUnixMillis: now.UTC().UnixMilli(),
			Note:             strings.TrimSpace(note),
		})
		if err != nil {
			return ArchiveSummary{}, err
		}
		if err := store.Put(ctx, backupartifact.PutObject{
			Key: key, Body: strings.NewReader(string(body)),
			ExpectedBytes: uint64(len(body)),
		}); err != nil {
			return ArchiveSummary{}, err
		}
	} else if err := store.Delete(ctx, key); err != nil {
		return ArchiveSummary{}, err
	}
	summary, _, err := loadArchiveMetadata(ctx, store, archiveID)
	return summary, err
}

// DeleteArchive removes exactly one published archive unless it is held.
func DeleteArchive(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
) error {
	summary, _, err := loadArchiveMetadata(ctx, store, archiveID)
	if err != nil {
		return err
	}
	if summary.Held {
		return ErrArchiveHeld
	}
	if err := store.Delete(ctx, "catalog/"+archiveID); err != nil {
		return err
	}
	return store.DeletePrefix(ctx, "backups/"+archiveID)
}

// ApplyRetention keeps the newest successful archives plus every held archive.
func ApplyRetention(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	retentionCount int,
) ([]string, error) {
	if retentionCount < 1 || retentionCount > 1000 {
		return nil, ErrInvalidRequest
	}
	archives, err := ListArchives(ctx, store)
	if err != nil {
		return nil, err
	}
	kept := 0
	deleted := make([]string, 0)
	for _, archive := range archives {
		if archive.Health != ArchiveHealthHealthy || archive.Held {
			continue
		}
		kept++
		if kept <= retentionCount {
			continue
		}
		if err := store.Delete(ctx, "catalog/"+archive.ID); err != nil {
			return deleted, err
		}
		if err := store.DeletePrefix(ctx, "backups/"+archive.ID); err != nil {
			return deleted, err
		}
		deleted = append(deleted, archive.ID)
	}
	return deleted, nil
}

func loadArchiveMetadata(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	archiveID string,
) (ArchiveSummary, backupartifact.ArchiveManifest, error) {
	if archiveID == "" || strings.Contains(archiveID, "/") {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, ErrInvalidRequest
	}
	root := "backups/" + archiveID + "/"
	manifestBody, err := readCatalogObject(ctx, store, root+"manifest.json")
	if err != nil {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, err
	}
	markerBody, err := readCatalogObject(ctx, store, root+"COMPLETE")
	if err != nil {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, err
	}
	if _, err := backupartifact.LoadCompleteMarker(markerBody, manifestBody); err != nil {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, err
	}
	manifest, err := backupartifact.LoadArchiveManifest(manifestBody)
	if err != nil {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, err
	}
	if manifest.ID != archiveID {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{},
			fmt.Errorf("%w: archive ID mismatch", backupartifact.ErrObjectCorrupt)
	}
	summary := ArchiveSummary{
		ID: archiveID, Trigger: string(manifest.Trigger),
		SourceClusterID:       manifest.SourceClusterID,
		StartedAtUnixMillis:   manifest.StartedAtUnixMillis,
		CompletedAtUnixMillis: manifest.CompletedAtUnixMillis,
		LogicalBytes:          manifest.LogicalBytes, StoredBytes: manifest.StoredBytes,
		Records:      manifest.Records,
		MaxMessageID: strconv.FormatUint(manifest.MaxMessageID, 10),
		Health:       ArchiveHealthHealthy,
	}
	if _, corruptErr := readCatalogObject(ctx, store, root+"CORRUPT"); corruptErr == nil {
		summary.Health = ArchiveHealthCorrupt
		summary.ErrorCode = "integrity_verification_failed"
	} else if !errors.Is(corruptErr, backupartifact.ErrObjectNotFound) {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, corruptErr
	}
	holdBody, holdErr := readCatalogObject(ctx, store, root+"HOLD")
	if holdErr == nil {
		var hold archiveHold
		if err := json.Unmarshal(holdBody, &hold); err != nil ||
			hold.HeldAtUnixMillis <= 0 || len(hold.Note) > 256 {
			return ArchiveSummary{}, backupartifact.ArchiveManifest{},
				fmt.Errorf("%w: invalid hold marker", backupartifact.ErrObjectCorrupt)
		}
		summary.Held = true
		summary.HoldNote = hold.Note
	} else if !errors.Is(holdErr, backupartifact.ErrObjectNotFound) {
		return ArchiveSummary{}, backupartifact.ArchiveManifest{}, holdErr
	}
	return summary, manifest, nil
}

func readCatalogObject(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	key string,
) ([]byte, error) {
	reader, object, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	if object.Bytes == 0 || object.Bytes > maxCatalogObjectBytes {
		_ = reader.Close()
		return nil, fmt.Errorf("%w: catalog object size", backupartifact.ErrObjectCorrupt)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxCatalogObjectBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if uint64(len(body)) != object.Bytes || len(body) > maxCatalogObjectBytes {
		return nil, fmt.Errorf("%w: catalog object size mismatch", backupartifact.ErrObjectCorrupt)
	}
	return body, nil
}
