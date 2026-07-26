package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

// GenerationGCCursorStore persists one bounded independent cursor per repository.
type GenerationGCCursorStore interface {
	LoadGenerationGCCursor(context.Context, string) (backupcontract.GenerationGCCursor, bool, error)
	CompareAndSwapGenerationGCCursor(context.Context, string, uint64, backupcontract.GenerationGCCursor) error
}

// ControllerGenerationGCCursorStore stores only two compact cursor records in
// Controller state; it never stores pending object identities.
type ControllerGenerationGCCursorStore struct {
	state CoordinationStateStore
}

// NewControllerGenerationGCCursorStore creates a bounded durable cursor adapter.
func NewControllerGenerationGCCursorStore(state CoordinationStateStore) (*ControllerGenerationGCCursorStore, error) {
	if state == nil {
		return nil, fmt.Errorf("backup generation GC cursor store: state is required")
	}
	return &ControllerGenerationGCCursorStore{state: state}, nil
}

func (s *ControllerGenerationGCCursorStore) LoadGenerationGCCursor(
	ctx context.Context,
	repository string,
) (backupcontract.GenerationGCCursor, bool, error) {
	repository = strings.TrimSpace(repository)
	if s == nil || s.state == nil || !validControllerCaptureGeneration(repository) {
		return backupcontract.GenerationGCCursor{}, false, runtimebackup.ErrInvalidCapture
	}
	state, err := s.state.Load(ctx)
	if err != nil {
		return backupcontract.GenerationGCCursor{}, false, err
	}
	index, found := findGenerationGCCursor(state.GenerationGCCursors, repository)
	if !found {
		return backupcontract.GenerationGCCursor{}, false, nil
	}
	return state.GenerationGCCursors[index], true, nil
}

func (s *ControllerGenerationGCCursorStore) CompareAndSwapGenerationGCCursor(
	ctx context.Context,
	repository string,
	expectedRevision uint64,
	next backupcontract.GenerationGCCursor,
) error {
	repository = strings.TrimSpace(repository)
	if s == nil || s.state == nil || next.Repository != repository ||
		next.Revision != expectedRevision+1 || !validGenerationGCCursor(next) {
		return runtimebackup.ErrInvalidCapture
	}
	for attempt := 0; attempt < maxControllerFrontierCASAttempts; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		index, found := findGenerationGCCursor(state.GenerationGCCursors, repository)
		if found {
			if state.GenerationGCCursors[index].Revision != expectedRevision {
				return backupusecase.ErrStateConflict
			}
			state.GenerationGCCursors[index] = next
		} else {
			if expectedRevision != 0 || len(state.GenerationGCCursors) >= 2 {
				return backupusecase.ErrStateConflict
			}
			state.GenerationGCCursors = append(state.GenerationGCCursors, next)
			sort.Slice(state.GenerationGCCursors, func(i, j int) bool {
				return state.GenerationGCCursors[i].Repository < state.GenerationGCCursors[j].Repository
			})
		}
		err = s.state.CompareAndSwap(ctx, state.Revision, state)
		if err == nil {
			return nil
		}
		if !errors.Is(err, backupusecase.ErrStateConflict) {
			return err
		}
	}
	return fmt.Errorf("%w: Controller state remained contended", backupusecase.ErrStateConflict)
}

func validGenerationGCCursor(cursor backupcontract.GenerationGCCursor) bool {
	return validControllerCaptureGeneration(cursor.Repository) &&
		validControllerCaptureGeneration(cursor.CycleID) &&
		cursor.CatalogRetentionRevision > 0 &&
		cursor.Revision > 0 && len(cursor.AfterKey) <= 8<<10 &&
		utf8.ValidString(cursor.AfterKey) &&
		cursor.CutoffUnixMillis > 0 && cursor.UpdatedAtUnixMillis > 0
}

func findGenerationGCCursor(
	cursors []backupcontract.GenerationGCCursor,
	repository string,
) (int, bool) {
	index := sort.Search(len(cursors), func(index int) bool {
		return cursors[index].Repository >= repository
	})
	return index, index < len(cursors) && cursors[index].Repository == repository
}

var _ GenerationGCCursorStore = (*ControllerGenerationGCCursorStore)(nil)
