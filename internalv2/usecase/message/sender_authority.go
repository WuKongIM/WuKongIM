package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

// UIDAuthorityResolver resolves the authoritative hash-slot target for a sender UID.
type UIDAuthorityResolver interface {
	ResolveUIDAuthority(context.Context, string) (authority.Target, error)
}

// SenderAuthoritySubmitter submits sends that are authoritative on this node.
type SenderAuthoritySubmitter interface {
	SendBatch([]SendBatchItem) []SendBatchItemResult
}

// RemoteSenderAuthority forwards sends to a remote sender-authority target.
type RemoteSenderAuthority interface {
	SendBatchToAuthority(context.Context, authority.Target, []SendBatchItem) []SendBatchItemResult
}

// SenderAuthorityRouterOptions configures sender-authority routing.
type SenderAuthorityRouterOptions struct {
	// LocalNodeID is this node's cluster identity.
	LocalNodeID uint64
	// Resolver resolves the sender UID authority target.
	Resolver UIDAuthorityResolver
	// Local handles sends when this node owns sender authority.
	Local SenderAuthoritySubmitter
	// Remote forwards sends when another node owns sender authority.
	Remote RemoteSenderAuthority
}

// SenderAuthorityRouter routes sends through the resolved sender authority.
type SenderAuthorityRouter struct {
	localNodeID uint64
	resolver    UIDAuthorityResolver
	local       SenderAuthoritySubmitter
	remote      RemoteSenderAuthority
}

// NewSenderAuthorityRouter creates a sender-authority router.
func NewSenderAuthorityRouter(opts SenderAuthorityRouterOptions) *SenderAuthorityRouter {
	return &SenderAuthorityRouter{
		localNodeID: opts.LocalNodeID,
		resolver:    opts.Resolver,
		local:       opts.Local,
		remote:      opts.Remote,
	}
}

// Send routes one send command through sender authority.
func (r *SenderAuthorityRouter) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := r.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch routes send commands and returns item-aligned results.
func (r *SenderAuthorityRouter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	for i, item := range items {
		results[i] = r.sendOne(item)
	}
	return results
}

func (r *SenderAuthorityRouter) sendOne(item SendBatchItem) SendBatchItemResult {
	ctx := item.Context
	if ctx == nil {
		ctx = context.Background()
		item.Context = ctx
	}
	if item.Command.FromUID == "" {
		return SendBatchItemResult{Result: SendResult{Reason: ReasonAuthFail}}
	}
	if r == nil || r.resolver == nil {
		return SendBatchItemResult{Err: ErrRouteNotReady}
	}
	target, err := r.resolver.ResolveUIDAuthority(ctx, item.Command.FromUID)
	if err != nil {
		return SendBatchItemResult{Err: err}
	}
	if err := target.Validate(); err != nil {
		return SendBatchItemResult{Err: ErrRouteNotReady}
	}
	if target.IsLocal(r.localNodeID) {
		if r.local == nil {
			return SendBatchItemResult{Err: ErrRouteNotReady}
		}
		return singleSenderAuthorityResult(r.local.SendBatch([]SendBatchItem{item}))
	}
	if r.remote == nil {
		return SendBatchItemResult{Err: ErrRouteNotReady}
	}
	return singleSenderAuthorityResult(r.remote.SendBatchToAuthority(ctx, target, []SendBatchItem{item}))
}

func singleSenderAuthorityResult(results []SendBatchItemResult) SendBatchItemResult {
	if len(results) != 1 {
		return SendBatchItemResult{Err: ErrAppendResultMissing}
	}
	return results[0]
}
