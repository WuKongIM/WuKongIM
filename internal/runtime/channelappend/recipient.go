package channelappend

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const subscriberSnapshotLoadLimit = 1 << 30

type recipientDispatchResult struct {
	subscriberCache subscriberCache
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
		return recipientDispatchResult{}, dispatchRecipientSet(ctx, event, recipientsFromUIDs(event.MessageScopedUIDs), ports)
	}
	if event.ChannelType == channelTypePerson {
		left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
		if err != nil {
			return recipientDispatchResult{}, withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "person_channel_decode"})
		}
		return recipientDispatchResult{}, dispatchRecipientSet(ctx, event, []Recipient{{UID: left}, {UID: right}}, ports)
	}
	if target.Large {
		return recipientDispatchResult{}, dispatchSubscriberPages(ctx, event, ports)
	}
	return dispatchSubscriberSnapshot(ctx, target, event, cache, ports)
}

func dispatchSubscriberPages(ctx context.Context, event CommittedEnvelope, ports commitPorts) error {
	if ports.subscribers == nil {
		return nil
	}
	pageSize := boundedPositive(ports.subscriberPageSize, defaultSubscriberScanPageSize)
	cursor := ""
	for {
		previousCursor := cursor
		if err := contextErr(ctx); err != nil {
			return withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "context"})
		}
		page, err := ports.subscribers.NextSubscriberPage(ctx, SubscriberPageRequest{
			ChannelID: ChannelID{ID: event.ChannelID, Type: event.ChannelType},
			Cursor:    cursor,
			Limit:     pageSize,
		})
		if err != nil {
			return withPostCommitFailureDetail(err, PostCommitFailureDetail{Phase: "subscriber_page"})
		}
		if !page.Done && (page.Cursor == "" || page.Cursor == previousCursor) {
			return withPostCommitFailureDetail(ErrInvalidSubscriberCursor, PostCommitFailureDetail{
				Phase:          "subscriber_cursor",
				RecipientCount: len(page.Recipients),
			})
		}
		if err := dispatchRecipientSet(ctx, event, page.Recipients, ports); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		cursor = page.Cursor
	}
}

func dispatchSubscriberSnapshot(ctx context.Context, target AuthorityTarget, event CommittedEnvelope, cache subscriberCache, ports commitPorts) (recipientDispatchResult, error) {
	if ports.subscribers == nil {
		return recipientDispatchResult{}, nil
	}
	if cache.matches(target) {
		return recipientDispatchResult{}, dispatchRecipientSet(ctx, event, cache.recipients, ports)
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
	if err := dispatchRecipientSet(ctx, event, page.Recipients, ports); err != nil {
		return recipientDispatchResult{}, err
	}
	return recipientDispatchResult{subscriberCache: nextCache}, nil
}

func dispatchRecipientSet(ctx context.Context, event CommittedEnvelope, recipients []Recipient, ports commitPorts) error {
	enqueuer := effectiveRecipientDeliveryEnqueuer(ports)
	if len(recipients) == 0 || (ports.activeAdmitter == nil && enqueuer == nil) {
		return nil
	}
	batchSize := boundedPositive(ports.recipientBatchSize, defaultRecipientBatchSize)
	recipients, uids := normalizeRecipientsForAuthorityResolution(recipients)
	if len(recipients) == 0 {
		return nil
	}
	if err := admitConversationActiveBatch(ctx, event, recipients, uids, ports.activeAdmitter); err != nil {
		return err
	}
	if enqueuer == nil {
		return nil
	}
	if ports.recipientAuthorityResolver == nil {
		return withPostCommitFailureDetail(errors.New("channelappend: recipient authority resolver required"), PostCommitFailureDetail{Phase: "recipient_route_resolve"})
	}
	targets, err := resolveRecipientAuthorityTargets(ctx, ports.recipientAuthorityResolver, uids)
	if err != nil {
		return withPostCommitFailureDetail(err, PostCommitFailureDetail{
			Phase:          "recipient_route_resolve",
			UID:            firstString(uids),
			UIDCount:       len(uids),
			RecipientCount: len(recipients),
		})
	}
	grouped := make(map[RecipientAuthorityTarget][]Recipient)
	order := make([]RecipientAuthorityTarget, 0)
	for _, recipient := range recipients {
		target := targets[recipient.UID]
		if err := target.Validate(); err != nil {
			detail := postCommitTargetDetail(target)
			detail.Phase = "recipient_target_validate"
			detail.UID = recipient.UID
			detail.UIDCount = len(uids)
			detail.RecipientCount = len(recipients)
			return withPostCommitFailureDetail(ErrRouteNotReady, detail)
		}
		if _, ok := grouped[target]; !ok {
			order = append(order, target)
		}
		grouped[target] = append(grouped[target], recipient)
	}
	concurrency := boundedPositive(ports.recipientDispatchConcurrency, 1)
	if concurrency <= 1 || len(order) <= 1 {
		for _, target := range order {
			if err := dispatchRecipientTarget(ctx, event, target, grouped[target], batchSize, len(order), enqueuer); err != nil {
				return err
			}
		}
		return nil
	}
	return dispatchRecipientTargetsConcurrent(ctx, event, order, grouped, batchSize, concurrency, enqueuer)
}

func effectiveRecipientDeliveryEnqueuer(ports commitPorts) RecipientDeliveryEnqueuer {
	return ports.deliveryEnqueuer
}

func admitConversationActiveBatch(ctx context.Context, event CommittedEnvelope, recipients []Recipient, uids []string, admitter ConversationActiveAdmitter) error {
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
			UID:            firstString(uids),
			UIDCount:       len(uids),
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

func normalizeRecipientsForAuthorityResolution(recipients []Recipient) ([]Recipient, []string) {
	normalized := make([]Recipient, 0, len(recipients))
	uids := make([]string, 0, len(recipients))
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			continue
		}
		recipient.UID = uid
		normalized = append(normalized, recipient)
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		uids = append(uids, uid)
	}
	return normalized, uids
}

func resolveRecipientAuthorityTargets(ctx context.Context, resolver RecipientAuthorityResolver, uids []string) (map[string]RecipientAuthorityTarget, error) {
	if batchResolver, ok := resolver.(BatchRecipientAuthorityResolver); ok {
		targets, err := batchResolver.ResolveRecipientAuthorities(ctx, append([]string(nil), uids...))
		if err != nil {
			return nil, err
		}
		return targets, nil
	}
	targets := make(map[string]RecipientAuthorityTarget, len(uids))
	for _, uid := range uids {
		target, err := resolver.ResolveRecipientAuthority(ctx, uid)
		if err != nil {
			return nil, err
		}
		targets[uid] = target
	}
	return targets, nil
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

func dispatchRecipientTargetsConcurrent(ctx context.Context, event CommittedEnvelope, order []RecipientAuthorityTarget, grouped map[RecipientAuthorityTarget][]Recipient, batchSize int, concurrency int, enqueuer RecipientDeliveryEnqueuer) error {
	if concurrency > len(order) {
		concurrency = len(order)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	targets := make(chan RecipientAuthorityTarget)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for target := range targets {
			if err := dispatchRecipientTarget(runCtx, event, target, grouped[target], batchSize, len(order), enqueuer); err != nil {
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
	for _, target := range order {
		select {
		case targets <- target:
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
