package message

import (
	"context"
	"time"

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
	groups := make([]senderAuthorityBatchGroup, 0, len(items))
	groupIndexes := make(map[authority.Target]int, len(items))
	for i, item := range items {
		prepared, target, result, done := r.prepareItem(item)
		if done {
			results[i] = result
			continue
		}
		groupIndex, ok := groupIndexes[target]
		if !ok {
			groupIndex = len(groups)
			groupIndexes[target] = groupIndex
			groups = append(groups, senderAuthorityBatchGroup{target: target})
		}
		groups[groupIndex].indexes = append(groups[groupIndex].indexes, i)
		groups[groupIndex].items = append(groups[groupIndex].items, prepared)
	}
	for _, group := range groups {
		r.submitGroup(group, results)
	}
	return results
}

type senderAuthorityBatchGroup struct {
	target  authority.Target
	indexes []int
	items   []SendBatchItem
}

func (r *SenderAuthorityRouter) prepareItem(item SendBatchItem) (SendBatchItem, authority.Target, SendBatchItemResult, bool) {
	ctx := item.Context
	if ctx == nil {
		ctx = context.Background()
		item.Context = ctx
	}
	if err := senderAuthorityItemError(item, time.Now()); err != nil {
		return item, authority.Target{}, SendBatchItemResult{Err: err}, true
	}
	if item.Command.FromUID == "" {
		return item, authority.Target{}, SendBatchItemResult{Result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if r == nil || r.resolver == nil {
		return item, authority.Target{}, SendBatchItemResult{Err: ErrRouteNotReady}, true
	}
	target, err := r.resolver.ResolveUIDAuthority(ctx, item.Command.FromUID)
	if err != nil {
		return item, authority.Target{}, SendBatchItemResult{Err: err}, true
	}
	if err := target.Validate(); err != nil {
		return item, authority.Target{}, SendBatchItemResult{Err: ErrRouteNotReady}, true
	}
	return item, target, SendBatchItemResult{}, false
}

func (r *SenderAuthorityRouter) submitGroup(group senderAuthorityBatchGroup, results []SendBatchItemResult) {
	if group.target.IsLocal(r.localNodeID) {
		if r.local == nil {
			fillSenderAuthorityGroupResults(results, group.indexes, ErrRouteNotReady)
			return
		}
		mergeSenderAuthorityGroupResults(results, group.indexes, r.local.SendBatch(group.items))
		return
	}
	if r.remote == nil {
		fillSenderAuthorityGroupResults(results, group.indexes, ErrRouteNotReady)
		return
	}
	ctx, cancel := senderAuthorityGroupContext(group.items)
	defer cancel()
	mergeSenderAuthorityGroupResults(results, group.indexes, r.remote.SendBatchToAuthority(ctx, group.target, group.items))
}

func mergeSenderAuthorityGroupResults(results []SendBatchItemResult, indexes []int, groupResults []SendBatchItemResult) {
	if len(groupResults) != len(indexes) {
		fillSenderAuthorityGroupResults(results, indexes, ErrAppendResultMissing)
		return
	}
	for i, result := range groupResults {
		results[indexes[i]] = result
	}
}

func fillSenderAuthorityGroupResults(results []SendBatchItemResult, indexes []int, err error) {
	for _, index := range indexes {
		results[index].Err = err
	}
}

func senderAuthorityItemError(item SendBatchItem, now time.Time) error {
	if item.Context != nil {
		if err := item.Context.Err(); err != nil {
			return err
		}
	}
	if !item.Deadline.IsZero() && !item.Deadline.After(now) {
		return context.DeadlineExceeded
	}
	return nil
}

func senderAuthorityGroupContext(items []SendBatchItem) (context.Context, context.CancelFunc) {
	base := context.Background()
	baseSet := false
	var earliest time.Time
	hasDeadline := false
	recordDeadline := func(deadline time.Time, ok bool) {
		if !ok || deadline.IsZero() {
			return
		}
		if !hasDeadline || deadline.Before(earliest) {
			earliest = deadline
			hasDeadline = true
		}
	}
	for _, item := range items {
		if !baseSet && item.Context != nil {
			base = item.Context
			baseSet = true
		}
		recordDeadline(item.Deadline, !item.Deadline.IsZero())
		if item.Context != nil {
			deadline, ok := item.Context.Deadline()
			recordDeadline(deadline, ok)
		}
	}
	if hasDeadline {
		return context.WithDeadline(base, earliest)
	}
	return context.WithCancel(base)
}
