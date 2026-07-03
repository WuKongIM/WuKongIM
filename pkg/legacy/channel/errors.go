package channel

import (
	"errors"

	channelcompat "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

var (
	ErrInvalidConfig           = errors.New("channel: invalid config")
	ErrInvalidArgument         = channelcompat.ErrInvalidArgument
	ErrInvalidMeta             = errors.New("channel: invalid metadata")
	ErrConflictingMeta         = errors.New("channel: conflicting metadata")
	ErrStaleMeta               = errors.New("channel: stale metadata")
	ErrNotLeader               = errors.New("channel: not leader")
	ErrNotReady                = errors.New("channel: not ready")
	ErrLeaseExpired            = errors.New("channel: lease expired")
	ErrWriteFenced             = errors.New("channel: write fenced")
	ErrInsufficientISR         = errors.New("channel: insufficient isr")
	ErrTombstoned              = errors.New("channel: tombstoned")
	ErrSnapshotRequired        = errors.New("channel: snapshot required")
	ErrChannelDeleting         = errors.New("channel: channel deleting")
	ErrChannelNotFound         = errors.New("channel: channel not found")
	ErrIdempotencyConflict     = errors.New("channel: idempotency conflict")
	ErrProtocolUpgradeRequired = errors.New("channel: protocol upgrade required")
	ErrMessageSeqExhausted     = errors.New("channel: legacy message seq exhausted")
	ErrMessageNotFound         = errors.New("channel: message not found")
	ErrInvalidFetchArgument    = errors.New("channel: invalid fetch argument")
	ErrInvalidFetchBudget      = errors.New("channel: invalid fetch budget")
	ErrNoSafeChannelLeader     = errors.New("channel: no safe leader candidate")
	ErrCorruptState            = channelcompat.ErrCorruptState
	ErrEmptyState              = channelcompat.ErrEmptyState
	ErrCorruptValue            = channelcompat.ErrCorruptValue

	errNotImplemented = errors.New("channel: not implemented")
)
