package channel

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// WillReceiptReader reads immutable evidence under exact recovered authority.
// Cluster callers must also establish fresh Slot authority around this operation.
type WillReceiptReader interface {
	ReadWillReceipt(context.Context, WillReceiptRequest) (WillReceiptResult, error)
}

// WillReceiptRequest selects a server-owned identity without allowing callers
// to choose committed progress or authorize a new publication.
type WillReceiptRequest struct {
	// ChannelID selects the immutable publication's owning log.
	ChannelID ChannelID
	// Every expected fence comes from the caller's fresh Slot metadata read.
	ExpectedChannelEpoch, ExpectedLeaderEpoch, ExpectedRouteGeneration uint64
	// FromUID and ServerWillKey select the server domain, never a client retry key.
	FromUID, ServerWillKey string
}

// Valid bounds identities before storage, queue admission or RPC encoding.
func (q WillReceiptRequest) Valid() bool {
	return q.ChannelID.ID != "" && len(q.ChannelID.ID) <= 1024 && utf8.ValidString(q.ChannelID.ID) &&
		!strings.ContainsRune(q.ChannelID.ID, 0) && q.ChannelID.Type != 0 && q.ExpectedChannelEpoch != 0 &&
		q.ExpectedLeaderEpoch != 0 && q.ExpectedRouteGeneration != 0 && q.FromUID != "" && len(q.FromUID) <= 65535 &&
		publication.ValidServerWillKey(q.ServerWillKey)
}

// WillReceiptResult retains the independently captured committed boundary.
// Found=false is an observation, never proof of nonpublication after uncertainty.
type WillReceiptResult struct {
	// CommittedThrough is captured by the serving reactor, not the request.
	CommittedThrough uint64
	// Found controls whether Receipt contains complete immutable evidence.
	Found   bool
	Receipt WillReceipt
}

// Valid rejects partial, uncommitted or internally inconsistent proof.
func (r WillReceiptResult) Valid() bool {
	if !r.Found {
		return r.Receipt == (WillReceipt{})
	}
	return r.Receipt.Valid() && r.Receipt.MessageSeq <= r.CommittedThrough
}
