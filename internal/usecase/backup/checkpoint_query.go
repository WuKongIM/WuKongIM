package backup

import (
	"context"
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
	// CatalogHead is the exact immutable discovery fence represented by Items.
	CatalogHead *backupartifact.CatalogPageReference
	// Items are ordered newest first.
	Items []CheckpointSummary
	// NextCursor continues after the final returned item.
	NextCursor string
	// Total is the number of rows matching the current filter.
	Total int
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
	page.CatalogHead = cloneCatalogPageHead(state.CatalogHead)
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
