package delivery

import (
	"context"
	"errors"
	"time"
)

const (
	// DeliveryAckActionBind reports a pending ack bind attempt.
	DeliveryAckActionBind = "bind"
	// DeliveryAckActionAck reports a client recvack mutation attempt.
	DeliveryAckActionAck = "ack"
	// DeliveryAckActionRollback reports a failed delivery's token-scoped rollback.
	DeliveryAckActionRollback = "rollback"
	// DeliveryAckActionSessionClosed reports cleanup for a closed owner-local session.
	DeliveryAckActionSessionClosed = "session_closed"
	// DeliveryAckActionExpire reports TTL cleanup for stale pending acks.
	DeliveryAckActionExpire = "expire"
	// DeliveryAckActionReset reports lifecycle cleanup of transient ack state.
	DeliveryAckActionReset = "reset"

	// DeliveryAckResultOK reports that the ack mutation changed state successfully.
	DeliveryAckResultOK = "ok"
	// DeliveryAckResultMiss reports that an ack mutation found no matching pending entry.
	DeliveryAckResultMiss = "miss"
	// DeliveryAckResultRejected reports that a pending ack bind was rejected by limits or invalid input.
	DeliveryAckResultRejected = "rejected"
	// DeliveryAckResultNoop reports that a cleanup mutation had no matching state to remove.
	DeliveryAckResultNoop = "noop"

	// DeliveryAckBatchPhaseBind reports one item-aligned tracker batch reservation stage.
	DeliveryAckBatchPhaseBind = "bind"
	// DeliveryAckBatchPhaseFinish reports one shard-grouped successful-write finish stage.
	DeliveryAckBatchPhaseFinish = "finish"

	// DeliveryAckBatchOutcomeOK reports a complete batch stage without rejected or rolled-back items.
	DeliveryAckBatchOutcomeOK = "ok"
	// DeliveryAckBatchOutcomePartial reports a batch stage with both successful and unsuccessful items.
	DeliveryAckBatchOutcomePartial = "partial"
	// DeliveryAckBatchOutcomeRejected reports a bind stage where every item was rejected.
	DeliveryAckBatchOutcomeRejected = "rejected"
	// DeliveryAckBatchOutcomeRolledBack reports a finish stage with no successful item and at least one rollback.
	DeliveryAckBatchOutcomeRolledBack = "rolled_back"
	// DeliveryAckBatchOutcomeMiss reports a finish stage whose selected tokens were already consumed.
	DeliveryAckBatchOutcomeMiss = "miss"

	// DeliveryErrorClassNone reports that no error was present.
	DeliveryErrorClassNone = "none"
	// DeliveryErrorClassCanceled reports context cancellation.
	DeliveryErrorClassCanceled = "canceled"
	// DeliveryErrorClassDeadline reports context deadline expiry.
	DeliveryErrorClassDeadline = "deadline"
	// DeliveryErrorClassError reports an unclassified delivery error.
	DeliveryErrorClassError = "error"
)

// AckObserver receives owner-local pending recvack state changes.
type AckObserver interface {
	ObserveAck(AckEvent)
}

// AckBatchObserver receives aggregate owner-local batch bind and finish stages.
type AckBatchObserver interface {
	ObserveAckBatch(AckBatchEvent)
}

// AckEvent describes one owner-local pending recvack mutation.
type AckEvent struct {
	// Action is bind, rollback, ack, session_closed, or expire.
	Action string
	// Result is ok, miss, rejected, or noop.
	Result string
	// Changed is the number of pending ack entries added or removed.
	Changed int
	// PendingCount is the owner-local pending ack count after the mutation.
	PendingCount int
}

// AckBatchEvent describes one owner-local pending recvack batch stage.
type AckBatchEvent struct {
	// Phase is bind or finish.
	Phase string
	// Outcome is ok, partial, rejected, rolled_back, or miss.
	Outcome string
	// Items is the aligned batch item count.
	Items int
	// Shards is the number of tracker shards touched by this stage.
	Shards int
	// Rejected is the number of items that did not receive a bind token.
	Rejected int
	// Rollback is the number of bound reservations actually canceled by the caller.
	Rollback int
	// Duration is the complete tracker batch stage latency.
	Duration time.Duration
}

// DeliveryErrorClass normalizes delivery errors into bounded labels.
func DeliveryErrorClass(err error) string {
	switch {
	case err == nil:
		return DeliveryErrorClassNone
	case errors.Is(err, context.Canceled):
		return DeliveryErrorClassCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return DeliveryErrorClassDeadline
	default:
		return DeliveryErrorClassError
	}
}
