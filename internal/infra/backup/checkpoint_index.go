package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	checkpointIndexFormat   = "wukongim-backup-checkpoint-index"
	checkpointIndexVersion  = 2
	maxCheckpointIndexBytes = 256 << 20
)

// CheckpointCatalogIndex is a rebuildable local acceleration index over the
// signed immutable catalog. It is never publication or restore authority.
type CheckpointCatalogIndex struct {
	catalog *ReplicatedCheckpointCatalog
	path    string

	mu      sync.Mutex
	loaded  bool
	head    backupartifact.CatalogPageReference
	entries []backupartifact.CatalogCheckpointReference
	byID    map[string]backupartifact.CatalogCheckpointReference
}

type checkpointIndexSnapshot struct {
	Format  string                                      `json:"format"`
	Version uint16                                      `json:"version"`
	Head    backupartifact.CatalogPageReference         `json:"head"`
	Entries []backupartifact.CatalogCheckpointReference `json:"entries"`
}

type checkpointIndexEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

type checkpointCursor struct {
	Version uint16 `json:"version"`
	ID      string `json:"id"`
}

// NewCheckpointCatalogIndex creates a disk-backed derived catalog index.
func NewCheckpointCatalogIndex(catalog *ReplicatedCheckpointCatalog, path string) (*CheckpointCatalogIndex, error) {
	path = strings.TrimSpace(path)
	if catalog == nil || path == "" {
		return nil, fmt.Errorf("backup checkpoint index: catalog and path are required")
	}
	return &CheckpointCatalogIndex{catalog: catalog, path: filepath.Clean(path)}, nil
}

// List returns a stable newest-first keyset page. A damaged local index is
// discarded and rebuilt from the signed dual-repository catalog.
func (i *CheckpointCatalogIndex) List(
	ctx context.Context,
	head backupartifact.CatalogPageReference,
	request backupusecase.CheckpointListRequest,
) (backupusecase.CheckpointPage, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ensure(ctx, head); err != nil {
		return backupusecase.CheckpointPage{}, err
	}
	filtered := i.entries
	if query := strings.ToLower(strings.TrimSpace(request.IDQuery)); query != "" {
		filtered = make([]backupartifact.CatalogCheckpointReference, 0)
		for _, entry := range i.entries {
			if strings.Contains(strings.ToLower(entry.ID), query) {
				filtered = append(filtered, entry)
			}
		}
	}
	start, err := checkpointPageStart(filtered, request.Cursor)
	if err != nil {
		return backupusecase.CheckpointPage{}, err
	}
	limit := request.Limit
	if limit <= 0 || limit > backupusecase.MaxCheckpointPageSize {
		return backupusecase.CheckpointPage{}, backupusecase.ErrInvalidRequest
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := backupusecase.CheckpointPage{
		Items: make([]backupusecase.CheckpointSummary, end-start),
		Total: len(filtered),
	}
	for index, entry := range filtered[start:end] {
		page.Items[index] = backupusecase.CheckpointSummary{
			ID: entry.ID, CreatedAtUnixMillis: entry.CreatedAtUnixMillis,
			EffectiveAtUnixMillis: entry.EffectiveAtUnixMillis, Held: entry.Held,
		}
	}
	if end < len(filtered) && len(page.Items) > 0 {
		page.NextCursor, err = encodeCheckpointCursor(page.Items[len(page.Items)-1].ID)
		if err != nil {
			return backupusecase.CheckpointPage{}, err
		}
	}
	return page, nil
}

// Get authenticates and returns one exact checkpoint through the derived ID index.
func (i *CheckpointCatalogIndex) Get(
	ctx context.Context,
	head backupartifact.CatalogPageReference,
	checkpointID string,
) (backupusecase.CheckpointDetail, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ensure(ctx, head); err != nil {
		return backupusecase.CheckpointDetail{}, err
	}
	reference, found := i.byID[checkpointID]
	if !found {
		return backupusecase.CheckpointDetail{}, backupusecase.ErrCheckpointNotFound
	}
	checkpoint, err := i.catalog.LoadCheckpoint(ctx, reference)
	if err != nil {
		return backupusecase.CheckpointDetail{}, err
	}
	return backupusecase.CheckpointDetail{
		CheckpointSummary: backupusecase.CheckpointSummary{
			ID: checkpoint.ID, CreatedAtUnixMillis: checkpoint.CreatedAtUnixMillis,
			EffectiveAtUnixMillis: checkpoint.EffectiveAtUnixMillis, Held: reference.Held,
		},
		SourceClusterID: checkpoint.SourceClusterID, SourceGeneration: checkpoint.SourceGeneration,
		HashSlotCount: checkpoint.HashSlotCount,
		ErasureHeads:  append([]backupartifact.ErasureStreamHead(nil), checkpoint.ErasureHeads...),
	}, nil
}

func (i *CheckpointCatalogIndex) ensure(ctx context.Context, head backupartifact.CatalogPageReference) error {
	if i.loaded && i.head == head {
		return nil
	}
	if !i.loaded {
		authenticated, err := i.rebuild(ctx, head)
		if err != nil {
			return err
		}
		cached, cacheErr := loadCheckpointIndex(i.path)
		if cacheErr != nil || !reflect.DeepEqual(cached, authenticated) {
			if err := writeCheckpointIndex(i.path, authenticated); err != nil {
				return err
			}
		}
		i.install(authenticated)
		return nil
	}
	var snapshot checkpointIndexSnapshot
	var err error
	if i.loaded && i.head.Sequence < head.Sequence {
		snapshot, err = i.extend(ctx, head)
	}
	if !i.loaded || err != nil || i.head.Sequence >= head.Sequence {
		snapshot, err = i.rebuild(ctx, head)
	}
	if err != nil {
		return err
	}
	if err := writeCheckpointIndex(i.path, snapshot); err != nil {
		return err
	}
	i.install(snapshot)
	return nil
}

func (i *CheckpointCatalogIndex) extend(
	ctx context.Context,
	head backupartifact.CatalogPageReference,
) (checkpointIndexSnapshot, error) {
	reference := &head
	added := make([]backupartifact.CatalogCheckpointReference, 0)
	for !catalogPageReferencesEqual(reference, &i.head) {
		if err := ctx.Err(); err != nil {
			return checkpointIndexSnapshot{}, err
		}
		if reference == nil || reference.Sequence <= i.head.Sequence {
			return checkpointIndexSnapshot{}, backupartifact.ErrObjectCorrupt
		}
		page, err := i.catalog.LoadPage(ctx, *reference)
		if err != nil {
			return checkpointIndexSnapshot{}, err
		}
		added = append(added, page.Entries...)
		reference = page.Previous
	}
	entries := append(added, i.entries...)
	return newCheckpointIndexSnapshot(head, entries)
}

func (i *CheckpointCatalogIndex) rebuild(
	ctx context.Context,
	head backupartifact.CatalogPageReference,
) (checkpointIndexSnapshot, error) {
	reference := &head
	entries := make([]backupartifact.CatalogCheckpointReference, 0)
	for reference != nil {
		if err := ctx.Err(); err != nil {
			return checkpointIndexSnapshot{}, err
		}
		page, err := i.catalog.LoadPage(ctx, *reference)
		if err != nil {
			return checkpointIndexSnapshot{}, err
		}
		entries = append(entries, page.Entries...)
		reference = page.Previous
	}
	return newCheckpointIndexSnapshot(head, entries)
}

func newCheckpointIndexSnapshot(
	head backupartifact.CatalogPageReference,
	entries []backupartifact.CatalogCheckpointReference,
) (checkpointIndexSnapshot, error) {
	snapshot := checkpointIndexSnapshot{
		Format: checkpointIndexFormat, Version: checkpointIndexVersion,
		Head: head, Entries: normalizeCheckpointIndexEntries(entries),
	}
	if err := validateCheckpointIndex(snapshot); err != nil {
		return checkpointIndexSnapshot{}, err
	}
	return snapshot, nil
}

func normalizeCheckpointIndexEntries(
	entries []backupartifact.CatalogCheckpointReference,
) []backupartifact.CatalogCheckpointReference {
	latest := make(map[string]backupartifact.CatalogCheckpointReference, len(entries))
	for _, entry := range entries {
		if _, exists := latest[entry.ID]; !exists {
			latest[entry.ID] = entry
		}
	}
	result := make([]backupartifact.CatalogCheckpointReference, 0, len(latest))
	for _, entry := range latest {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAtUnixMillis != result[j].CreatedAtUnixMillis {
			return result[i].CreatedAtUnixMillis > result[j].CreatedAtUnixMillis
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (i *CheckpointCatalogIndex) install(snapshot checkpointIndexSnapshot) {
	i.loaded = true
	i.head = snapshot.Head
	i.entries = append([]backupartifact.CatalogCheckpointReference(nil), snapshot.Entries...)
	i.byID = make(map[string]backupartifact.CatalogCheckpointReference, len(i.entries))
	for _, entry := range i.entries {
		i.byID[entry.ID] = entry
	}
}

func validateCheckpointIndex(snapshot checkpointIndexSnapshot) error {
	if snapshot.Format != checkpointIndexFormat || snapshot.Version != checkpointIndexVersion ||
		snapshot.Head.Sequence == 0 || len(snapshot.Entries) == 0 {
		return backupartifact.ErrObjectCorrupt
	}
	seen := make(map[string]struct{}, len(snapshot.Entries))
	for index, entry := range snapshot.Entries {
		if entry.ID == "" || entry.Key != backupartifact.CheckpointObjectKey(entry.ID) ||
			!validCheckpointIndexSHA(entry.SHA256) || entry.Bytes <= 0 ||
			entry.EffectiveAtUnixMillis <= 0 ||
			entry.CreatedAtUnixMillis < entry.EffectiveAtUnixMillis ||
			!validCheckpointIndexSHA(entry.GenerationVector.ID) ||
			entry.GenerationVector.Key != backupartifact.GenerationVectorObjectKey(entry.GenerationVector.ID) ||
			!validCheckpointIndexSHA(entry.GenerationVector.SHA256) ||
			entry.GenerationVector.Bytes <= 0 ||
			entry.GenerationVector.HashSlotCount == 0 {
			return backupartifact.ErrObjectCorrupt
		}
		if index > 0 {
			previous := snapshot.Entries[index-1]
			if previous.CreatedAtUnixMillis < entry.CreatedAtUnixMillis ||
				(previous.CreatedAtUnixMillis == entry.CreatedAtUnixMillis && previous.ID >= entry.ID) {
				return backupartifact.ErrObjectCorrupt
			}
		}
		if _, exists := seen[entry.ID]; exists {
			return backupartifact.ErrObjectCorrupt
		}
		seen[entry.ID] = struct{}{}
	}
	if _, exists := seen[snapshot.Head.LatestCheckpointID]; !exists {
		return backupartifact.ErrObjectCorrupt
	}
	return nil
}

func loadCheckpointIndex(path string) (checkpointIndexSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return checkpointIndexSnapshot{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxCheckpointIndexBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxCheckpointIndexBytes {
		return checkpointIndexSnapshot{}, backupartifact.ErrObjectCorrupt
	}
	var envelope checkpointIndexEnvelope
	if strictCheckpointIndexJSON(body, &envelope) != nil ||
		!validCheckpointIndexSHA(envelope.SHA256) ||
		checkpointIndexSHA256(envelope.Payload) != envelope.SHA256 {
		return checkpointIndexSnapshot{}, backupartifact.ErrObjectCorrupt
	}
	var snapshot checkpointIndexSnapshot
	if strictCheckpointIndexJSON(envelope.Payload, &snapshot) != nil ||
		validateCheckpointIndex(snapshot) != nil {
		return checkpointIndexSnapshot{}, backupartifact.ErrObjectCorrupt
	}
	return snapshot, nil
}

func writeCheckpointIndex(path string, snapshot checkpointIndexSnapshot) error {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	body, err := json.Marshal(checkpointIndexEnvelope{
		Payload: payload, SHA256: checkpointIndexSHA256(payload),
	})
	if err != nil || len(body) > maxCheckpointIndexBytes {
		return backupartifact.ErrObjectCorrupt
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-index-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	remove = false
	return nil
}

func checkpointPageStart(entries []backupartifact.CatalogCheckpointReference, cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, backupusecase.ErrInvalidRequest
	}
	var value checkpointCursor
	if strictCheckpointIndexJSON(decoded, &value) != nil || value.Version != 1 || value.ID == "" {
		return 0, backupusecase.ErrInvalidRequest
	}
	for index, entry := range entries {
		if entry.ID == value.ID {
			return index + 1, nil
		}
	}
	return 0, backupusecase.ErrInvalidRequest
}

func encodeCheckpointCursor(id string) (string, error) {
	body, err := json.Marshal(checkpointCursor{Version: 1, ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func strictCheckpointIndexJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return backupartifact.ErrObjectCorrupt
	}
	return nil
}

func validCheckpointIndexSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func checkpointIndexSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func catalogPageReferencesEqual(left, right *backupartifact.CatalogPageReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ backupusecase.CheckpointCatalogBrowser = (*CheckpointCatalogIndex)(nil)
