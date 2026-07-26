package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	// DefaultCheckpointPageSize is used when a caller omits a page size.
	DefaultCheckpointPageSize = 50
	// MaxCheckpointPageSize bounds one Manager catalog response.
	MaxCheckpointPageSize = 200
)

// CheckpointSummary is one repository-backed checkpoint catalog row.
type CheckpointSummary struct {
	// ID identifies the immutable checkpoint.
	ID string
	// CreatedAtUnixMillis is its UTC publication time.
	CreatedAtUnixMillis int64
	// EffectiveAtUnixMillis is the oldest Slot watermark.
	EffectiveAtUnixMillis int64
	// Held is the latest signed immutable catalog retention decision.
	Held bool
}

// CheckpointDetail is the bounded operator-facing projection of a checkpoint.
type CheckpointDetail struct {
	CheckpointSummary
	// CheckpointSHA256 authenticates the exact selected checkpoint bytes.
	CheckpointSHA256 string
	// SourceClusterID and SourceGeneration fence the captured source.
	SourceClusterID  string
	SourceGeneration string
	// HashSlotCount is the exact vector width.
	HashSlotCount uint16
	// ErasureHeads are the authenticated per-Slot deletion prefixes observed at publication.
	ErasureHeads []backupartifact.ErasureStreamHead
}

// CheckpointListRequest selects one stable newest-first catalog page.
type CheckpointListRequest struct {
	// Cursor is the opaque keyset returned by the previous page.
	Cursor string
	// Limit bounds response rows.
	Limit int
	// IDQuery applies a case-insensitive checkpoint-ID filter.
	IDQuery string
}

// CheckpointPage is one stable keyset-paged catalog response.
type CheckpointPage struct {
	// CatalogHeadToken is the opaque immutable discovery fence represented by Items.
	CatalogHeadToken string
	// Items are ordered newest first.
	Items []CheckpointSummary
	// NextCursor continues after the final returned item.
	NextCursor string
	// Total is the number of rows matching the current filter.
	Total int
}

// CheckpointPublication is the bounded result of one explicit vector cut.
type CheckpointPublication struct {
	// Checkpoint is the newly published immutable catalog row.
	Checkpoint CheckpointSummary
	// CheckpointSHA256 authenticates the exact checkpoint bytes.
	CheckpointSHA256 string
	// CatalogHeadToken is the opaque immutable discovery fence after publication.
	CatalogHeadToken string
}

// CheckpointHoldCommit is one internal immutable retention-state append.
type CheckpointHoldCommit struct {
	// Checkpoint is the latest operator-facing checkpoint state.
	Checkpoint CheckpointSummary
	// Head is the new internal catalog head, or the current head when unchanged.
	Head backupartifact.CatalogPageReference
	// Changed reports whether a new state-only page was appended.
	Changed bool
}

type catalogHeadTokenEnvelope struct {
	Version uint16                              `json:"version"`
	Head    backupartifact.CatalogPageReference `json:"head"`
}

const catalogHeadTokenVersion uint16 = 1

// EncodeCatalogHeadToken hides repository object coordinates behind a bounded
// versioned token while preserving the exact authenticated restore fence.
func EncodeCatalogHeadToken(
	head backupartifact.CatalogPageReference,
) (string, error) {
	if backupartifact.ValidateCatalogPageReference(head) != nil {
		return "", ErrInvalidRequest
	}
	body, err := json.Marshal(catalogHeadTokenEnvelope{
		Version: catalogHeadTokenVersion, Head: head,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

// DecodeCatalogHeadToken restores one exact internal catalog reference.
func DecodeCatalogHeadToken(
	token string,
) (backupartifact.CatalogPageReference, error) {
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(body) == 0 || len(body) > 16<<10 {
		return backupartifact.CatalogPageReference{}, ErrInvalidRequest
	}
	var envelope catalogHeadTokenEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil ||
		envelope.Version != catalogHeadTokenVersion ||
		backupartifact.ValidateCatalogPageReference(envelope.Head) != nil {
		return backupartifact.CatalogPageReference{}, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return backupartifact.CatalogPageReference{}, fmt.Errorf(
			"%w: catalog head token has trailing data", ErrInvalidRequest,
		)
	}
	return envelope.Head, nil
}

// ListCheckpointsPage reads immutable checkpoint history instead of Controller arrays.
func (a *App) ListCheckpointsPage(ctx context.Context, request CheckpointListRequest) (CheckpointPage, error) {
	if !a.enabled {
		return CheckpointPage{}, ErrDisabled
	}
	if a.catalogBrowser == nil || request.Limit < 0 || request.Limit > MaxCheckpointPageSize {
		return CheckpointPage{}, ErrInvalidRequest
	}
	if request.Limit == 0 {
		request.Limit = DefaultCheckpointPageSize
	}
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.IDQuery = strings.TrimSpace(request.IDQuery)
	state, err := a.store.Load(ctx)
	if err != nil {
		return CheckpointPage{}, err
	}
	if state.CatalogHead == nil {
		return CheckpointPage{Items: []CheckpointSummary{}}, nil
	}
	page, err := a.catalogBrowser.List(ctx, *state.CatalogHead, request)
	if err != nil {
		return CheckpointPage{}, err
	}
	page.CatalogHeadToken, err = EncodeCatalogHeadToken(*state.CatalogHead)
	if err != nil {
		return CheckpointPage{}, err
	}
	return page, nil
}

// CheckpointByID returns a bounded detail projection for one exact checkpoint.
func (a *App) CheckpointByID(ctx context.Context, checkpointID string) (CheckpointDetail, error) {
	if !a.enabled {
		return CheckpointDetail{}, ErrDisabled
	}
	checkpointID = strings.TrimSpace(checkpointID)
	if a.catalogBrowser == nil || checkpointID == "" {
		return CheckpointDetail{}, ErrInvalidRequest
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return CheckpointDetail{}, err
	}
	if state.CatalogHead == nil {
		return CheckpointDetail{}, ErrCheckpointNotFound
	}
	return a.catalogBrowser.Get(ctx, *state.CatalogHead, checkpointID)
}

// SetCheckpointHold atomically advances the catalog retention revision after
// both immutable state-page copies commit. Repeated requests are idempotent.
func (a *App) SetCheckpointHold(
	ctx context.Context,
	checkpointID string,
	held bool,
) (CheckpointSummary, error) {
	if a == nil || !a.enabled {
		return CheckpointSummary{}, ErrDisabled
	}
	checkpointID = strings.TrimSpace(checkpointID)
	if checkpointID == "" || a.catalogRetention == nil {
		return CheckpointSummary{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := a.store.Load(ctx)
		if err != nil {
			return CheckpointSummary{}, err
		}
		if state.CatalogHead == nil {
			return CheckpointSummary{}, ErrCheckpointNotFound
		}
		now := a.now().UTC().UnixMilli()
		if state.CatalogRetentionRevision == 0 ||
			hasActiveGenerationGCGuard(state, now) {
			return CheckpointSummary{}, ErrStateConflict
		}
		commit, err := a.catalogRetention.SetCheckpointHold(
			ctx, *state.CatalogHead, checkpointID, held,
			now,
		)
		if err != nil {
			return CheckpointSummary{}, err
		}
		if !commit.Changed {
			return commit.Checkpoint, nil
		}
		next := state.Clone()
		next.CatalogHead = cloneCatalogPageHead(&commit.Head)
		next.CatalogRetentionRevision++
		if next.CatalogRetentionRevision == 0 {
			return CheckpointSummary{}, ErrStateConflict
		}
		if err := a.store.CompareAndSwap(
			ctx, state.Revision, next,
		); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return CheckpointSummary{}, err
		}
		return commit.Checkpoint, nil
	}
	return CheckpointSummary{}, ErrStateConflict
}

func hasActiveGenerationGCGuard(state State, nowUnixMillis int64) bool {
	for _, guard := range state.IntegrityAudit.GCGuards {
		if guard.ExpiresAtUnixMillis > nowUnixMillis {
			return true
		}
	}
	return false
}
