package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// IntegrityAuditRetentionSelectionRequest fixes the authenticated catalog and
// UTC retention instant used by one durable integrity-audit cycle.
type IntegrityAuditRetentionSelectionRequest struct {
	Head backupartifact.CatalogPageReference
	At   time.Time
	// ActiveRestoreCheckpointID is nil when starting a new cycle. A resumed
	// cycle supplies the exact previously selected value, including empty.
	ActiveRestoreCheckpointID *string
}

// IntegrityAuditRetentionSelection is an authenticated sparse checkpoint set.
// Only its digest and active-restore identity enter Controller Raft.
type IntegrityAuditRetentionSelection struct {
	ID                        string
	ActiveRestoreCheckpointID string
	Checkpoints               []backupartifact.CatalogCheckpointReference
}

// IntegrityAuditRetentionSelectionSource deterministically rebuilds the exact
// sparse checkpoint set for a fixed catalog head, UTC instant, and restore ID.
type IntegrityAuditRetentionSelectionSource interface {
	LoadIntegrityAuditRetentionSelection(
		context.Context,
		IntegrityAuditRetentionSelectionRequest,
	) (IntegrityAuditRetentionSelection, error)
}

// IntegrityAuditActiveRestoreSource returns the checkpoint protected by an
// in-progress restore, or an empty string when no restore is active.
type IntegrityAuditActiveRestoreSource interface {
	ActiveRestoreCheckpointID(context.Context) (string, error)
}

// CheckpointIndexIntegrityAuditRetentionSource selects the same signed sparse
// checkpoint references consumed by Generation GC.
type CheckpointIndexIntegrityAuditRetentionSource struct {
	index         *CheckpointCatalogIndex
	policy        backupusecase.RetentionPolicy
	activeRestore IntegrityAuditActiveRestoreSource
}

// CheckpointIndexIntegrityAuditRetentionSourceOptions configures sparse audit
// selection without making the rebuildable index an authority.
type CheckpointIndexIntegrityAuditRetentionSourceOptions struct {
	Index         *CheckpointCatalogIndex
	Policy        backupusecase.RetentionPolicy
	ActiveRestore IntegrityAuditActiveRestoreSource
}

// NewCheckpointIndexIntegrityAuditRetentionSource creates a deterministic
// sparse retention selector.
func NewCheckpointIndexIntegrityAuditRetentionSource(
	options CheckpointIndexIntegrityAuditRetentionSourceOptions,
) (*CheckpointIndexIntegrityAuditRetentionSource, error) {
	if options.Index == nil || options.ActiveRestore == nil ||
		options.Policy.MonthlyMonths < 0 ||
		options.Policy.MonthlyMonths > 120 {
		return nil, fmt.Errorf(
			"backup integrity audit retention source: dependencies are invalid",
		)
	}
	return &CheckpointIndexIntegrityAuditRetentionSource{
		index: options.Index, policy: options.Policy,
		activeRestore: options.ActiveRestore,
	}, nil
}

// LoadIntegrityAuditRetentionSelection authenticates catalog history through
// the index and applies the fixed UTC tier decision.
func (s *CheckpointIndexIntegrityAuditRetentionSource) LoadIntegrityAuditRetentionSelection(
	ctx context.Context,
	request IntegrityAuditRetentionSelectionRequest,
) (IntegrityAuditRetentionSelection, error) {
	if request.Head.Sequence == 0 || request.At.IsZero() {
		return IntegrityAuditRetentionSelection{}, fmt.Errorf(
			"backup integrity audit retention source: request is invalid",
		)
	}
	references, err := s.index.References(ctx, request.Head)
	if err != nil {
		return IntegrityAuditRetentionSelection{}, err
	}
	activeRestoreCheckpointID := ""
	if request.ActiveRestoreCheckpointID == nil {
		activeRestoreCheckpointID, err =
			s.activeRestore.ActiveRestoreCheckpointID(ctx)
		if err != nil {
			return IntegrityAuditRetentionSelection{}, err
		}
	} else {
		activeRestoreCheckpointID =
			strings.TrimSpace(*request.ActiveRestoreCheckpointID)
	}
	if activeRestoreCheckpointID != "" &&
		!catalogCheckpointReferenceExists(
			references, activeRestoreCheckpointID,
		) {
		return IntegrityAuditRetentionSelection{}, fmt.Errorf(
			"%w: active restore checkpoint is absent from catalog",
			backupartifact.ErrObjectCorrupt,
		)
	}
	decision, err := backupusecase.DecideCheckpointRetention(
		request.At.UTC(), references, s.policy,
		activeRestoreCheckpointID,
	)
	if err != nil {
		return IntegrityAuditRetentionSelection{}, err
	}
	selection := IntegrityAuditRetentionSelection{
		ActiveRestoreCheckpointID: activeRestoreCheckpointID,
		Checkpoints: append(
			[]backupartifact.CatalogCheckpointReference(nil),
			decision.Retain...,
		),
	}
	return NewIntegrityAuditRetentionSelection(
		request.Head, request.At, selection.ActiveRestoreCheckpointID,
		selection.Checkpoints,
	)
}

// NewIntegrityAuditRetentionSelection builds the content digest used to bind a
// durable cursor to an exact sparse set without storing that set in Raft.
func NewIntegrityAuditRetentionSelection(
	head backupartifact.CatalogPageReference,
	at time.Time,
	activeRestoreCheckpointID string,
	checkpoints []backupartifact.CatalogCheckpointReference,
) (IntegrityAuditRetentionSelection, error) {
	selection := IntegrityAuditRetentionSelection{
		ActiveRestoreCheckpointID: strings.TrimSpace(
			activeRestoreCheckpointID,
		),
		Checkpoints: append(
			[]backupartifact.CatalogCheckpointReference(nil),
			checkpoints...,
		),
	}
	var err error
	selection.ID, err = integrityAuditRetentionSelectionID(
		IntegrityAuditRetentionSelectionRequest{Head: head, At: at},
		selection,
	)
	if err != nil {
		return IntegrityAuditRetentionSelection{}, err
	}
	return selection, nil
}

func integrityAuditRetentionSelectionID(
	request IntegrityAuditRetentionSelectionRequest,
	selection IntegrityAuditRetentionSelection,
) (string, error) {
	checkpoints := append(
		[]backupartifact.CatalogCheckpointReference(nil),
		selection.Checkpoints...,
	)
	sort.Slice(checkpoints, func(left, right int) bool {
		return checkpoints[left].ID < checkpoints[right].ID
	})
	payload, err := json.Marshal(struct {
		Head                         backupartifact.CatalogPageReference         `json:"head"`
		AtUnixMillis                 int64                                       `json:"at_unix_millis"`
		ActiveRestoreCheckpointID    string                                      `json:"active_restore_checkpoint_id,omitempty"`
		RetainedCheckpointReferences []backupartifact.CatalogCheckpointReference `json:"retained_checkpoint_references"`
	}{
		Head: request.Head, AtUnixMillis: request.At.UTC().UnixMilli(),
		ActiveRestoreCheckpointID:    selection.ActiveRestoreCheckpointID,
		RetainedCheckpointReferences: checkpoints,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func catalogCheckpointReferenceExists(
	references []backupartifact.CatalogCheckpointReference,
	checkpointID string,
) bool {
	for _, reference := range references {
		if reference.ID == checkpointID {
			return true
		}
	}
	return false
}
