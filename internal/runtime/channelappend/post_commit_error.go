package channelappend

import (
	"errors"
	"fmt"
)

// PostCommitFailureDetail carries high-cardinality diagnostics for one dropped post-commit side effect.
type PostCommitFailureDetail struct {
	// Phase identifies the post-commit sub-step that produced the failure.
	Phase string
	// UID is one representative recipient UID involved in the failure.
	UID string
	// UIDCount is the number of unique UIDs being resolved or processed.
	UIDCount int
	// RecipientCount is the number of recipient rows in the failed batch or page.
	RecipientCount int
	// TargetHashSlot is the recipient authority hash slot when known.
	TargetHashSlot uint16
	// TargetSlotID is the logical Slot Raft Group that owns TargetHashSlot when known.
	TargetSlotID uint32
	// TargetLeaderNodeID is the recipient authority leader node when known.
	TargetLeaderNodeID uint64
	// TargetRouteRevision is the route-table revision used to resolve the target when known.
	TargetRouteRevision uint64
	// TargetAuthorityEpoch is the authority epoch used to fence the target when known.
	TargetAuthorityEpoch uint64
	// DispatchTargetCount is the number of recipient authority targets in the failed dispatch fanout.
	DispatchTargetCount int
	// DispatchBatchSize is the number of recipients in the failed dispatch batch.
	DispatchBatchSize int
	// DispatchOwnerNodeID is the owner node for a failed online delivery push.
	DispatchOwnerNodeID uint64
	// DispatchOwnerRouteNum is the number of online routes in a failed owner push.
	DispatchOwnerRouteNum int
}

type postCommitFailureError struct {
	detail PostCommitFailureDetail
	err    error
}

func (e *postCommitFailureError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	if e.detail.Phase == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%s: %v", e.detail.Phase, e.err)
}

func (e *postCommitFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func withPostCommitFailureDetail(err error, detail PostCommitFailureDetail) error {
	if err == nil || detail.Phase == "" {
		return err
	}
	return &postCommitFailureError{detail: detail, err: err}
}

func postCommitFailureDetailFromError(err error) PostCommitFailureDetail {
	var detailed *postCommitFailureError
	if errors.As(err, &detailed) && detailed != nil {
		return detailed.detail
	}
	return PostCommitFailureDetail{}
}

func postCommitTargetDetail(target RecipientAuthorityTarget) PostCommitFailureDetail {
	return PostCommitFailureDetail{
		TargetHashSlot:       target.HashSlot,
		TargetSlotID:         target.SlotID,
		TargetLeaderNodeID:   target.LeaderNodeID,
		TargetRouteRevision:  target.RouteRevision,
		TargetAuthorityEpoch: target.AuthorityEpoch,
	}
}

func (d PostCommitFailureDetail) withFallback(fallback PostCommitFailureDetail) PostCommitFailureDetail {
	if d.TargetHashSlot == 0 {
		d.TargetHashSlot = fallback.TargetHashSlot
	}
	if d.TargetSlotID == 0 {
		d.TargetSlotID = fallback.TargetSlotID
	}
	if d.TargetLeaderNodeID == 0 {
		d.TargetLeaderNodeID = fallback.TargetLeaderNodeID
	}
	if d.TargetRouteRevision == 0 {
		d.TargetRouteRevision = fallback.TargetRouteRevision
	}
	if d.TargetAuthorityEpoch == 0 {
		d.TargetAuthorityEpoch = fallback.TargetAuthorityEpoch
	}
	return d
}

func (d PostCommitFailureDetail) toObservation(event CommittedEnvelope, attempt int, result string, err error) PostCommitFailureObservation {
	return PostCommitFailureObservation{
		ChannelID:             event.ChannelID,
		ChannelType:           event.ChannelType,
		MessageID:             event.MessageID,
		MessageSeq:            event.MessageSeq,
		Attempt:               attempt,
		Result:                result,
		Phase:                 d.Phase,
		UID:                   d.UID,
		UIDCount:              d.UIDCount,
		RecipientCount:        d.RecipientCount,
		TargetHashSlot:        d.TargetHashSlot,
		TargetSlotID:          d.TargetSlotID,
		TargetLeaderNodeID:    d.TargetLeaderNodeID,
		TargetRouteRevision:   d.TargetRouteRevision,
		TargetAuthorityEpoch:  d.TargetAuthorityEpoch,
		DispatchTargetCount:   d.DispatchTargetCount,
		DispatchBatchSize:     d.DispatchBatchSize,
		DispatchOwnerNodeID:   d.DispatchOwnerNodeID,
		DispatchOwnerRouteNum: d.DispatchOwnerRouteNum,
		Err:                   err,
	}
}
