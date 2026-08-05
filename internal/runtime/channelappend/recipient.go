package channelappend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const subscriberSnapshotLoadLimit = 1 << 30

const (
	inlineRecipientAuthorityUIDLimit = 512
	inlineRecipientAuthorityUIDSlots = 1024
)

type inlineRecipientAuthorityUIDTable struct {
	// hashes stores the allocation-free FNV-1a hash for each occupied probe slot.
	hashes [inlineRecipientAuthorityUIDSlots]uint64
	// positions stores the authority UID index plus one; zero marks an empty probe slot.
	positions [inlineRecipientAuthorityUIDSlots]uint16
}

// lookupOrInsert resolves one UID against the inline table and records nextIndex when absent.
func (t *inlineRecipientAuthorityUIDTable) lookupOrInsert(uid string, authorityUIDs []string, nextIndex int) (int, bool) {
	hash := hashString64(uid)
	position := int(hash & (inlineRecipientAuthorityUIDSlots - 1))
	for {
		indexPlusOne := t.positions[position]
		if indexPlusOne == 0 {
			t.hashes[position] = hash
			t.positions[position] = uint16(nextIndex + 1)
			return nextIndex, false
		}
		index := int(indexPlusOne - 1)
		if t.hashes[position] == hash && authorityUIDs[index] == uid {
			return index, true
		}
		position = (position + 1) & (inlineRecipientAuthorityUIDSlots - 1)
	}
}

type recipientDispatchResult struct {
	// subscriberCache carries a successfully loaded non-large recipient snapshot.
	subscriberCache subscriberCache
}

type recipientSetDispatchResult struct{}

type normalizedRecipientAuthoritySet struct {
	// recipients preserves the normalized delivery input order, including duplicate UIDs.
	recipients []Recipient
	// authorityUIDs contains each UID once for aligned authority resolution.
	authorityUIDs []string
	// authorityRecipient marks authorityUIDs entries that also receive delivery.
	authorityRecipient []bool
	// recipientAuthorityIndexes maps each recipient back to its authorityUIDs entry.
	recipientAuthorityIndexes []int
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
	// deliverySeen reports whether target has at least one delivery recipient.
	deliverySeen bool
}

type recipientAuthorityGrouping struct {
	// groups stores one entry per distinct exact authority target.
	groups []recipientAuthorityGroup
	// deliveryOrder preserves first-seen target order for delivery dispatch.
	deliveryOrder []int
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
	return dispatchRecipientsForTarget(ctx, onlinedelivery.ModeDurable, target, event, cache, ports)
}

func dispatchRecipientsForTarget(ctx context.Context, mode onlinedelivery.Mode, target AuthorityTarget, event CommittedEnvelope, cache subscriberCache, ports commitPorts) (recipientDispatchResult, error) {
	enqueuer := ports.deliveryEnqueuer
	if enqueuer == nil {
		return recipientDispatchResult{}, nil
	}
	if err := contextErr(ctx); err != nil {
		return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "context"})
	}
	if len(event.MessageScopedUIDs) > 0 {
		_, err := dispatchRecipientSetResultForMode(ctx, mode, event, recipientsFromUIDs(event.MessageScopedUIDs), ports)
		return recipientDispatchResult{}, err
	}
	if event.ChannelType == channelTypePerson {
		left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
		if err != nil {
			return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "person_channel_decode"})
		}
		_, dispatchErr := dispatchRecipientSetResultForMode(ctx, mode, event, []Recipient{{UID: left}, {UID: right}}, ports)
		return recipientDispatchResult{}, dispatchErr
	}
	if target.Large {
		return dispatchSubscriberPages(ctx, mode, event, ports)
	}
	return dispatchSubscriberSnapshot(ctx, mode, target, event, cache, ports)
}

func dispatchSubscriberPages(ctx context.Context, mode onlinedelivery.Mode, event CommittedEnvelope, ports commitPorts) (recipientDispatchResult, error) {
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
		_, dispatchErr := dispatchRecipientSetResultForMode(ctx, mode, event, page.Recipients, ports)
		if dispatchErr != nil {
			return result, dispatchErr
		}
		if page.Done {
			return result, nil
		}
		cursor = page.Cursor
	}
}

func dispatchSubscriberSnapshot(ctx context.Context, mode onlinedelivery.Mode, target AuthorityTarget, event CommittedEnvelope, cache subscriberCache, ports commitPorts) (recipientDispatchResult, error) {
	if ports.subscribers == nil {
		return recipientDispatchResult{}, nil
	}
	if cache.matches(target) {
		_, err := dispatchRecipientSetResultForMode(ctx, mode, event, cache.recipients, ports)
		return recipientDispatchResult{subscriberCache: cache}, err
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
	_, err = dispatchRecipientSetResultForMode(ctx, mode, event, page.Recipients, ports)
	if err != nil {
		return recipientDispatchResult{}, err
	}
	return recipientDispatchResult{subscriberCache: nextCache}, nil
}

func dispatchRecipientSet(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) error {
	_, err := dispatchRecipientSetResult(ctx, event, recipients, ports)
	return err
}

func dispatchRecipientSetResult(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) (recipientSetDispatchResult, error) {
	return dispatchRecipientSetResultForMode(ctx, onlinedelivery.ModeDurable, event, recipients, ports)
}

func dispatchRecipientSetResultForMode(ctx context.Context, mode onlinedelivery.Mode, event CommittedEnvelope, recipients []Recipient, ports commitPorts) (recipientSetDispatchResult, error) {
	enqueuer := ports.deliveryEnqueuer
	if len(recipients) == 0 || enqueuer == nil {
		return recipientSetDispatchResult{}, nil
	}
	normalized := normalizeRecipientsForAuthorityResolution(event.FromUID, recipients, false)
	if len(normalized.recipients) == 0 {
		return recipientSetDispatchResult{}, nil
	}

	var (
		results    []RecipientAuthorityResult
		resolveErr error
		grouping   recipientAuthorityGrouping
		groupErr   error
	)
	if ports.recipientAuthorityResolver != nil {
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
			deliveryErr = dispatchRecipientDelivery(ctx, mode, event, grouping, ports, enqueuer)
		}
	}

	return recipientSetDispatchResult{}, deliveryErr
}

func dispatchRecipientDelivery(ctx context.Context, mode onlinedelivery.Mode, event CommittedEnvelope, grouping recipientAuthorityGrouping, ports commitPorts, enqueuer OnlineDeliveryEnqueuer) error {
	if enqueuer == nil {
		return nil
	}
	batchSize := boundedPositive(ports.recipientBatchSize, defaultRecipientBatchSize)
	return dispatchRecipientPlans(ctx, mode, event, grouping.groups, grouping.deliveryOrder, batchSize, enqueuer)
}

func dispatchRecipientPlans(
	ctx context.Context,
	mode onlinedelivery.Mode,
	event CommittedEnvelope,
	groups []recipientAuthorityGroup,
	order []int,
	batchSize int,
	enqueuer OnlineDeliveryEnqueuer,
) error {
	planTargetCapacity := min(batchSize, len(order))
	plan := onlinedelivery.RecipientDeliveryPlan{Mode: mode, Event: event, Targets: make([]onlinedelivery.RecipientTargetBatch, 0, planTargetCapacity)}
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
		plan = onlinedelivery.RecipientDeliveryPlan{Mode: mode, Event: event, Targets: make([]onlinedelivery.RecipientTargetBatch, 0, planTargetCapacity)}
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
			plan.Targets = append(plan.Targets, onlinedelivery.RecipientTargetBatch{
				Target: target,
				// Grouping already owns this normalized recipient storage. A
				// capacity-limited view can transfer to the async plan without
				// another copy and cannot overwrite a sibling target window.
				Recipients: recipients[:n:n],
			})
			recipients = recipients[n:]
			remaining -= n
		}
	}
	return flush()
}

func normalizeRecipientsForAuthorityResolution(_ string, recipients []Recipient, _ bool) normalizedRecipientAuthoritySet {
	set := normalizedRecipientAuthoritySet{
		authorityUIDs:             make([]string, 0, len(recipients)),
		authorityRecipient:        make([]bool, 0, len(recipients)),
		recipientAuthorityIndexes: make([]int, 0, len(recipients)),
	}
	copyRecipients := false
	var inlineUIDs inlineRecipientAuthorityUIDTable
	var seen map[string]int
	if len(recipients) > inlineRecipientAuthorityUIDLimit {
		seen = make(map[string]int, len(recipients))
	}
	for recipientIndex, recipient := range recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			if !copyRecipients {
				set.recipients = make([]Recipient, 0, len(recipients))
				set.recipients = append(set.recipients, recipients[:recipientIndex]...)
				copyRecipients = true
			}
			continue
		}
		if uid != recipient.UID && !copyRecipients {
			set.recipients = make([]Recipient, 0, len(recipients))
			set.recipients = append(set.recipients, recipients[:recipientIndex]...)
			copyRecipients = true
		}
		if copyRecipients {
			recipient.UID = uid
			set.recipients = append(set.recipients, recipient)
		}
		var (
			authorityIndex int
			ok             bool
		)
		if seen != nil {
			authorityIndex, ok = seen[uid]
		} else {
			authorityIndex, ok = inlineUIDs.lookupOrInsert(uid, set.authorityUIDs, len(set.authorityUIDs))
		}
		if !ok {
			authorityIndex = len(set.authorityUIDs)
			set.authorityUIDs = append(set.authorityUIDs, uid)
			set.authorityRecipient = append(set.authorityRecipient, true)
			set.uniqueRecipientCount++
			if seen != nil {
				seen[uid] = authorityIndex
			}
		} else if !set.authorityRecipient[authorityIndex] {
			set.authorityRecipient[authorityIndex] = true
			set.uniqueRecipientCount++
		}
		set.recipientAuthorityIndexes = append(set.recipientAuthorityIndexes, authorityIndex)
	}
	if !copyRecipients {
		// The caller retains ownership; downstream grouping only reads this normalized view.
		set.recipients = recipients
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

func groupRecipientAuthorities(set normalizedRecipientAuthoritySet, results []RecipientAuthorityResult, _ string) (recipientAuthorityGrouping, error) {
	groupCapacity := recipientAuthorityGroupCapacity(results)
	grouping := recipientAuthorityGrouping{
		groups:        make([]recipientAuthorityGroup, 0, groupCapacity),
		deliveryOrder: make([]int, 0, groupCapacity),
	}
	if len(results) != len(set.authorityUIDs) {
		return grouping, fmt.Errorf("channelappend: aligned recipient authority result count %d does not match UID count %d: %w", len(results), len(set.authorityUIDs), ErrRouteNotReady)
	}
	// The default physical hash-slot table is 256 entries. The fixed first-group
	// index removes a target-keyed map from the hot path while the exact-target
	// scan preserves semantics for custom slot counts and transition collisions.
	var firstGroupByHashSlot [256]uint32
	authorityGroupIndexes := make([]int, len(results))
	for index := range authorityGroupIndexes {
		authorityGroupIndexes[index] = -1
	}
	ensureGroup := func(target RecipientAuthorityTarget) int {
		hashSlot := int(target.HashSlot)
		if hashSlot < len(firstGroupByHashSlot) {
			if position := firstGroupByHashSlot[hashSlot]; position != 0 {
				index := int(position - 1)
				if grouping.groups[index].target == target {
					return index
				}
				for index := range grouping.groups {
					if grouping.groups[index].target == target {
						return index
					}
				}
			}
		} else {
			for index := range grouping.groups {
				if grouping.groups[index].target == target {
					return index
				}
			}
		}
		index := len(grouping.groups)
		grouping.groups = append(grouping.groups, recipientAuthorityGroup{target: target})
		if hashSlot < len(firstGroupByHashSlot) && firstGroupByHashSlot[hashSlot] == 0 {
			firstGroupByHashSlot[hashSlot] = uint32(index + 1)
		}
		return index
	}
	for index, result := range results {
		if result.Err != nil || result.Target.Validate() != nil {
			continue
		}
		indexForGroup := ensureGroup(result.Target)
		authorityGroupIndexes[index] = indexForGroup
	}

	recipientGroupIndexes := make([]int, len(set.recipients))
	for index, recipient := range set.recipients {
		authorityIndex := set.recipientAuthorityIndexes[index]
		result := results[authorityIndex]
		if result.Err != nil {
			return grouping, withRecipientRouteResolveDetail(result.Err, set)
		}
		if err := result.Target.Validate(); err != nil {
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
		// Reserve an empty, group-capped window so appends fill only this group's range.
		grouping.groups[index].recipients = recipientStorage[recipientOffset:recipientOffset:end]
		recipientOffset = end
	}
	for index, recipient := range set.recipients {
		groupIndex := recipientGroupIndexes[index]
		grouping.groups[groupIndex].recipients = append(grouping.groups[groupIndex].recipients, recipient)
	}

	return grouping, nil
}

func recipientAuthorityGroupCapacity(results []RecipientAuthorityResult) int {
	var occupiedPhysicalSlots [256]bool
	capacity := 0
	for _, result := range results {
		if result.Err != nil || result.Target.Validate() != nil {
			continue
		}
		hashSlot := int(result.Target.HashSlot)
		if hashSlot >= len(occupiedPhysicalSlots) {
			// Custom physical slot tables are uncommon and may contain exact
			// duplicates. A slight overestimate is preferable to a map here.
			capacity++
			continue
		}
		if occupiedPhysicalSlots[hashSlot] {
			continue
		}
		occupiedPhysicalSlots[hashSlot] = true
		capacity++
	}
	return capacity
}

func withRecipientRouteResolveDetail(err error, set normalizedRecipientAuthoritySet) error {
	return withPostCommitFailureDetail(err, PostCommitFailureDetail{
		Phase:          "recipient_route_resolve",
		UID:            firstRecipientUID(set.recipients),
		UIDCount:       set.uniqueRecipientCount,
		RecipientCount: len(set.recipients),
	})
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
