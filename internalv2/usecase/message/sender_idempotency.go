package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery = channelwrite.IdempotencyQuery

// SendIdempotencyLookup recovers a previously committed send result.
type SendIdempotencyLookup interface {
	// LookupSend returns a prior result for a canonical sender/client key.
	LookupSend(context.Context, IdempotencyQuery) (SendResult, bool, error)
}
