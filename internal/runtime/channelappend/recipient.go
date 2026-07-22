package channelappend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const subscriberSnapshotLoadLimit = 1 << 30

type recipientDispatchResult struct {
	// subscriberCache carries a successfully loaded non-large recipient snapshot.
	subscriberCache subscriberCache
	// activeErr reports an independent conversation projection failure without failing delivery.
	activeErr error
}

type recipientSetDispatchResult struct {
	// activeErr reports the best-effort conversation projection outcome for this recipient set.
	activeErr error
}

type normalizedRecipientAuthoritySet struct {
	// recipients preserves the normalized delivery input order, including duplicate UIDs.
	recipients []Recipient
	// authorityUIDs contains each UID once for aligned authority resolution.
	authorityUIDs []string
	// authorityRecipient marks authorityUIDs entries that also receive delivery.
	authorityRecipient []bool
	// recipientAuthorityIndexes maps each recipient back to its authorityUIDs entry.
	recipientAuthorityIndexes []int
	// senderAuthorityIndex is the sender entry in authorityUIDs, or -1 when omitted.
	senderAuthorityIndex int
	// uniqueRecipientCount counts distinct normalized recipient UIDs.
	uniqueRecipientCount int
}

type recipientAuthorityGroup struct {
	// target is the exact fenced authority shared by this group.
	target RecipientAuthorityTarget
	// recipientCount sizes the delivery slice before the fill pass.
	recipientCount int
	// recipients preserves delivery order for UIDs owned by target.
	recipients []Recipient
	// activeCount sizes the conversation-active slice before the fill pass.
	activeCount int
	// activeRecipients contains recipient projections owned by target.
	activeRecipients []conversationactive.ActiveEntry
	// senderUID is populated only for the sender authority group.
	senderUID string
	// deliverySeen reports whether target has at least one delivery recipient.
	deliverySeen bool
	// activeSeen reports whether target participates in conversation projection.
	activeSeen bool
}

type recipientAuthorityGrouping struct {
	// groups stores one entry per distinct exact authority target.
	groups []recipientAuthorityGroup
	// deliveryOrder preserves first-seen target order for delivery dispatch.
	deliveryOrder []int
	// activeOrder preserves first-seen target order for conversation projection.
	activeOrder []int
	// activeReady reports whether every sender and recipient active route is usable.
	activeReady bool
}

func dispatchCommittedRecipients(ctx context.Context, event CommittedEnvelope, ports commitPorts) error {
	target := AuthorityTarget{
		ChannelID: ChannelID{ID: event.ChannelID, Type: event.ChannelType},
		Large:     true,
	}
	_, err := dispatchCommittedRecipientsForTarget(ctx, target, event, subscriberCache{}, ports)
	return err
}

func dispatchCommittedRecipientsForTarget(ctx context.Context, target AuthorityTarget, event CommittedEnvelope, cache subscriberCache, ports commitPorts) (recipientDispatchResult, error) {
	enqueuer := effectiveRecipientDeliveryEnqueuer(ports)
	if ports.activeAdmitter == nil && enqueuer == nil {
		return recipientDispatchResult{}, nil
	}
	if err := contextErr(ctx); err != nil {
		return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "context"})
	}
	if len(event.MessageScopedUIDs) > 0 {
		result, err := dispatchRecipientSetResult(ctx, event, recipientsFromUIDs(event.MessageScopedUIDs), ports)
		return recipientDispatchResult{activeErr: result.activeErr}, err
	}
	if event.ChannelType == channelTypePerson {
		left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
		if err != nil {
			return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "person_channel_decode"})
		}
		result, dispatchErr := dispatchRecipientSetResult(ctx, event, []Recipient{{UID: left}, {UID: right}}, ports)
		return recipientDispatchResult{activeErr: result.activeErr}, dispatchErr
	}
	if target.Large {
		return dispatchSubscriberPages(ctx, event, ports)
	}
	return dispatchSubscriberSnapshot(ctx, target, event, cache, ports)
}

func dispatchSubscriberPages(ctx context.Context, event CommittedEnvelope, ports commitPorts) (recipientDispatchResult, error) {
	if ports.subscribers == nil {
		return recipientDispatchResult{}, nil
	}
	pageSize := boundedPositive(ports.subscriberPageSize, defaultSubscriberScanPageSize)
	cursor := ""
	var result recipientDispatchResult
	for {
		previousCursor := cursor
		if err := contextErr(ctx); err != nil {
			return result, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "context"})
		}
		page, err := ports.subscribers.NextSubscriberPage(ctx, SubscriberPageRequest{
			ChannelID: ChannelID{ID: event.ChannelID, Type: event.ChannelType},
			Cursor:    cursor,
			Limit:     pageSize,
		})
		if err != nil {
			return result, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "subscriber_page"})
		}
		if !page.Done && (page.Cursor == "" || page.Cursor == previousCursor) {
			return result, withPostCommitFailureDetail(ErrInvalidSubscriberCursor, PostCommitFailureDetail{
				Phase:          "subscriber_cursor",
				RecipientCount: len(page.Recipients),
			})
		}
		pageResult, dispatchErr := dispatchRecipientSetResult(ctx, event, page.Recipients, ports)
		if result.activeErr == nil {
			result.activeErr = pageResult.activeErr
		}
		if dispatchErr != nil {
			return result, dispatchErr
		}
		if page.Done {
			return result, nil
		}
		cursor = page.Cursor
	}
}

func dispatchSubscriberSnapshot(ctx context.Context, target AuthorityTarget, event CommittedEnvelope, cache subscriberCache, ports commitPorts) (recipientDispatchResult, error) {
	if ports.subscribers == nil {
		return recipientDispatchResult{}, nil
	}
	if cache.matches(target) {
		result, err := dispatchRecipientSetResult(ctx, event, cache.recipients, ports)
		return recipientDispatchResult{subscriberCache: cache, activeErr: result.activeErr}, err
	}
	if err := contextErr(ctx); err != nil {
		return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "context"})
	}
	page, err := ports.subscribers.NextSubscriberPage(ctx, SubscriberPageRequest{
		ChannelID: ChannelID{ID: event.ChannelID, Type: event.ChannelType},
		Limit:     subscriberSnapshotLoadLimit,
	})
	if err != nil {
		return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "subscriber_snapshot"})
	}
	if !page.Done {
		return recipientDispatchResult{}, withPostCommitFailureDetail(ErrInvalidSubscriberCursor, PostCommitFailureDetail{
			Phase:          "subscriber_snapshot",
			RecipientCount: len(page.Recipients),
		})
	}
	nextCache := subscriberCache{
		ready:           true,
		mutationVersion: target.SubscriberMutationVersion,
		recipients:      append([]Recipient(nil), page.Recipients...),
	}
	dispatch, err := dispatchRecipientSetResult(ctx, event, page.Recipients, ports)
	if err != nil {
		return recipientDispatchResult{activeErr: dispatch.activeErr}, err
	}
	return recipientDispatchResult{subscriberCache: nextCache, activeErr: dispatch.activeErr}, nil
}

func dispatchRecipientSet(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) error {
	_, err := dispatchRecipientSetResult(ctx, event, recipients, ports)
	return err
}

func dispatchRecipientSetResult(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) (recipientSetDispatchResult, error) {
	enqueuer := effectiveRecipientDeliveryEnqueuer(ports)
	if len(recipients) == 0 || (ports.activeAdmitter == nil && enqueuer == nil) {
		return recipientSetDispatchResult{}, nil
	}
	routedActive, hasRoutedActive := ports.activeAdmitter.(RoutedConversationActiveAdmitter)
	normalized := normalizeRecipientsForAuthorityResolution(event.FromUID, recipients, hasRoutedActive)
	if len(normalized.recipients) == 0 {
		return recipientSetDispatchResult{}, nil
	}

	var (
		results    []RecipientAuthorityResult
		resolveErr error
		grouping   recipientAuthorityGrouping
		groupErr   error
	)
	if ports.recipientAuthorityResolver != nil && (enqueuer != nil || hasRoutedActive) {
		results, resolveErr = resolveRecipientAuthorityTargets(ctx, ports.recipientAuthorityResolver, normalized.authorityUIDs)
		if resolveErr == nil {
			grouping, groupErr = groupRecipientAuthorities(normalized, results, event.FromUID)
		}
	}

	var deliveryErr error
	if enqueuer != nil {
		switch {
		case ports.recipientAuthorityResolver == nil:
			deliveryErr = withPostCommitFailureDetail(errors.New("channelappend: recipient authority resolver required"), PostCommitFailureDetail{Phase: "recipient_route_resolve"})
		case resolveErr != nil:
			deliveryErr = withRecipientRouteResolveDetail(resolveErr, normalized)
		case groupErr != nil:
			deliveryErr = groupErr
		default:
			deliveryErr = dispatchRecipientDelivery(ctx, event, grouping, ports, enqueuer)
		}
	}

	var activeErr error
	if hasRoutedActive && resolveErr == nil && groupErr == nil && grouping.activeReady {
		activeErr = admitRoutedConversationActiveBatches(ctx, event, normalized, grouping, routedActive)
	} else {
		activeErr = admitConversationActiveBatch(ctx, event, normalized.recipients, normalized.uniqueRecipientCount, ports.activeAdmitter)
	}
	return recipientSetDispatchResult{activeErr: activeErr}, deliveryErr
}

func dispatchRecipientDelivery(ctx context.Context, event CommittedEnvelope, grouping recipientAuthorityGrouping, ports commitPorts, enqueuer RecipientDeliveryEnqueuer) error {
	if enqueuer == nil {
		return nil
	}
	batchSize := boundedPositive(ports.recipientBatchSize, defaultRecipientBatchSize)
	if planEnqueuer, ok := enqueuer.(RecipientDeliveryPlanEnqueuer); ok {
		return dispatchRecipientPlans(ctx, event, grouping.groups, grouping.deliveryOrder, batchSize, planEnqueuer)
	}
	concurrency := boundedPositive(ports.recipientDispatchConcurrency, 1)
	if concurrency <= 1 || len(grouping.deliveryOrder) <= 1 {
		for _, groupIndex := range grouping.deliveryOrder {
			group := grouping.groups[groupIndex]
			if err := dispatchRecipientTarget(ctx, event, group.target, group.recipients, batchSize, len(grouping.deliveryOrder), enqueuer); err != nil {
				return err
			}
		}
		return nil
	}
	return dispatchRecipientTargetsConcurrent(ctx, event, grouping.groups, grouping.deliveryOrder, batchSize, concurrency, enqueuer)
}

func dispatchRecipientPlans(
	ctx context.Context,
	event CommittedEnvelope,
	groups []recipientAuthorityGroup,
	order []int,
	batchSize int,
	enqueuer RecipientDeliveryPlanEnqueuer,
) error {
	planTargetCapacity := min(batchSize, len(order))
	plan := RecipientDeliveryPlan{Event: event, Targets: make([]RecipientTargetBatch, 0, planTargetCapacity)}
	flush := func() error {
		if plan.RecipientCount() == 0 {
			return nil
		}
		if err := enqueuer.EnqueueRecipientDeliveryPlan(ctx, plan); err != nil {
			target := plan.Targets[0].Target
			detail := postCommitTargetDetail(target)
			detail.Phase = "recipient_dispatch"
			detail.UID = firstRecipientUID(plan.Targets[0].Recipients)
			detail.RecipientCount = plan.RecipientCount()
			detail.DispatchTargetCount = len(plan.Targets)
			detail.DispatchBatchSize = plan.RecipientCount()
			return withPostCommitFailureDetail(err, detail)
		}
		plan = RecipientDeliveryPlan{Event: event, Targets: make([]RecipientTargetBatch, 0, planTargetCapacity)}
		return nil
	}

	remaining := batchSize
	for _, groupIndex := range order {
		group := groups[groupIndex]
		target := group.target
		recipients := group.recipients
		for len(recipients) > 0 {
			if remaining == 0 {
				if err := flush(); err != nil {
					return err
				}
				remaining = batchSize
			}
			n := remaining
			if n > len(recipients) {
				n = len(recipients)
			}
			plan.Targets = append(plan.Targets, RecipientTargetBatch{
				Target:     target,
				Recipients: append([]Recipient(nil), recipients[:n]...),
			})
			recipients = recipients[n:]
			remaining -= n
		}
	}
	return flush()
}

func effectiveRecipientDeliveryEnqueuer(ports commitPorts) RecipientDeliveryEnqueuer {
	return ports.deliveryEnqueuer
}

func admitConversationActiveBatch(ctx context.Context, event CommittedEnvelope, recipients []Recipient, uniqueRecipientCount int, admitter ConversationActiveAdmitter) error {
	if admitter == nil {
		return nil
	}
	entries := make([]conversationactive.ActiveEntry, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		entries = append(entries, conversationactive.ActiveEntry{UID: recipient.UID})
	}
	if len(entries) == 0 && event.FromUID == "" {
		return nil
	}
	batch := conversationactive.ActiveBatch{
		Kind:        conversationKindForCommittedEnvelope(event),
		SenderUID:   event.FromUID,
		ChannelID:   event.ChannelID,
		ChannelType: event.ChannelType,
		MessageSeq:  event.MessageSeq,
		ActiveAtMS:  event.ServerTimestampMS,
		Recipients:  entries,
	}
	if err := admitter.AdmitActiveBatch(ctx, batch); err != nil {
		return withPostCommitFailureDetail(err, PostCommitFailureDetail{
			Phase:          "conversation_active",
			UID:            firstRecipientUID(recipients),
			UIDCount:       uniqueRecipientCount,
			RecipientCount: len(recipients),
		})
	}
	return nil
}

func conversationKindForCommittedEnvelope(event CommittedEnvelope) metadb.ConversationKind {
	if event.SyncOnce || runtimechannelid.IsCommandChannel(event.ChannelID) {
		return metadb.ConversationKindCMD
	}
	return metadb.ConversationKindNormal
}

func normalizeRecipientsForAuthorityResolution(senderUID string, recipients []Recipient, includeSender bool) normalizedRecipientAuthoritySet {
	type uidState struct {
		index     int
		recipient bool
	}
	set := normalizedRecipientAuthoritySet{
		recipients:                make([]Recipient, 0, len(recipients)),
		authorityUIDs:             make([]string, 0, len(recipients)+1),
		authorityRecipient:        make([]bool, 0, len(recipients)+1),
		recipientAuthorityIndexes: make([]int, 0, len(recipients)),
		senderAuthorityIndex:      -1,
	}
	seen := make(map[string]uidState, len(recipients)+1)
	if includeSender && senderUID != "" {
		set.senderAuthorityIndex = 0
		set.authorityUIDs = append(set.authorityUIDs, senderUID)
		set.authorityRecipient = append(set.authorityRecipient, false)
		seen[senderUID] = uidState{index: 0}
	}
	for _, recipient := range recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			continue
		}
		recipient.UID = uid
		set.recipients = append(set.recipients, recipient)
		state, ok := seen[uid]
		if !ok {
			state.index = len(set.authorityUIDs)
			set.authorityUIDs = append(set.authorityUIDs, uid)
			set.authorityRecipient = append(set.authorityRecipient, true)
			state.recipient = true
			set.uniqueRecipientCount++
			seen[uid] = state
		} else if !state.recipient {
			state.recipient = true
			set.authorityRecipient[state.index] = true
			set.uniqueRecipientCount++
			seen[uid] = state
		}
		set.recipientAuthorityIndexes = append(set.recipientAuthorityIndexes, state.index)
	}
	return set
}

func resolveRecipientAuthorityTargets(ctx context.Context, resolver RecipientAuthorityResolver, uids []string) ([]RecipientAuthorityResult, error) {
	if batchResolver, ok := resolver.(BatchRecipientAuthorityResolver); ok {
		results, err := batchResolver.ResolveRecipientAuthorities(ctx, uids)
		if err != nil {
			return nil, err
		}
		if len(results) != len(uids) {
			return nil, fmt.Errorf("channelappend: aligned recipient authority result count %d does not match UID count %d: %w", len(results), len(uids), ErrRouteNotReady)
		}
		return results, nil
	}
	results := make([]RecipientAuthorityResult, len(uids))
	for index, uid := range uids {
		target, err := resolver.ResolveRecipientAuthority(ctx, uid)
		if err != nil {
			results[index].Err = err
			continue
		}
		results[index].Target = target
	}
	return results, nil
}

func groupRecipientAuthorities(set normalizedRecipientAuthoritySet, results []RecipientAuthorityResult, senderUID string) (recipientAuthorityGrouping, error) {
	grouping := recipientAuthorityGrouping{activeReady: len(results) == len(set.authorityUIDs)}
	if len(results) != len(set.authorityUIDs) {
		return grouping, fmt.Errorf("channelappend: aligned recipient authority result count %d does not match UID count %d: %w", len(results), len(set.authorityUIDs), ErrRouteNotReady)
	}
	groupIndex := make(map[RecipientAuthorityTarget]int, min(len(results), 256))
	authorityGroupIndexes := make([]int, len(results))
	for index := range authorityGroupIndexes {
		authorityGroupIndexes[index] = -1
	}
	ensureGroup := func(target RecipientAuthorityTarget) int {
		if index, ok := groupIndex[target]; ok {
			return index
		}
		index := len(grouping.groups)
		groupIndex[target] = index
		grouping.groups = append(grouping.groups, recipientAuthorityGroup{target: target})
		return index
	}
	for index, result := range results {
		if result.Err != nil || result.Target.Validate() != nil {
			grouping.activeReady = false
			continue
		}
		indexForGroup := ensureGroup(result.Target)
		authorityGroupIndexes[index] = indexForGroup
		group := &grouping.groups[indexForGroup]
		if !group.activeSeen {
			group.activeSeen = true
			grouping.activeOrder = append(grouping.activeOrder, indexForGroup)
		}
		if index == set.senderAuthorityIndex {
			group.senderUID = senderUID
		}
		if set.authorityRecipient[index] {
			group.activeCount++
		}
	}

	recipientGroupIndexes := make([]int, len(set.recipients))
	for index, recipient := range set.recipients {
		authorityIndex := set.recipientAuthorityIndexes[index]
		result := results[authorityIndex]
		if result.Err != nil {
			grouping.activeReady = false
			return grouping, withRecipientRouteResolveDetail(result.Err, set)
		}
		if err := result.Target.Validate(); err != nil {
			grouping.activeReady = false
			detail := postCommitTargetDetail(result.Target)
			detail.Phase = "recipient_target_validate"
			detail.UID = recipient.UID
			detail.UIDCount = set.uniqueRecipientCount
			detail.RecipientCount = len(set.recipients)
			return grouping, withPostCommitFailureDetail(ErrRouteNotReady, detail)
		}
		indexForGroup := authorityGroupIndexes[authorityIndex]
		recipientGroupIndexes[index] = indexForGroup
		group := &grouping.groups[indexForGroup]
		if !group.deliverySeen {
			group.deliverySeen = true
			grouping.deliveryOrder = append(grouping.deliveryOrder, indexForGroup)
		}
		group.recipientCount++
	}

	recipientStorage := make([]Recipient, len(set.recipients))
	recipientOffset := 0
	for index := range grouping.groups {
		count := grouping.groups[index].recipientCount
		if count == 0 {
			continue
		}
		end := recipientOffset + count
		grouping.groups[index].recipients = recipientStorage[recipientOffset:recipientOffset:end]
		recipientOffset = end
	}
	for index, recipient := range set.recipients {
		groupIndex := recipientGroupIndexes[index]
		grouping.groups[groupIndex].recipients = append(grouping.groups[groupIndex].recipients, recipient)
	}

	if grouping.activeReady {
		activeStorage := make([]conversationactive.ActiveEntry, set.uniqueRecipientCount)
		activeOffset := 0
		for index := range grouping.groups {
			count := grouping.groups[index].activeCount
			if count == 0 {
				continue
			}
			end := activeOffset + count
			grouping.groups[index].activeRecipients = activeStorage[activeOffset:activeOffset:end]
			activeOffset = end
		}
		for authorityIndex, recipient := range set.authorityRecipient {
			if !recipient {
				continue
			}
			groupIndex := authorityGroupIndexes[authorityIndex]
			grouping.groups[groupIndex].activeRecipients = append(grouping.groups[groupIndex].activeRecipients, conversationactive.ActiveEntry{UID: set.authorityUIDs[authorityIndex]})
		}
	}
	return grouping, nil
}

func withRecipientRouteResolveDetail(err error, set normalizedRecipientAuthoritySet) error {
	return withPostCommitFailureDetail(err, PostCommitFailureDetail{
		Phase:          "recipient_route_resolve",
		UID:            firstRecipientUID(set.recipients),
		UIDCount:       set.uniqueRecipientCount,
		RecipientCount: len(set.recipients),
	})
}

func admitRoutedConversationActiveBatches(ctx context.Context, event CommittedEnvelope, set normalizedRecipientAuthoritySet, grouping recipientAuthorityGrouping, admitter RoutedConversationActiveAdmitter) error {
	groups := make([]ConversationActiveTargetBatch, 0, len(grouping.activeOrder))
	for _, groupIndex := range grouping.activeOrder {
		group := grouping.groups[groupIndex]
		groups = append(groups, ConversationActiveTargetBatch{
			Target: group.target,
			Batch: conversationactive.ActiveBatch{
				Kind:        conversationKindForCommittedEnvelope(event),
				SenderUID:   group.senderUID,
				ChannelID:   event.ChannelID,
				ChannelType: event.ChannelType,
				MessageSeq:  event.MessageSeq,
				ActiveAtMS:  event.ServerTimestampMS,
				Recipients:  group.activeRecipients,
			},
		})
	}
	if len(groups) == 0 {
		return nil
	}
	if err := admitter.AdmitRoutedActiveBatches(ctx, groups); err != nil {
		return withPostCommitFailureDetail(err, PostCommitFailureDetail{
			Phase:          "conversation_active",
			UID:            firstRecipientUID(set.recipients),
			UIDCount:       set.uniqueRecipientCount,
			RecipientCount: len(set.recipients),
		})
	}
	return nil
}

func dispatchRecipientTarget(ctx context.Context, event CommittedEnvelope, target RecipientAuthorityTarget, recipients []Recipient, batchSize int, targetCount int, enqueuer RecipientDeliveryEnqueuer) error {
	for len(recipients) > 0 {
		n := batchSize
		if n > len(recipients) {
			n = len(recipients)
		}
		batch := RecipientBatch{
			Event:      event,
			Recipients: append([]Recipient(nil), recipients[:n]...),
		}
		if err := enqueuer.EnqueueRecipientBatch(ctx, target, batch); err != nil {
			detail := postCommitTargetDetail(target)
			detail.Phase = "recipient_dispatch"
			detail.UID = firstRecipientUID(batch.Recipients)
			detail.RecipientCount = len(recipients)
			detail.DispatchTargetCount = targetCount
			detail.DispatchBatchSize = len(batch.Recipients)
			return withPostCommitFailureDetail(err, detail)
		}
		recipients = recipients[n:]
	}
	return nil
}

func dispatchRecipientTargetsConcurrent(ctx context.Context, event CommittedEnvelope, groups []recipientAuthorityGroup, order []int, batchSize int, concurrency int, enqueuer RecipientDeliveryEnqueuer) error {
	if concurrency > len(order) {
		concurrency = len(order)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	targets := make(chan int)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for groupIndex := range targets {
			group := groups[groupIndex]
			if err := dispatchRecipientTarget(runCtx, event, group.target, group.recipients, batchSize, len(order), enqueuer); err != nil {
				select {
				case errs <- err:
					cancel()
				default:
				}
				return
			}
		}
	}
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}
	for _, groupIndex := range order {
		select {
		case targets <- groupIndex:
		case <-runCtx.Done():
			break
		}
		if runCtx.Err() != nil {
			break
		}
	}
	close(targets)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
	}
	return runCtx.Err()
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstRecipientUID(recipients []Recipient) string {
	if len(recipients) == 0 {
		return ""
	}
	return recipients[0].UID
}

func recipientsFromUIDs(uids []string) []Recipient {
	out := make([]Recipient, 0, len(uids))
	for _, uid := range uids {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		out = append(out, Recipient{UID: uid})
	}
	return out
}

func boundedPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
