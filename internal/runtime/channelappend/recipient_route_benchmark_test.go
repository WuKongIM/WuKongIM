package channelappend

import (
	"context"
	"strconv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
)

var (
	benchmarkLegacyRecipientAuthoritySink benchmarkLegacyRecipientAuthorityPlan
	benchmarkAlignedRecipientGroupingSink recipientAuthorityGrouping
	benchmarkRoutedActiveGroupsSink       []ConversationActiveTargetBatch
)

// benchmarkRecipientAuthorityInput holds immutable route-source data outside
// the timed section. Both benchmark variants still allocate their resolver
// result surface, matching the application boundary used in production.
type benchmarkRecipientAuthorityInput struct {
	senderUID  string
	recipients []Recipient
	targets    map[string]RecipientAuthorityTarget
}

// benchmarkLegacyRecipientAuthorityPlan mirrors the removed two-stage shape:
// delivery consumes a UID-keyed map, then conversation activity independently
// rebuilds an aligned UID snapshot and exact-target groups.
type benchmarkLegacyRecipientAuthorityPlan struct {
	deliveryOrder  []RecipientAuthorityTarget
	deliveryGroups map[RecipientAuthorityTarget][]Recipient
	activeGroups   []ConversationActiveTargetBatch
}

type benchmarkLegacyActiveItem struct {
	uid          string
	recipient    conversationactive.ActiveEntry
	hasRecipient bool
	isSender     bool
}

type benchmarkLegacyActiveTargetCount struct {
	target         RecipientAuthorityTarget
	recipientCount int
	senderUID      string
}

type benchmarkRoutedActiveAdmitter struct{}

func (benchmarkRoutedActiveAdmitter) AdmitRoutedActiveBatches(_ context.Context, groups []ConversationActiveTargetBatch) error {
	benchmarkRoutedActiveGroupsSink = groups
	return nil
}

func BenchmarkRecipientAuthoritySnapshotReuse512(b *testing.B) {
	input := newBenchmarkRecipientAuthorityInput("message-512", 512)
	benchmarkRecipientAuthoritySnapshotReuse(b, []benchmarkRecipientAuthorityInput{input}, 1, 512)
}

func BenchmarkRecipientAuthoritySnapshotReuseCloudMediumWeighted(b *testing.B) {
	const cloudMediumRecipientPlanSize = 512
	inputs := make([]benchmarkRecipientAuthorityInput, 0, 255)
	messageIndex := 0
	appendMessages := func(messages, recipients int) {
		for range messages {
			messageID := "message-" + strconv.Itoa(messageIndex)
			messageIndex++
			if recipients <= cloudMediumRecipientPlanSize {
				inputs = append(inputs, newBenchmarkRecipientAuthorityInput(messageID, recipients))
				continue
			}
			for offset := 0; offset < recipients; offset += cloudMediumRecipientPlanSize {
				count := recipients - offset
				if count > cloudMediumRecipientPlanSize {
					count = cloudMediumRecipientPlanSize
				}
				inputs = append(inputs, newBenchmarkRecipientAuthorityInput(messageID+"-page-"+strconv.Itoa(offset/cloudMediumRecipientPlanSize), count))
			}
		}
	}

	// One twentieth of a Cloud Medium second: 250 messages, split into 255
	// bounded recipient plans, with 19,650 total recipient rows.
	appendMessages(100, 2)
	appendMessages(25, 2)
	appendMessages(60, 20)
	appendMessages(42, 100)
	appendMessages(18, 500)
	appendMessages(5, 1000)
	benchmarkRecipientAuthoritySnapshotReuse(b, inputs, 250, 19_650)
}

func benchmarkRecipientAuthoritySnapshotReuse(b *testing.B, inputs []benchmarkRecipientAuthorityInput, messages, recipients int) {
	b.Helper()
	if got := benchmarkRecipientInputRows(inputs); got != recipients {
		b.Fatalf("recipient input rows = %d, want %d", got, recipients)
	}

	b.Run("legacy_two_snapshots_uid_map", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(messages), "messages/op")
		b.ReportMetric(float64(len(inputs)), "plans/op")
		b.ReportMetric(float64(recipients), "recipients/op")
		for index := 0; index < b.N; index++ {
			for inputIndex := range inputs {
				benchmarkLegacyRecipientAuthoritySink = benchmarkLegacyRecipientAuthorityRoutePlan(inputs[inputIndex])
			}
		}
	})

	b.Run("aligned_one_snapshot_shared_groups", func(b *testing.B) {
		admitter := benchmarkRoutedActiveAdmitter{}
		b.ReportAllocs()
		b.ReportMetric(float64(messages), "messages/op")
		b.ReportMetric(float64(len(inputs)), "plans/op")
		b.ReportMetric(float64(recipients), "recipients/op")
		for index := 0; index < b.N; index++ {
			for inputIndex := range inputs {
				input := inputs[inputIndex]
				set := normalizeRecipientsForAuthorityResolution(input.senderUID, input.recipients, true)
				results := make([]RecipientAuthorityResult, len(set.authorityUIDs))
				for authorityIndex, uid := range set.authorityUIDs {
					results[authorityIndex].Target = input.targets[uid]
				}
				grouping, err := groupRecipientAuthorities(set, results, input.senderUID)
				if err != nil {
					b.Fatalf("groupRecipientAuthorities() error = %v", err)
				}
				if err := admitRoutedConversationActiveBatches(context.Background(), CommittedEnvelope{
					FromUID: input.senderUID, ChannelID: "benchmark", ChannelType: 2,
				}, set, grouping, admitter); err != nil {
					b.Fatalf("admitRoutedConversationActiveBatches() error = %v", err)
				}
				benchmarkAlignedRecipientGroupingSink = grouping
			}
		}
	})
}

func newBenchmarkRecipientAuthorityInput(prefix string, recipientCount int) benchmarkRecipientAuthorityInput {
	input := benchmarkRecipientAuthorityInput{
		senderUID:  prefix + "-sender",
		recipients: make([]Recipient, recipientCount),
		targets:    make(map[string]RecipientAuthorityTarget, recipientCount+1),
	}
	input.targets[input.senderUID] = benchmarkRecipientAuthorityTarget(recipientCount)
	for index := 0; index < recipientCount; index++ {
		uid := prefix + "-recipient-" + strconv.Itoa(index)
		input.recipients[index] = Recipient{UID: uid}
		input.targets[uid] = benchmarkRecipientAuthorityTarget(index)
	}
	return input
}

func benchmarkRecipientAuthorityTarget(index int) RecipientAuthorityTarget {
	hashSlot := uint16(index % 256)
	logicalSlot := uint32(hashSlot%10 + 1)
	return RecipientAuthorityTarget{
		HashSlot:       hashSlot,
		SlotID:         logicalSlot,
		LeaderNodeID:   uint64(logicalSlot%3 + 1),
		LeaderTerm:     7,
		ConfigEpoch:    9,
		RouteRevision:  11,
		AuthorityEpoch: 13,
	}
}

func benchmarkRecipientInputRows(inputs []benchmarkRecipientAuthorityInput) int {
	total := 0
	for index := range inputs {
		total += len(inputs[index].recipients)
	}
	return total
}

func benchmarkLegacyRecipientAuthorityRoutePlan(input benchmarkRecipientAuthorityInput) benchmarkLegacyRecipientAuthorityPlan {
	normalized := make([]Recipient, 0, len(input.recipients))
	uids := make([]string, 0, len(input.recipients))
	seen := make(map[string]struct{}, len(input.recipients))
	for _, recipient := range input.recipients {
		if recipient.UID == "" {
			continue
		}
		normalized = append(normalized, recipient)
		if _, ok := seen[recipient.UID]; ok {
			continue
		}
		seen[recipient.UID] = struct{}{}
		uids = append(uids, recipient.UID)
	}

	resolved := make(map[string]RecipientAuthorityTarget, len(uids))
	for _, uid := range uids {
		resolved[uid] = input.targets[uid]
	}
	plan := benchmarkLegacyRecipientAuthorityPlan{
		deliveryGroups: make(map[RecipientAuthorityTarget][]Recipient, min(len(uids), 256)),
	}
	for _, recipient := range normalized {
		target := resolved[recipient.UID]
		if _, ok := plan.deliveryGroups[target]; !ok {
			plan.deliveryOrder = append(plan.deliveryOrder, target)
		}
		plan.deliveryGroups[target] = append(plan.deliveryGroups[target], recipient)
	}
	plan.activeGroups = benchmarkLegacyConversationActiveGroups(input, normalized)
	return plan
}

func benchmarkLegacyConversationActiveGroups(input benchmarkRecipientAuthorityInput, recipients []Recipient) []ConversationActiveTargetBatch {
	items := make([]benchmarkLegacyActiveItem, 0, len(recipients)+1)
	itemIndex := make(map[string]int, len(recipients)+1)
	addItem := func(uid string, sender bool, recipient *Recipient) {
		index, ok := itemIndex[uid]
		if !ok {
			index = len(items)
			itemIndex[uid] = index
			items = append(items, benchmarkLegacyActiveItem{uid: uid})
		}
		items[index].isSender = items[index].isSender || sender
		if recipient != nil && !items[index].hasRecipient {
			items[index].recipient = conversationactive.ActiveEntry{UID: recipient.UID}
			items[index].hasRecipient = true
		}
	}
	addItem(input.senderUID, true, nil)
	for index := range recipients {
		addItem(recipients[index].UID, false, &recipients[index])
	}

	// The old conversation path independently materialized another aligned
	// route result set even though delivery had just resolved the same UIDs.
	results := make([]RecipientAuthorityResult, len(items))
	for index := range items {
		results[index].Target = input.targets[items[index].uid]
	}
	targetIndex := make(map[RecipientAuthorityTarget]int, min(len(items), 256))
	targets := make([]benchmarkLegacyActiveTargetCount, 0, min(len(items), 256))
	totalRecipients := 0
	for index, item := range items {
		target := results[index].Target
		groupIndex, ok := targetIndex[target]
		if !ok {
			groupIndex = len(targets)
			targetIndex[target] = groupIndex
			targets = append(targets, benchmarkLegacyActiveTargetCount{target: target})
		}
		if item.isSender {
			targets[groupIndex].senderUID = item.uid
		}
		if item.hasRecipient {
			targets[groupIndex].recipientCount++
			totalRecipients++
		}
	}

	groups := make([]ConversationActiveTargetBatch, len(targets))
	storage := make([]conversationactive.ActiveEntry, totalRecipients)
	offset := 0
	for index, target := range targets {
		end := offset + target.recipientCount
		groups[index] = ConversationActiveTargetBatch{
			Target: target.target,
			Batch: conversationactive.ActiveBatch{
				SenderUID: target.senderUID,
				ChannelID: "benchmark", ChannelType: 2,
				Recipients: storage[offset:offset:end],
			},
		}
		offset = end
	}
	for _, item := range items {
		if !item.hasRecipient {
			continue
		}
		groupIndex := targetIndex[input.targets[item.uid]]
		groups[groupIndex].Batch.Recipients = append(groups[groupIndex].Batch.Recipients, item.recipient)
	}
	return groups
}
