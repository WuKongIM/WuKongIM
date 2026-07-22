package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// ConversationAuthorityNode is the cluster surface needed by authority routing.
type ConversationAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (cluster.Route, error)
	RouteKeysPartial([]string) ([]cluster.RouteKeyResult, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, cluster.NodeRPCHandler)
	WatchRouteAuthorities() <-chan cluster.RouteAuthorityEvent
}

// ConversationAuthorityClient routes conversation authority operations to UID leaders.
type ConversationAuthorityClient struct {
	node   ConversationAuthorityNode
	local  accessnode.ConversationAuthority
	remote *accessnode.Client

	routeRetryBackoff time.Duration
	routeRetrySleep   func(context.Context, time.Duration) error
}

var _ conversationusecase.Store = (*ConversationAuthorityClient)(nil)
var _ conversationusecase.DeleteStore = (*ConversationAuthorityClient)(nil)

const (
	defaultConversationRouteRetryAttempts = 100
	conversationAdmissionRetryAttempts    = 3
	defaultConversationRouteRetryBackoff  = 5 * time.Millisecond
	// maxConversationActiveBatchEnvelopeRows matches the bounded WKVC2 wire contract.
	maxConversationActiveBatchEnvelopeRows = 4096
	// maxConversationAuthorityDeleteGroup matches the bounded node RPC collection contract.
	// Validate before authority selection so local and remote leaders expose identical behavior.
	maxConversationAuthorityDeleteGroup = 4096
)

// NewConversationAuthorityClient creates a cluster-routed conversation authority client.
func NewConversationAuthorityClient(node ConversationAuthorityNode, local accessnode.ConversationAuthority) *ConversationAuthorityClient {
	return &ConversationAuthorityClient{
		node:              node,
		local:             local,
		remote:            accessnode.NewClient(node),
		routeRetryBackoff: defaultConversationRouteRetryBackoff,
	}
}

// AdmitPatches groups active patches by exact authority target and forwards each group.
func (c *ConversationAuthorityClient) AdmitPatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	groups, err := c.groupByTarget(patches)
	if err != nil {
		return err
	}
	for _, group := range groups {
		authority, err := c.authorityForTarget(group.target)
		if err != nil {
			return err
		}
		if err := authority.AdmitPatches(ctx, group.target, group.patches); err != nil {
			return err
		}
	}
	return nil
}

// AdmitActiveBatch routes a channelappend active batch to each affected UID authority.
func (c *ConversationAuthorityClient) AdmitActiveBatch(ctx context.Context, batch conversationactive.ActiveBatch) error {
	if batch.SenderUID == "" && len(batch.Recipients) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	pending := []conversationactive.ActiveBatch{batch}
	for attempt := 0; attempt < conversationAdmissionRetryAttempts; attempt++ {
		groups, routeFailures, err := c.groupActiveBatchesByTargetPartial(pending)
		if err != nil {
			if attempt+1 < conversationAdmissionRetryAttempts && shouldRetryConversationRouteLookup(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}

		groups = splitConversationActiveBatchGroups(groups, maxConversationActiveBatchEnvelopeRows)
		failed := make([]conversationactive.ActiveBatch, 0, len(routeFailures)+len(groups))
		var terminalErr error
		for _, failure := range routeFailures {
			if attempt+1 < conversationAdmissionRetryAttempts && shouldRetryConversationRouteLookup(failure.err) {
				failed = append(failed, failure.batch)
				continue
			}
			if terminalErr == nil {
				terminalErr = failure.err
			}
		}
		results := c.admitActiveBatchGroups(ctx, groups)
		for index, result := range results {
			if result.Err == nil {
				continue
			}
			if attempt+1 < conversationAdmissionRetryAttempts && shouldRetryConversationAdmission(result.Err) {
				failed = append(failed, cloneConversationActiveBatchForRetry(groups[index].batch))
				continue
			}
			if terminalErr == nil {
				terminalErr = result.Err
			}
		}
		if terminalErr != nil {
			return terminalErr
		}
		if len(failed) == 0 {
			return nil
		}
		if attempt+1 < conversationAdmissionRetryAttempts {
			if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
				return sleepErr
			}
			pending = failed
			continue
		}
	}
	return conversationusecase.ErrRouteNotReady
}

// ListConversationActiveView reads one UID's active view from the current authority leader.
func (c *ConversationAuthorityClient) ListConversationActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	var page conversationusecase.ActiveViewPage
	err := c.withFreshTarget(ctx, uid, func(target conversationusecase.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		page, err = authority.ListConversationActiveViewForTarget(ctx, target, kind, uid, after, limit)
		return err
	})
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return page, nil
}

// HideConversations routes durable delete barriers through the exact UID
// authority so the leader can reconcile its active cache before returning.
func (c *ConversationAuthorityClient) HideConversations(ctx context.Context, deletes []metadb.ConversationDelete) error {
	if len(deletes) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, group := range groupConversationDeletesByUID(deletes) {
		group := group
		if len(group.deletes) > maxConversationAuthorityDeleteGroup {
			return fmt.Errorf("internal/infra/cluster: conversation deletes for one UID exceed limit")
		}
		if err := c.withFreshTarget(ctx, group.uid, func(target conversationusecase.RouteTarget) error {
			authority, err := c.authorityForTarget(target)
			if err != nil {
				return err
			}
			return mapConversationRouteError(authority.HideConversationsForTarget(ctx, target, group.deletes))
		}); err != nil {
			return err
		}
	}
	return nil
}

// DrainAuthority drains one exact authority target for handoff.
func (c *ConversationAuthorityClient) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (string, error) {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return "", err
	}
	return authority.DrainAuthority(ctx, target)
}

type conversationAuthorityPatchGroup struct {
	target  conversationusecase.RouteTarget
	patches []conversationusecase.ActivePatch
}

type conversationAuthorityActiveBatchGroup struct {
	target conversationusecase.RouteTarget
	batch  conversationactive.ActiveBatch
}

type conversationAuthorityActiveBatchFailure struct {
	batch conversationactive.ActiveBatch
	err   error
}

type conversationAuthorityDeleteGroup struct {
	uid     string
	deletes []metadb.ConversationDelete
}

func groupConversationDeletesByUID(deletes []metadb.ConversationDelete) []conversationAuthorityDeleteGroup {
	groupIndex := make(map[string]int, len(deletes))
	groups := make([]conversationAuthorityDeleteGroup, 0, len(deletes))
	for _, req := range deletes {
		if idx, ok := groupIndex[req.UID]; ok {
			groups[idx].deletes = append(groups[idx].deletes, req)
			continue
		}
		groupIndex[req.UID] = len(groups)
		groups = append(groups, conversationAuthorityDeleteGroup{uid: req.UID, deletes: []metadb.ConversationDelete{req}})
	}
	return groups
}

func (c *ConversationAuthorityClient) groupByTarget(patches []conversationusecase.ActivePatch) ([]conversationAuthorityPatchGroup, error) {
	groupIndex := make(map[conversationusecase.RouteTarget]int)
	groups := make([]conversationAuthorityPatchGroup, 0, len(patches))
	for _, patch := range patches {
		target, err := c.resolve(patch.UID)
		if err != nil {
			return nil, err
		}
		if idx, ok := groupIndex[target]; ok {
			groups[idx].patches = append(groups[idx].patches, patch)
			continue
		}
		groupIndex[target] = len(groups)
		groups = append(groups, conversationAuthorityPatchGroup{
			target:  target,
			patches: []conversationusecase.ActivePatch{patch},
		})
	}
	return groups, nil
}

// groupActiveBatchesByTargetPartial routes every pending UID through one
// installed route snapshot. Key-specific lookup failures remain isolated so
// successful exact-target siblings can be admitted without being replayed.
func (c *ConversationAuthorityClient) groupActiveBatchesByTargetPartial(batches []conversationactive.ActiveBatch) ([]conversationAuthorityActiveBatchGroup, []conversationAuthorityActiveBatchFailure, error) {
	if c == nil || c.node == nil {
		return nil, nil, conversationusecase.ErrRouteNotReady
	}
	if len(batches) == 1 {
		return c.groupSingleActiveBatchByTargetPartial(batches[0])
	}
	uids := make([]string, 0)
	uidIndex := make(map[string]int)
	addUID := func(uid string) {
		if uid == "" {
			return
		}
		if _, ok := uidIndex[uid]; ok {
			return
		}
		uidIndex[uid] = len(uids)
		uids = append(uids, uid)
	}
	coalescedRecipients := make([][]conversationactive.ActiveEntry, len(batches))
	for index, batch := range batches {
		addUID(batch.SenderUID)
		coalescedRecipients[index] = coalesceConversationActiveRecipients(batch.Recipients)
		for _, recipient := range coalescedRecipients[index] {
			addUID(recipient.UID)
		}
	}
	if len(uids) == 0 {
		return nil, nil, nil
	}

	routes, err := c.node.RouteKeysPartial(uids)
	if err != nil {
		return nil, nil, mapConversationRouteError(err)
	}
	if len(routes) != len(uids) {
		return nil, nil, fmt.Errorf("%w: aligned route result count %d does not match UID count %d", conversationusecase.ErrRouteNotReady, len(routes), len(uids))
	}
	targets := make([]conversationusecase.RouteTarget, len(routes))
	routeErrs := make([]error, len(routes))
	for index, result := range routes {
		if result.Err != nil {
			routeErrs[index] = mapConversationRouteError(result.Err)
			continue
		}
		if result.Route.Leader == 0 {
			routeErrs[index] = fmt.Errorf("%w: route leader is unknown", conversationusecase.ErrRouteNotReady)
			continue
		}
		targets[index] = conversationRouteTargetFromClusterRoute(result.Route)
	}

	groups := make([]conversationAuthorityActiveBatchGroup, 0, len(uids))
	failures := make([]conversationAuthorityActiveBatchFailure, 0)
	for batchIndex, batch := range batches {
		groupIndex := make(map[conversationusecase.RouteTarget]int)
		failureIndex := make(map[string]int)
		addFailure := func(uid string, sender bool, recipient *conversationactive.ActiveEntry, routeErr error) {
			index, ok := failureIndex[uid]
			if !ok {
				index = len(failures)
				failureIndex[uid] = index
				failures = append(failures, conversationAuthorityActiveBatchFailure{
					batch: conversationActiveBatchMetadata(batch),
					err:   routeErr,
				})
			}
			if sender {
				failures[index].batch.SenderUID = uid
			}
			if recipient != nil {
				failures[index].batch.Recipients = append(failures[index].batch.Recipients, *recipient)
			}
		}
		addRouted := func(uid string, sender bool, recipient *conversationactive.ActiveEntry) {
			routeIndex := uidIndex[uid]
			if routeErrs[routeIndex] != nil {
				addFailure(uid, sender, recipient, routeErrs[routeIndex])
				return
			}
			target := targets[routeIndex]
			index := ensureConversationActiveBatchGroup(&groups, groupIndex, target, batch)
			if sender {
				groups[index].batch.SenderUID = uid
			}
			if recipient != nil {
				groups[index].batch.Recipients = append(groups[index].batch.Recipients, *recipient)
			}
		}
		if batch.SenderUID != "" {
			addRouted(batch.SenderUID, true, nil)
		}
		for recipientIndex := range coalescedRecipients[batchIndex] {
			recipient := &coalescedRecipients[batchIndex][recipientIndex]
			addRouted(recipient.UID, false, recipient)
		}
	}
	return groups, failures, nil
}

// singleActiveRouteItem coalesces one UID's sender and recipient roles before
// the single bulk route snapshot is resolved.
type singleActiveRouteItem struct {
	uid              string
	recipient        conversationactive.ActiveEntry
	hasRecipient     bool
	recipientEmitted bool
	isSender         bool
}

// singleActiveTargetGroup records exact capacity needed for one routed target.
type singleActiveTargetGroup struct {
	target         conversationusecase.RouteTarget
	recipientCount int
	senderUID      string
}

// groupSingleActiveBatchByTargetPartial avoids the retry-path intermediate
// copies on the normal one-batch admission path. It preserves first-appearance
// ordering, duplicate-recipient IsSender OR semantics, and aligned route
// failures while allocating recipient groups at their exact sizes.
func (c *ConversationAuthorityClient) groupSingleActiveBatchByTargetPartial(batch conversationactive.ActiveBatch) ([]conversationAuthorityActiveBatchGroup, []conversationAuthorityActiveBatchFailure, error) {
	itemCapacity := len(batch.Recipients) + 1
	items := make([]singleActiveRouteItem, 0, itemCapacity)
	uids := make([]string, 0, itemCapacity)
	itemIndex := make(map[string]int, itemCapacity)
	addItem := func(uid string, sender bool, recipient *conversationactive.ActiveEntry) {
		if uid == "" {
			return
		}
		index, ok := itemIndex[uid]
		if !ok {
			index = len(items)
			itemIndex[uid] = index
			items = append(items, singleActiveRouteItem{uid: uid})
			uids = append(uids, uid)
		}
		items[index].isSender = items[index].isSender || sender
		if recipient == nil {
			return
		}
		if items[index].hasRecipient {
			items[index].recipient.IsSender = items[index].recipient.IsSender || recipient.IsSender
			return
		}
		items[index].recipient = *recipient
		items[index].hasRecipient = true
	}
	addItem(batch.SenderUID, true, nil)
	for index := range batch.Recipients {
		recipient := &batch.Recipients[index]
		addItem(recipient.UID, false, recipient)
	}
	if len(items) == 0 {
		return nil, nil, nil
	}

	routes, err := c.node.RouteKeysPartial(uids)
	if err != nil {
		return nil, nil, mapConversationRouteError(err)
	}
	if len(routes) != len(items) {
		return nil, nil, fmt.Errorf("%w: aligned route result count %d does not match UID count %d", conversationusecase.ErrRouteNotReady, len(routes), len(items))
	}

	targetCapacity := len(items)
	if targetCapacity > 256 {
		targetCapacity = 256
	}
	targetIndex := make(map[conversationusecase.RouteTarget]int, targetCapacity)
	targetGroups := make([]singleActiveTargetGroup, 0, targetCapacity)
	var failures []conversationAuthorityActiveBatchFailure
	totalRecipientCount := 0
	for index, result := range routes {
		item := items[index]
		routeErr := result.Err
		if routeErr == nil && result.Route.Leader == 0 {
			routeErr = fmt.Errorf("%w: route leader is unknown", conversationusecase.ErrRouteNotReady)
		}
		if routeErr != nil {
			failed := conversationActiveBatchMetadata(batch)
			if item.isSender {
				failed.SenderUID = item.uid
			}
			if item.hasRecipient {
				failed.Recipients = append(failed.Recipients, item.recipient)
			}
			failures = append(failures, conversationAuthorityActiveBatchFailure{
				batch: failed,
				err:   mapConversationRouteError(routeErr),
			})
			continue
		}
		target := conversationRouteTargetFromClusterRoute(result.Route)
		groupIndex, ok := targetIndex[target]
		if !ok {
			groupIndex = len(targetGroups)
			targetIndex[target] = groupIndex
			targetGroups = append(targetGroups, singleActiveTargetGroup{target: target})
		}
		if item.isSender {
			targetGroups[groupIndex].senderUID = item.uid
		}
		if item.hasRecipient {
			targetGroups[groupIndex].recipientCount++
			totalRecipientCount++
		}
	}

	groups := make([]conversationAuthorityActiveBatchGroup, len(targetGroups))
	recipientStorage := make([]conversationactive.ActiveEntry, totalRecipientCount)
	recipientOffset := 0
	for index, targetGroup := range targetGroups {
		groupBatch := conversationActiveBatchMetadata(batch)
		groupBatch.SenderUID = targetGroup.senderUID
		if targetGroup.recipientCount > 0 {
			recipientEnd := recipientOffset + targetGroup.recipientCount
			groupBatch.Recipients = recipientStorage[recipientOffset:recipientOffset:recipientEnd]
			recipientOffset = recipientEnd
		}
		groups[index] = conversationAuthorityActiveBatchGroup{
			target: targetGroup.target,
			batch:  groupBatch,
		}
	}
	for _, recipient := range batch.Recipients {
		itemPosition, ok := itemIndex[recipient.UID]
		if !ok {
			continue
		}
		item := &items[itemPosition]
		if !item.hasRecipient || item.recipientEmitted {
			continue
		}
		result := routes[itemPosition]
		if result.Err != nil || result.Route.Leader == 0 {
			continue
		}
		target := conversationRouteTargetFromClusterRoute(result.Route)
		groupIndex := targetIndex[target]
		groups[groupIndex].batch.Recipients = append(groups[groupIndex].batch.Recipients, item.recipient)
		item.recipientEmitted = true
	}
	return groups, failures, nil
}

// cloneConversationActiveBatchForRetry detaches a failed group from shared
// happy-path recipient storage before the next route attempt retains it.
func cloneConversationActiveBatchForRetry(batch conversationactive.ActiveBatch) conversationactive.ActiveBatch {
	batch.Recipients = append([]conversationactive.ActiveEntry(nil), batch.Recipients...)
	return batch
}

func coalesceConversationActiveRecipients(recipients []conversationactive.ActiveEntry) []conversationactive.ActiveEntry {
	recipientIndex := make(map[string]int, len(recipients))
	coalesced := make([]conversationactive.ActiveEntry, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		if idx, ok := recipientIndex[recipient.UID]; ok {
			coalesced[idx].IsSender = coalesced[idx].IsSender || recipient.IsSender
			continue
		}
		recipientIndex[recipient.UID] = len(coalesced)
		coalesced = append(coalesced, recipient)
	}
	return coalesced
}

func ensureConversationActiveBatchGroup(groups *[]conversationAuthorityActiveBatchGroup, groupIndex map[conversationusecase.RouteTarget]int, target conversationusecase.RouteTarget, source conversationactive.ActiveBatch) int {
	if idx, ok := groupIndex[target]; ok {
		return idx
	}
	groupIndex[target] = len(*groups)
	*groups = append(*groups, conversationAuthorityActiveBatchGroup{
		target: target,
		batch:  conversationActiveBatchMetadata(source),
	})
	return len(*groups) - 1
}

func conversationActiveBatchMetadata(source conversationactive.ActiveBatch) conversationactive.ActiveBatch {
	return conversationactive.ActiveBatch{
		Kind:        source.Kind,
		ChannelID:   source.ChannelID,
		ChannelType: source.ChannelType,
		MessageSeq:  source.MessageSeq,
		ActiveAtMS:  source.ActiveAtMS,
	}
}

func splitConversationActiveBatchGroups(groups []conversationAuthorityActiveBatchGroup, maxRows int) []conversationAuthorityActiveBatchGroup {
	if len(groups) == 0 || maxRows <= 0 {
		return groups
	}
	result := make([]conversationAuthorityActiveBatchGroup, 0, len(groups))
	for _, group := range groups {
		if conversationActiveBatchRows(group.batch) <= maxRows {
			result = append(result, group)
			continue
		}
		remaining := group.batch.Recipients
		first := true
		for first || len(remaining) > 0 {
			chunk := conversationActiveBatchMetadata(group.batch)
			capacity := maxRows
			if first && group.batch.SenderUID != "" {
				chunk.SenderUID = group.batch.SenderUID
				capacity--
			}
			count := capacity
			if count > len(remaining) {
				count = len(remaining)
			}
			chunk.Recipients = remaining[:count]
			result = append(result, conversationAuthorityActiveBatchGroup{target: group.target, batch: chunk})
			remaining = remaining[count:]
			first = false
		}
	}
	return result
}

func conversationActiveBatchRows(batch conversationactive.ActiveBatch) int {
	rows := len(batch.Recipients)
	if batch.SenderUID != "" {
		rows++
	}
	return rows
}

// admitActiveBatchGroups sends one aligned bulk call per destination leader.
// Exact targets remain separate inside each envelope; leader grouping is only
// a transport optimization and never weakens a hash-slot authority fence.
func (c *ConversationAuthorityClient) admitActiveBatchGroups(ctx context.Context, groups []conversationAuthorityActiveBatchGroup) []accessnode.ConversationActiveBatchResult {
	results := make([]accessnode.ConversationActiveBatchResult, len(groups))
	if len(groups) == 0 {
		return results
	}
	type leaderBatch struct {
		indexes []int
		groups  []accessnode.ConversationActiveBatchGroup
		rows    int
	}
	byLeader := make(map[uint64][]*leaderBatch)
	leaderOrder := make([]uint64, 0)
	for index, group := range groups {
		leaderNodeID := group.target.LeaderNodeID
		if leaderNodeID == 0 {
			results[index].Err = fmt.Errorf("%w: active batch target leader is unknown", conversationusecase.ErrRouteNotReady)
			continue
		}
		batches := byLeader[leaderNodeID]
		rows := conversationActiveBatchRows(group.batch)
		if len(batches) == 0 {
			leaderOrder = append(leaderOrder, leaderNodeID)
		}
		if len(batches) == 0 || batches[len(batches)-1].rows+rows > maxConversationActiveBatchEnvelopeRows {
			batches = append(batches, &leaderBatch{})
			byLeader[leaderNodeID] = batches
		}
		batch := batches[len(batches)-1]
		batch.indexes = append(batch.indexes, index)
		batch.groups = append(batch.groups, accessnode.ConversationActiveBatchGroup{Target: group.target, Batch: group.batch})
		batch.rows += rows
	}

	for _, leaderNodeID := range leaderOrder {
		for _, batch := range byLeader[leaderNodeID] {
			if err := ctx.Err(); err != nil {
				fillConversationActiveBatchErrors(results, batch.indexes, err)
				continue
			}
			var batchResults []accessnode.ConversationActiveBatchResult
			if leaderNodeID == c.node.NodeID() {
				if c.local == nil {
					fillConversationActiveBatchErrors(results, batch.indexes, conversationusecase.ErrRouteNotReady)
					continue
				}
				if authority, ok := c.local.(accessnode.ConversationBatchAuthority); ok {
					batchResults = authority.AdmitActiveBatches(ctx, batch.groups)
				} else {
					batchResults = make([]accessnode.ConversationActiveBatchResult, len(batch.groups))
					for index, group := range batch.groups {
						batchResults[index].Err = c.local.AdmitActiveBatch(ctx, group.Target, group.Batch)
					}
				}
			} else {
				if c.remote == nil {
					fillConversationActiveBatchErrors(results, batch.indexes, conversationusecase.ErrRouteNotReady)
					continue
				}
				var err error
				batchResults, err = c.remote.AdmitConversationActiveBatches(ctx, leaderNodeID, batch.groups)
				if err != nil {
					fillConversationActiveBatchErrors(results, batch.indexes, mapConversationRouteError(err))
					continue
				}
			}
			if len(batchResults) != len(batch.indexes) {
				err := fmt.Errorf("%w: aligned active batch result count %d does not match group count %d", conversationusecase.ErrRouteNotReady, len(batchResults), len(batch.indexes))
				fillConversationActiveBatchErrors(results, batch.indexes, err)
				continue
			}
			for index, result := range batchResults {
				result.Err = mapConversationRouteError(result.Err)
				results[batch.indexes[index]] = result
			}
		}
	}
	return results
}

func fillConversationActiveBatchErrors(results []accessnode.ConversationActiveBatchResult, indexes []int, err error) {
	for _, index := range indexes {
		results[index].Err = err
	}
}

func (c *ConversationAuthorityClient) withFreshTarget(ctx context.Context, uid string, call func(conversationusecase.RouteTarget) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; attempt < defaultConversationRouteRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := c.resolve(uid)
		if err != nil {
			if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRouteLookup(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}
		if err := call(target); err != nil {
			if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRoute(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}
		return nil
	}
	return conversationusecase.ErrRouteNotReady
}

func (c *ConversationAuthorityClient) resolve(uid string) (conversationusecase.RouteTarget, error) {
	if c == nil || c.node == nil {
		return conversationusecase.RouteTarget{}, conversationusecase.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return conversationusecase.RouteTarget{}, mapConversationRouteError(err)
	}
	if route.Leader == 0 {
		return conversationusecase.RouteTarget{}, fmt.Errorf("%w: route leader is unknown", conversationusecase.ErrRouteNotReady)
	}
	return conversationRouteTargetFromClusterRoute(route), nil
}

func (c *ConversationAuthorityClient) authorityForTarget(target conversationusecase.RouteTarget) (accessnode.ConversationAuthority, error) {
	if c == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	if c.node == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	if target.LeaderNodeID == c.node.NodeID() {
		if c.local == nil {
			return nil, conversationusecase.ErrRouteNotReady
		}
		return c.local, nil
	}
	if c.remote == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	return remoteConversationAuthority{client: c.remote, nodeID: target.LeaderNodeID}, nil
}

func (c *ConversationAuthorityClient) sleepBeforeRouteRetry(ctx context.Context) error {
	if c == nil || c.routeRetryBackoff <= 0 {
		return nil
	}
	if c.routeRetrySleep != nil {
		return c.routeRetrySleep(ctx, c.routeRetryBackoff)
	}
	timer := time.NewTimer(c.routeRetryBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func conversationRouteTargetFromClusterRoute(route cluster.Route) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		LeaderTerm:     route.LeaderTerm,
		ConfigEpoch:    route.ConfigEpoch,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func shouldRetryConversationRoute(err error) bool {
	return errors.Is(err, conversationusecase.ErrStaleRoute) ||
		errors.Is(err, conversationusecase.ErrNotLeader) ||
		errors.Is(err, conversationusecase.ErrRouteNotReady)
}

func shouldRetryConversationAdmission(err error) bool {
	return shouldRetryConversationRoute(err) || isRetryableConversationTransportError(err)
}

func isRetryableConversationTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, transport.ErrCanceled) {
		return false
	}
	switch {
	case errors.Is(err, transport.ErrStopped),
		errors.Is(err, transport.ErrTimeout),
		errors.Is(err, transport.ErrNodeNotFound),
		errors.Is(err, transport.ErrQueueFull),
		errors.Is(err, transport.ErrDialFailed),
		errors.Is(err, transport.ErrBusy),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.EPIPE):
		return true
	default:
		return false
	}
}

func shouldRetryConversationRouteLookup(err error) bool {
	return shouldRetryConversationRoute(err)
}

func mapConversationRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, cluster.ErrRouteNotReady), errors.Is(err, cluster.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", conversationusecase.ErrRouteNotReady, err)
	case errors.Is(err, cluster.ErrNotLeader), errors.Is(err, propose.ErrNotLeader):
		return fmt.Errorf("%w: %w", conversationusecase.ErrNotLeader, err)
	case errors.Is(err, propose.ErrProposalBackpressure), errors.Is(err, propose.ErrBackgroundProposalThrottled):
		return fmt.Errorf("%w: %w", conversationusecase.ErrRouteNotReady, err)
	default:
		return err
	}
}

type remoteConversationAuthority struct {
	client *accessnode.Client
	nodeID uint64
}

func (a remoteConversationAuthority) AdmitPatches(ctx context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ErrRouteNotReady
	}
	return mapConversationRouteError(a.client.AdmitConversationPatches(ctx, a.nodeID, target, patches))
}

func (a remoteConversationAuthority) AdmitActiveBatch(ctx context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ErrRouteNotReady
	}
	return mapConversationRouteError(a.client.AdmitConversationActiveBatch(ctx, a.nodeID, target, batch))
}

func (a remoteConversationAuthority) ListConversationActiveViewForTarget(ctx context.Context, target conversationusecase.RouteTarget, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	page, err := a.client.ListConversations(ctx, a.nodeID, target, kind, uid, after, limit)
	return page, mapConversationRouteError(err)
}

func (a remoteConversationAuthority) HideConversationsForTarget(ctx context.Context, target conversationusecase.RouteTarget, deletes []metadb.ConversationDelete) error {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ErrRouteNotReady
	}
	return mapConversationRouteError(a.client.HideConversations(ctx, a.nodeID, target, deletes))
}

func (a remoteConversationAuthority) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (string, error) {
	if a.client == nil || a.nodeID == 0 {
		return "", conversationusecase.ErrRouteNotReady
	}
	result, err := a.client.DrainConversationAuthority(ctx, a.nodeID, target)
	return result, mapConversationRouteError(err)
}
