package backup

import (
	"context"
	"fmt"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// SourceFenceRequest binds an irreversible source fence to one exact successor plan.
type SourceFenceRequest struct {
	// RestorePlanID identifies the immutable target recovery plan.
	RestorePlanID string
	// CheckpointID selects the exact authenticated checkpoint.
	CheckpointID string
	// TargetClusterID and TargetGeneration identify the intended successor.
	TargetClusterID  string
	TargetGeneration string
}

// FenceSource irreversibly closes ordinary source-generation writes, waits for
// every active data node to observe the fence revision, and returns a signed receipt.
func (a *App) FenceSource(
	ctx context.Context,
	request SourceFenceRequest,
) (SourceFenceReceipt, error) {
	if !a.enabled {
		return SourceFenceReceipt{}, ErrDisabled
	}
	request.RestorePlanID = strings.TrimSpace(request.RestorePlanID)
	request.CheckpointID = strings.TrimSpace(request.CheckpointID)
	request.TargetClusterID = strings.TrimSpace(request.TargetClusterID)
	request.TargetGeneration = strings.TrimSpace(request.TargetGeneration)
	if a.sourceClusterID == "" || a.sourceGeneration == "" ||
		a.sourceFence == nil || a.sourceFenceSigner == nil ||
		a.signingKeyID == "" || a.newSourceFenceID == nil ||
		request.RestorePlanID == "" || request.CheckpointID == "" ||
		request.TargetClusterID == "" || request.TargetGeneration == "" {
		return SourceFenceReceipt{}, ErrInvalidRequest
	}
	checkpoint, err := a.CheckpointByID(ctx, request.CheckpointID)
	if err != nil {
		return SourceFenceReceipt{}, err
	}
	if checkpoint.SourceClusterID != a.sourceClusterID ||
		checkpoint.SourceGeneration != a.sourceGeneration ||
		checkpoint.CheckpointSHA256 == "" ||
		request.TargetClusterID == a.sourceClusterID ||
		request.TargetGeneration == a.sourceGeneration {
		return SourceFenceReceipt{}, ErrInvalidRequest
	}

	now := a.now().UTC().UnixMilli()
	fenceID := strings.TrimSpace(a.newSourceFenceID())
	if fenceID == "" {
		return SourceFenceReceipt{}, ErrInvalidRequest
	}
	record := SourceFenceRecord{
		Format:                  backupartifact.SourceFenceReceiptFormat,
		Version:                 backupartifact.SourceFenceReceiptVersion,
		ID:                      fenceID,
		SourceClusterID:         a.sourceClusterID,
		SourceGeneration:        a.sourceGeneration,
		RestorePlanID:           request.RestorePlanID,
		CheckpointID:            request.CheckpointID,
		CheckpointSHA256:        checkpoint.CheckpointSHA256,
		TargetClusterID:         request.TargetClusterID,
		TargetGeneration:        request.TargetGeneration,
		RequestedAtUnixMillis:   now,
		ConvergedAtUnixMillis:   0,
		FenceControllerRevision: 0,
	}
	err = a.mutate(ctx, func(state *State) error {
		if state.SourceFence != nil {
			if !sourceFenceMatchesRequest(*state.SourceFence, request) {
				return ErrSourceFenceExists
			}
			record = *state.SourceFence
			return nil
		}
		record.FenceControllerRevision = state.Revision + 1
		if err := backupartifact.ValidateSourceFenceRecord(record, false); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		stored := record
		state.SourceFence = &stored
		return nil
	})
	if err != nil {
		return SourceFenceReceipt{}, err
	}
	if record.ConvergedAtUnixMillis == 0 {
		if err := a.sourceFence.WaitForSourceFence(ctx, record); err != nil {
			return SourceFenceReceipt{}, err
		}
		convergedAt := a.now().UTC().UnixMilli()
		if convergedAt < record.RequestedAtUnixMillis {
			convergedAt = record.RequestedAtUnixMillis
		}
		err = a.mutate(ctx, func(state *State) error {
			if state.SourceFence == nil ||
				state.SourceFence.ID != record.ID ||
				!sourceFenceMatchesRequest(*state.SourceFence, request) {
				return ErrSourceFenceExists
			}
			if state.SourceFence.ConvergedAtUnixMillis == 0 {
				state.SourceFence.ConvergedAtUnixMillis = convergedAt
			}
			record = *state.SourceFence
			return nil
		})
		if err != nil {
			return SourceFenceReceipt{}, err
		}
	}
	receipt, err := backupartifact.SignSourceFenceReceipt(
		ctx, record, a.sourceFenceSigner, a.signingKeyID,
	)
	if err != nil {
		return SourceFenceReceipt{}, err
	}
	return receipt, nil
}

func sourceFenceMatchesRequest(
	record SourceFenceRecord,
	request SourceFenceRequest,
) bool {
	return record.SourceClusterID != record.TargetClusterID &&
		record.SourceGeneration != record.TargetGeneration &&
		record.RestorePlanID == request.RestorePlanID &&
		record.CheckpointID == request.CheckpointID &&
		record.TargetClusterID == request.TargetClusterID &&
		record.TargetGeneration == request.TargetGeneration
}
