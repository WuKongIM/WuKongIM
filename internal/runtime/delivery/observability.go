package delivery

import (
	"context"
	"errors"
	"time"
)

const (
	// DeliveryAckActionBind reports a pending ack reservation.
	DeliveryAckActionBind = "bind"
	// DeliveryAckActionAck reports client recvack cleanup.
	DeliveryAckActionAck = "ack"
	// DeliveryAckActionRollback reports failed-write reservation cleanup.
	DeliveryAckActionRollback = "rollback"
	// DeliveryAckActionSessionClosed reports exact-session cleanup.
	DeliveryAckActionSessionClosed = "session_closed"
	// DeliveryAckActionExpire reports TTL cleanup.
	DeliveryAckActionExpire = "expire"
	// DeliveryAckActionReset reports lifecycle cleanup of transient ack state.
	DeliveryAckActionReset = "reset"

	// DeliveryAckResultOK reports a successful state mutation.
	DeliveryAckResultOK = "ok"
	// DeliveryAckResultMiss reports that no matching state remained.
	DeliveryAckResultMiss = "miss"
	// DeliveryAckResultRejected reports a reservation rejected by validation or limits.
	DeliveryAckResultRejected = "rejected"
	// DeliveryAckResultNoop reports cleanup with no matching state.
	DeliveryAckResultNoop = "noop"

	// DeliveryAckBatchPhaseBind reports aligned batch reservation.
	DeliveryAckBatchPhaseBind = "bind"
	// DeliveryAckBatchPhaseFinish reports aligned success commit and rollback.
	DeliveryAckBatchPhaseFinish = "finish"

	// DeliveryAckBatchOutcomeOK reports a complete batch stage.
	DeliveryAckBatchOutcomeOK = "ok"
	// DeliveryAckBatchOutcomePartial reports mixed batch outcomes.
	DeliveryAckBatchOutcomePartial = "partial"
	// DeliveryAckBatchOutcomeRejected reports that every reservation was rejected.
	DeliveryAckBatchOutcomeRejected = "rejected"
	// DeliveryAckBatchOutcomeRolledBack reports that selected writes all rolled back.
	DeliveryAckBatchOutcomeRolledBack = "rolled_back"
	// DeliveryAckBatchOutcomeMiss reports that selected tokens were already consumed.
	DeliveryAckBatchOutcomeMiss = "miss"

	// DeliveryErrorClassNone reports that no error was present.
	DeliveryErrorClassNone = "none"
	// DeliveryErrorClassCanceled reports context cancellation.
	DeliveryErrorClassCanceled = "canceled"
	// DeliveryErrorClassDeadline reports context deadline expiry.
	DeliveryErrorClassDeadline = "deadline"
	// DeliveryErrorClassError reports an unclassified Online Delivery error.
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
	// Action identifies the bounded ACK mutation kind.
	Action string
	// Result identifies the bounded mutation outcome.
	Result string
	// Changed is the number of pending identities added or removed.
	Changed int
	// PendingCount is the exact owner-local gauge after the mutation.
	PendingCount int
}

// AckBatchEvent describes one owner-local pending recvack batch stage.
type AckBatchEvent struct {
	// Phase distinguishes aggregate bind and finish stages.
	Phase string
	// Outcome is the bounded aggregate stage result.
	Outcome string
	// Items is the item-aligned input size.
	Items int
	// Shards is the number of tracker shards touched by the stage.
	Shards int
	// Rejected is the number of invalid or capacity-rejected reservations.
	Rejected int
	// Rollback is the number of failed-write reservations canceled.
	Rollback int
	// Duration is aggregate transaction-stage latency.
	Duration time.Duration
}

// DeliveryErrorClass normalizes Online Delivery errors into bounded labels.
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

func bindAckBatchOutcome(bound, rejected int) string {
	switch {
	case bound == 0 && rejected > 0:
		return DeliveryAckBatchOutcomeRejected
	case rejected > 0:
		return DeliveryAckBatchOutcomePartial
	default:
		return DeliveryAckBatchOutcomeOK
	}
}

func finishAckBatchOutcome(finished, selected, rejected, rollback int) string {
	switch {
	case finished == selected && rejected == 0 && rollback == 0:
		return DeliveryAckBatchOutcomeOK
	case finished > 0:
		return DeliveryAckBatchOutcomePartial
	case rollback > 0:
		return DeliveryAckBatchOutcomeRolledBack
	case rejected > 0:
		return DeliveryAckBatchOutcomeRejected
	default:
		return DeliveryAckBatchOutcomeMiss
	}
}

func countBoundAckTokens(pending []PendingRecvAck, tokens []AckBindToken) int {
	count := 0
	for i := range pending {
		if i < len(tokens) && validPendingRecvAck(pending[i]) && tokens[i].Valid() {
			count++
		}
	}
	return count
}
