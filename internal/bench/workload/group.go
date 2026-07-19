package workload

import (
	"context"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultGroupClientPrefix     = "bench-msg"
	groupPreparedBarrierName     = "channel_prepared"
	verifyRecvModeSampled        = "sampled"
	groupSenderPickFirstOnline   = "first_online"
	groupSenderPickRoundRobin    = "round_robin"
	groupSenderPickWeighted8020  = "weighted_80_20"
	maxGroupChannelBatchSize     = 1000
	maxGroupSubscriberBatchSize  = 1000
	maxGroupSubscriberBatchBytes = 64 * 1024
)

// GroupPrepareTarget is the bench/v1 subset required to prepare group channels.
type GroupPrepareTarget interface {
	// UpsertChannels creates or updates benchmark group channels.
	UpsertChannels(ctx context.Context, req model.BatchChannelsRequest) error
	// AddSubscribers appends deterministic subscriber batches to group channels.
	AddSubscribers(ctx context.Context, req model.BatchSubscribersRequest) error
}

// GroupPrepareBarrier is a coordinator hook used to order channel creation before subscription.
type GroupPrepareBarrier interface {
	// Wait pauses until the named preparation barrier is satisfied.
	Wait(ctx context.Context, runID, name string) error
}

// NoopGroupPrepareBarrier is used until the coordinator exposes real preparation barriers.
type NoopGroupPrepareBarrier struct{}

// Wait implements GroupPrepareBarrier without blocking.
func (NoopGroupPrepareBarrier) Wait(ctx context.Context, runID, name string) error { return nil }

// GroupPrepareConfig controls deterministic group channel and subscriber preparation.
type GroupPrepareConfig struct {
	// RunID identifies the benchmark run used in generated batch IDs.
	RunID string
	// WorkerID identifies the worker executing this prepare shard.
	WorkerID string
	// ProfileName identifies the group channel profile.
	ProfileName string
	// ShardMode is model.ShardModeSplitMembersAndTraffic for huge split groups.
	ShardMode string
	// HashSlotSpread places each generated channel in its channel-index physical hash slot.
	HashSlotSpread bool
	// HashSlotCount is the physical hash-slot count used by HashSlotSpread.
	HashSlotCount uint16
	// OwnsChannel marks the split huge-group worker that should create the channel.
	OwnsChannel bool
	// ChannelRange is the half-open group channel range assigned to this worker.
	ChannelRange model.Range
	// MemberRange is the half-open member range assigned for split huge groups.
	MemberRange model.Range
	// MemberBase is the first generated user index for non-split group subscribers.
	MemberBase int
	// MemberReusePolicy is "allowed" when non-split group channels share MemberRange.
	MemberReusePolicy string
	// MembersPerChannel is the deterministic member count per non-split group channel.
	MembersPerChannel int
	// TotalUserCount is the full generated identity pool size, including offline users.
	TotalUserCount int
	// OnlineUserCount is the connected prefix of the generated identity pool.
	OnlineUserCount int
	// OnlineMemberRatio is the target connected-member ratio for each group channel.
	OnlineMemberRatio float64
	// SubscribersBatchSize is the maximum subscribers sent in one bench API request.
	SubscribersBatchSize int
	// UIDPrefix is prepended to generated subscriber user IDs.
	UIDPrefix string
}

// GroupChannel identifies one deterministic group channel and its assigned online members.
type GroupChannel struct {
	// ChannelIndex is the stable logical group channel index.
	ChannelIndex int
	// ChannelID is the target group channel ID.
	ChannelID string
	// OnlineMembers are connected members assigned to this worker for verification and sending.
	OnlineMembers []string
	// TrafficIndexes are deterministic message indexes to emit for this channel.
	TrafficIndexes []int
}

// GroupConfig controls deterministic group channel traffic.
type GroupConfig struct {
	// RunID identifies the benchmark run used in generated payloads.
	RunID string
	// ProfileName identifies the channel profile used in generated payloads.
	ProfileName string
	// TrafficName identifies the traffic stream used in generated payloads.
	TrafficName string
	// ClientMsgPrefix is prepended to generated client message numbers.
	ClientMsgPrefix string
	// PayloadSizeBytes controls the generated payload size. Zero keeps the base payload as-is.
	PayloadSizeBytes int
	// Phase identifies the traffic phase used in generated message identities.
	Phase string
	// RunDuration is the measured duration used during Run.
	RunDuration time.Duration
	// WarmupDuration is the duration used during Warmup.
	WarmupDuration time.Duration
	// CooldownDuration is the drain wait used during Cooldown.
	CooldownDuration time.Duration
	// AckTimeout bounds the wait for a sendack after one send request.
	AckTimeout time.Duration
	// RecvTimeout bounds the wait for a delivered recv frame when verification is enabled.
	RecvTimeout time.Duration
	// VerifyRecvMode enables receive verification when set to "full" or "sampled".
	VerifyRecvMode string
	// RecvSampleSize is the maximum deterministic member sample size for sampled verification.
	RecvSampleSize int
	// RecvAck controls whether verified recipients send receive acknowledgments.
	RecvAck bool
	// SenderPick selects the online member used as sender for each message.
	SenderPick string
	// GlobalRate is the configured per-channel rate before split partitioning.
	GlobalRate model.Rate
	// LocalRate is the worker-local effective rate.
	LocalRate model.Rate
	// MaxConcurrency bounds concurrent send+sendack operations. Zero preserves sequential sends.
	MaxConcurrency int
	// TrafficPartitionCount is the global split traffic partition count.
	TrafficPartitionCount int
	// OwnedTrafficPartitions are the split traffic partitions assigned to this worker.
	OwnedTrafficPartitions []int
	// Channels are group channels assigned to this worker.
	Channels []GroupChannel
	// Metrics stores counters, latencies, and error samples for this workload.
	Metrics *metrics.Registry

	sleep func(context.Context, time.Duration) error
}

// GroupRunConfig controls one timed group workload execution.
type GroupRunConfig struct {
	// Phase identifies the traffic phase used in generated message identities.
	Phase string
	// Duration is the traffic window length for this workload execution.
	Duration time.Duration
	// Rate is the worker-local send rate for this workload execution.
	Rate model.Rate
}

// GroupWorkload executes deterministic group-channel benchmark traffic.
type GroupWorkload struct {
	cfg      GroupConfig
	metrics  *metrics.Registry
	channels []GroupChannel
	clients  map[string]PersonClient
	// warmupOperationDeadline caps the final in-flight warmup operation tail.
	warmupOperationDeadline time.Time
}

// PrepareGroup creates owned group channels, waits for the channel barrier, and appends subscribers.
func PrepareGroup(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget, barrier GroupPrepareBarrier) error {
	if target == nil {
		return fmt.Errorf("group prepare: target is required")
	}
	cfg = normalizeGroupPrepareConfig(cfg)
	if cfg.RunID == "" {
		return fmt.Errorf("group prepare: run id is required")
	}
	if cfg.ProfileName == "" {
		return fmt.Errorf("group prepare: profile name is required")
	}
	if cfg.WorkerID == "" {
		return fmt.Errorf("group prepare: worker id is required")
	}
	if barrier == nil {
		barrier = NoopGroupPrepareBarrier{}
	}

	if cfg.ShardMode == model.ShardModeSplitMembersAndTraffic {
		if cfg.OwnsChannel {
			if err := upsertGroupChannels(ctx, cfg, target, true); err != nil {
				return err
			}
		}
		if err := barrier.Wait(ctx, cfg.RunID, groupPreparedBarrierName); err != nil {
			return err
		}
		return addHugeGroupSubscribers(ctx, cfg, target)
	}

	if err := upsertGroupChannels(ctx, cfg, target, false); err != nil {
		return err
	}
	return addSmallGroupSubscribers(ctx, cfg, target)
}

// PrepareGroupChannels creates only owned group channels without appending subscribers.
func PrepareGroupChannels(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget) error {
	if target == nil {
		return fmt.Errorf("group prepare: target is required")
	}
	cfg = normalizeGroupPrepareConfig(cfg)
	if cfg.RunID == "" {
		return fmt.Errorf("group prepare: run id is required")
	}
	if cfg.ProfileName == "" {
		return fmt.Errorf("group prepare: profile name is required")
	}
	if cfg.WorkerID == "" {
		return fmt.Errorf("group prepare: worker id is required")
	}
	if cfg.ShardMode == model.ShardModeSplitMembersAndTraffic && !cfg.OwnsChannel {
		return nil
	}
	return upsertGroupChannels(ctx, cfg, target, cfg.ShardMode == model.ShardModeSplitMembersAndTraffic)
}

// GroupChannelID returns the stable generated channel ID for a group channel.
func GroupChannelID(runID, profileName string, channelIndex int) string {
	return fmt.Sprintf("%s-%s-%d", strings.TrimSpace(runID), strings.TrimSpace(profileName), channelIndex)
}

// GroupChannelIDForHashSlot returns a stable channel ID that hashes to the requested physical hash slot.
func GroupChannelIDForHashSlot(runID, profileName string, channelIndex int, hashSlotCount uint16) string {
	if hashSlotCount == 0 {
		return GroupChannelID(runID, profileName, channelIndex)
	}
	desired := uint16(channelIndex % int(hashSlotCount))
	base := fmt.Sprintf("%s-%s-hs-%d", strings.TrimSpace(runID), strings.TrimSpace(profileName), desired)
	for nonce := 0; ; nonce++ {
		candidate := base
		if nonce > 0 {
			candidate = fmt.Sprintf("%s-%d", base, nonce)
		}
		if physicalHashSlotForKey(candidate, hashSlotCount) == desired {
			return candidate
		}
	}
}

// physicalHashSlotForKey mirrors pkg/hashslot.HashSlotForKey without importing
// the server-side Slot Raft dependency graph into the black-box benchmark.
func physicalHashSlotForKey(key string, hashSlotCount uint16) uint16 {
	if hashSlotCount == 0 {
		return 0
	}
	return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}

// GroupLocalRate returns the worker share of a split group rate without multiplying by channel count.
func GroupLocalRate(global model.Rate, partitionCount int, ownedPartitions []int) model.Rate {
	if partitionCount <= 0 || len(ownedPartitions) == 0 {
		return global
	}
	return model.Rate{PerSecond: global.PerSecond * float64(len(ownedPartitions)) / float64(partitionCount)}
}

// NewGroupWorkload validates the config, normalizes channels, and binds group clients.
func NewGroupWorkload(cfg GroupConfig, clients map[string]PersonClient) (*GroupWorkload, error) {
	cfg.RunID = strings.TrimSpace(cfg.RunID)
	cfg.ProfileName = strings.TrimSpace(cfg.ProfileName)
	cfg.TrafficName = strings.TrimSpace(cfg.TrafficName)
	cfg.ClientMsgPrefix = strings.TrimSpace(cfg.ClientMsgPrefix)
	cfg.VerifyRecvMode = strings.TrimSpace(cfg.VerifyRecvMode)
	cfg.SenderPick = strings.TrimSpace(cfg.SenderPick)
	if cfg.ClientMsgPrefix == "" {
		cfg.ClientMsgPrefix = defaultGroupClientPrefix
	}
	if cfg.SenderPick == "" {
		cfg.SenderPick = groupSenderPickFirstOnline
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NewRegistry()
	}
	if cfg.sleep == nil {
		cfg.sleep = sleepContext
	}
	channels, err := normalizeGroupChannels(cfg)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("group workload: at least one channel is required")
	}
	boundClients := make(map[string]PersonClient, len(clients))
	for uid, client := range clients {
		uid = strings.TrimSpace(uid)
		if uid == "" || client == nil {
			continue
		}
		boundClients[uid] = client
	}
	for _, ch := range channels {
		for _, uid := range ch.OnlineMembers {
			if _, ok := boundClients[uid]; !ok {
				return nil, fmt.Errorf("group workload: missing client for member %q", uid)
			}
		}
	}
	if cfg.LocalRate.PerSecond == 0 {
		cfg.LocalRate = GroupLocalRate(cfg.GlobalRate, cfg.TrafficPartitionCount, cfg.OwnedTrafficPartitions)
	}
	return &GroupWorkload{cfg: cfg, metrics: cfg.Metrics, channels: channels, clients: boundClients}, nil
}

// Metrics returns the workload metrics registry.
func (w *GroupWorkload) Metrics() *metrics.Registry {
	if w == nil {
		return nil
	}
	return w.metrics
}

// Warmup runs low-rate traffic for the configured warmup duration.
func (w *GroupWorkload) Warmup(ctx context.Context) error {
	if w.cfg.WarmupDuration <= 0 {
		return nil
	}
	restore := w.useWarmupTimeouts()
	defer restore()
	return w.RunWindow(ctx, GroupRunConfig{Phase: "warmup", Duration: w.cfg.WarmupDuration, Rate: warmupRateForDuration(w.cfg.LocalRate, w.cfg.WarmupDuration)})
}

// Run sends rate-limited group traffic for the configured measured duration.
func (w *GroupWorkload) Run(ctx context.Context) error {
	if w.cfg.RunDuration > 0 && w.cfg.LocalRate.PerSecond > 0 {
		return w.RunWindow(ctx, GroupRunConfig{Phase: "run", Duration: w.cfg.RunDuration, Rate: w.cfg.LocalRate})
	}
	for _, ch := range w.channels {
		indexes := ch.TrafficIndexes
		if len(indexes) == 0 && len(w.cfg.OwnedTrafficPartitions) > 0 {
			indexes = append([]int(nil), w.cfg.OwnedTrafficPartitions...)
		}
		if len(indexes) == 0 {
			indexes = []int{ch.ChannelIndex}
		}
		for _, messageIndex := range indexes {
			if err := w.sendOneInPhase(ctx, w.cfg.phaseName("run"), ch.ChannelIndex, messageIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

// RunMeasuredWindow sends one uniquely named window at the configured measured rate.
func (w *GroupWorkload) RunMeasuredWindow(ctx context.Context, duration time.Duration, window int) error {
	return w.RunWindow(ctx, GroupRunConfig{Phase: fmt.Sprintf("run-window-%d", window), Duration: duration, Rate: w.cfg.LocalRate})
}

// Cooldown waits for the configured drain period without emitting new sends.
func (w *GroupWorkload) Cooldown(ctx context.Context) error {
	if w.cfg.CooldownDuration <= 0 {
		return nil
	}
	return w.cfg.sleep(ctx, w.cfg.CooldownDuration)
}

// RunWindow emits this workload's traffic within a caller-owned phase window.
func (w *GroupWorkload) RunWindow(ctx context.Context, cfg GroupRunConfig) error {
	return w.runFor(ctx, cfg)
}

func (w *GroupWorkload) runFor(ctx context.Context, cfg GroupRunConfig) error {
	totalMessages := scheduledMessageCount(cfg.Duration, cfg.Rate.PerSecond, len(w.channels))
	if totalMessages <= 0 {
		return nil
	}
	interval := scheduledMessageInterval(cfg.Duration, totalMessages)
	phase := w.cfg.phaseName(cfg.Phase)
	if w.cfg.MaxConcurrency > 1 {
		stats := &scheduledMessageStats{}
		err := runScheduledMessagesByKeyWithStats(ctx, totalMessages, interval, w.cfg.MaxConcurrency, func(localOffset int) string {
			ch := w.channels[localOffset%len(w.channels)]
			messageIndex := w.messageIndexForLocalOffset(ch, localOffset/len(w.channels))
			return w.senderUID(ch, messageIndex)
		}, func(ctx context.Context, localOffset int) error {
			ch := w.channels[localOffset%len(w.channels)]
			messageIndex := w.messageIndexForLocalOffset(ch, localOffset/len(w.channels))
			err := w.sendOneInPhase(ctx, phase, ch.ChannelIndex, messageIndex)
			if shouldContinueTrafficOperationError(ctx, cfg.Phase, err) {
				return nil
			}
			return err
		}, stats)
		recordSchedulerStats(w.metrics, w.sendMetricLabels(phase), stats)
		return err
	}
	for localOffset := 0; localOffset < totalMessages; localOffset++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		ch := w.channels[localOffset%len(w.channels)]
		messageIndex := w.messageIndexForLocalOffset(ch, localOffset/len(w.channels))
		if err := w.sendOneInPhase(ctx, phase, ch.ChannelIndex, messageIndex); err != nil {
			if !shouldContinueTrafficOperationError(ctx, cfg.Phase, err) {
				return err
			}
		}
		if interval > 0 {
			if err := w.cfg.sleep(ctx, interval); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendOne sends one deterministic group-channel message.
func (w *GroupWorkload) SendOne(ctx context.Context, channelIndex, messageIndex int) error {
	return w.sendOneInPhase(ctx, w.cfg.phaseName("run"), channelIndex, messageIndex)
}

func (w *GroupWorkload) sendOneInPhase(ctx context.Context, phase string, channelIndex, messageIndex int) error {
	ch, err := w.channelForIndex(channelIndex)
	if err != nil {
		w.recordError("group_send_error", err)
		return err
	}
	if len(ch.OnlineMembers) == 0 {
		err := fmt.Errorf("group workload: channel %d has no online members", channelIndex)
		w.recordError("group_send_error", err)
		return err
	}
	senderUID := w.senderUID(ch, messageIndex)
	sender := w.clients[senderUID]
	payload := w.buildPayload(phase, channelIndex, messageIndex)
	clientSeq := nextSendClientSeq(sender, uint64(messageIndex))
	clientMsgNo := w.clientMsgNo(phase, channelIndex, messageIndex)
	pkt := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   ch.ChannelID,
		ChannelType: frame.ChannelTypeGroup,
		Payload:     payload,
	}

	sendLabels := w.sendMetricLabels(phase)
	sendStart := time.Now()
	unlockSendack, err := lockSendackOperation(ctx, sender)
	if err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("group_send_error", err)
			w.metrics.IncCounter("group_send_error_total", sendLabels)
		}
		return sessionOperationError(senderUID, "group sendack lock", err)
	}
	defer unlockSendack()
	if err := sender.Send(ctx, pkt); err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("group_send_error", err)
			w.metrics.IncCounter("group_send_error_total", sendLabels)
		}
		return sessionOperationError(senderUID, "group send", err)
	}
	ack, err := w.waitForSendack(ctx, sender, clientSeq, clientMsgNo)
	if err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("group_send_error", err)
			w.metrics.IncCounter("group_send_error_total", sendLabels)
		}
		return sessionOperationError(senderUID, "group sendack", err)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		err := fmt.Errorf("group workload: sendack rejected message %q with reason %s", clientMsgNo, ack.ReasonCode)
		w.recordError("group_send_error", err)
		w.metrics.IncCounter("group_send_error_total", sendLabels)
		return sessionOperationError(senderUID, "group sendack", err)
	}
	w.metrics.IncCounter("group_send_success_total", sendLabels)
	w.metrics.ObserveLatency("group_send_latency_seconds", sendLabels, time.Since(sendStart))

	recipients := w.verificationMembers(ch, senderUID)
	if len(recipients) == 0 {
		return nil
	}
	expectedMessageID := uint64(0)
	if ack.MessageID > 0 {
		expectedMessageID = uint64(ack.MessageID)
	}
	for _, uid := range recipients {
		recvStart := time.Now()
		recv, err := w.waitForRecv(ctx, w.clients[uid], ch.ChannelID, clientMsgNo, senderUID, expectedMessageID, ack.MessageSeq)
		if err != nil {
			if shouldRecordPhaseOperationError(ctx, err) {
				w.recordError("group_recv_error", err)
				w.metrics.IncCounter("group_recv_error_total", sendLabels)
			}
			return sessionOperationError(uid, "group recv", err)
		}
		if string(recv.Payload) != string(payload) {
			err := fmt.Errorf("group workload: recv payload mismatch for %q", clientMsgNo)
			w.recordError("group_recv_error", err)
			w.metrics.IncCounter("group_recv_error_total", sendLabels)
			return sessionOperationError(uid, "group recv", err)
		}
		if w.cfg.RecvAck {
			if err := w.clients[uid].RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
				if shouldRecordPhaseOperationError(ctx, err) {
					w.recordError("group_recv_error", err)
					w.metrics.IncCounter("group_recv_error_total", sendLabels)
				}
				return sessionOperationError(uid, "group recvack", err)
			}
		}
		w.metrics.IncCounter("group_recv_success_total", sendLabels)
		w.metrics.ObserveLatency("group_recv_latency_seconds", sendLabels, time.Since(recvStart))
	}
	return nil
}

func (w *GroupWorkload) senderUID(ch GroupChannel, messageIndex int) string {
	if len(ch.OnlineMembers) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(w.cfg.SenderPick)) {
	case groupSenderPickRoundRobin:
		idx := messageIndex % len(ch.OnlineMembers)
		if idx < 0 {
			idx += len(ch.OnlineMembers)
		}
		return ch.OnlineMembers[idx]
	case groupSenderPickWeighted8020:
		if len(ch.OnlineMembers) == 1 {
			return ch.OnlineMembers[0]
		}
		hotCount := int(math.Ceil(float64(len(ch.OnlineMembers)) * 0.2))
		if hotCount >= len(ch.OnlineMembers) {
			hotCount = len(ch.OnlineMembers) - 1
		}
		index := messageIndex
		if index < 0 {
			index = -index
		}
		cycle := index % 5
		if cycle < 4 {
			hotIndex := ((index / 5) * 4) + cycle
			return ch.OnlineMembers[hotIndex%hotCount]
		}
		coldCount := len(ch.OnlineMembers) - hotCount
		return ch.OnlineMembers[hotCount+(index/5)%coldCount]
	default:
		return ch.OnlineMembers[0]
	}
}

func (w *GroupWorkload) sendMetricLabels(phase string) metrics.Labels {
	return metrics.Labels{
		"phase":        stableMetricPhase(phase),
		"channel_type": model.ChannelTypeGroup,
		"profile":      w.cfg.ProfileName,
		"traffic":      w.cfg.TrafficName,
	}
}

func (w *GroupWorkload) messageIndexForLocalOffset(ch GroupChannel, localOffset int) int {
	if w.cfg.TrafficPartitionCount <= 0 || len(w.cfg.OwnedTrafficPartitions) == 0 {
		return localOffset
	}
	partitions := ch.TrafficIndexes
	if len(partitions) == 0 {
		partitions = w.cfg.OwnedTrafficPartitions
	}
	partition := partitions[localOffset%len(partitions)]
	cycle := localOffset / len(partitions)
	return cycle*w.cfg.TrafficPartitionCount + partition
}

func normalizeGroupPrepareConfig(cfg GroupPrepareConfig) GroupPrepareConfig {
	cfg.RunID = strings.TrimSpace(cfg.RunID)
	cfg.WorkerID = strings.TrimSpace(cfg.WorkerID)
	cfg.ProfileName = strings.TrimSpace(cfg.ProfileName)
	cfg.ShardMode = strings.TrimSpace(cfg.ShardMode)
	cfg.UIDPrefix = strings.TrimSpace(cfg.UIDPrefix)
	if cfg.UIDPrefix == "" {
		cfg.UIDPrefix = "bench-u"
	}
	if cfg.SubscribersBatchSize <= 0 {
		cfg.SubscribersBatchSize = 1000
	}
	return cfg
}

func upsertGroupChannels(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget, large bool) error {
	channels := make([]model.ChannelItem, 0, cfg.ChannelRange.Len())
	for channelIndex := cfg.ChannelRange.Start; channelIndex < cfg.ChannelRange.End; channelIndex++ {
		channels = append(channels, model.ChannelItem{ChannelID: groupPreparedChannelID(cfg, channelIndex), ChannelType: frame.ChannelTypeGroup, Large: large})
	}
	if len(channels) == 0 {
		return nil
	}
	for start := 0; start < len(channels); start += maxGroupChannelBatchSize {
		end := start + maxGroupChannelBatchSize
		if end > len(channels) {
			end = len(channels)
		}
		channelStart := cfg.ChannelRange.Start + start
		channelEnd := cfg.ChannelRange.Start + end
		if err := target.UpsertChannels(ctx, model.BatchChannelsRequest{
			RunID:    cfg.RunID,
			BatchID:  groupChannelsBatchID(cfg, channelStart, channelEnd),
			Upsert:   true,
			Channels: channels[start:end],
		}); err != nil {
			return err
		}
	}
	return nil
}

func addHugeGroupSubscribers(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget) error {
	if cfg.MemberRange.Len() <= 0 || cfg.ChannelRange.Len() <= 0 {
		return nil
	}
	channelID := groupPreparedChannelID(cfg, cfg.ChannelRange.Start)
	for start := cfg.MemberRange.Start; start < cfg.MemberRange.End; {
		end := groupSubscriberBatchEnd(cfg, start, cfg.MemberRange.End)
		if err := addSubscriberBatch(ctx, target, cfg, channelID, start, end); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func addSmallGroupSubscribers(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget) error {
	if cfg.MembersPerChannel <= 0 {
		return nil
	}
	items := make([]model.SubscriberItem, 0, minInt(cfg.ChannelRange.Len(), maxGroupSubscriberBatchSize))
	batchStart := -1
	batchEnd := -1
	flush := func() error {
		if len(items) == 0 {
			return nil
		}
		reqItems := append([]model.SubscriberItem(nil), items...)
		if err := target.AddSubscribers(ctx, model.BatchSubscribersRequest{
			RunID:   cfg.RunID,
			BatchID: groupSubscribersBatchID(cfg, batchStart, batchEnd),
			Items:   reqItems,
		}); err != nil {
			return err
		}
		items = items[:0]
		batchStart = -1
		batchEnd = -1
		return nil
	}
	for channelIndex := cfg.ChannelRange.Start; channelIndex < cfg.ChannelRange.End; channelIndex++ {
		memberIndexes := smallGroupMemberIndexes(cfg, channelIndex)
		if len(memberIndexes) == 0 {
			continue
		}
		channelID := groupPreparedChannelID(cfg, channelIndex)
		for start := 0; start < len(memberIndexes); {
			end := groupSubscriberIndexBatchEnd(cfg, start, len(memberIndexes))
			if batchStart < 0 {
				batchStart = channelIndex
			}
			batchEnd = channelIndex + 1
			items = append(items, subscriberItemForIndexes(cfg, channelID, memberIndexes[start:end]))
			if len(items) >= maxGroupSubscriberBatchSize {
				if err := flush(); err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			start = end
		}
	}
	return flush()
}

func groupPreparedChannelID(cfg GroupPrepareConfig, channelIndex int) string {
	if cfg.HashSlotSpread {
		return GroupChannelIDForHashSlot(cfg.RunID, cfg.ProfileName, channelIndex, cfg.HashSlotCount)
	}
	return GroupChannelID(cfg.RunID, cfg.ProfileName, channelIndex)
}

func smallGroupMemberIndexes(cfg GroupPrepareConfig, channelIndex int) []int {
	if strings.TrimSpace(cfg.MemberReusePolicy) == "allowed" && cfg.MemberRange.Len() > 0 {
		if cfg.TotalUserCount > cfg.OnlineUserCount && cfg.OnlineUserCount > 0 {
			return deterministicGroupMembersByAvailability(
				cfg.RunID,
				cfg.ProfileName,
				channelIndex,
				model.Range{Start: 0, End: cfg.OnlineUserCount},
				model.Range{Start: cfg.OnlineUserCount, End: cfg.TotalUserCount},
				cfg.MembersPerChannel,
				groupOnlineMemberCount(cfg.MembersPerChannel, cfg.OnlineMemberRatio),
			)
		}
		return DeterministicGroupMemberIndexes(cfg.RunID, cfg.ProfileName, channelIndex, cfg.MemberRange, cfg.MembersPerChannel)
	}
	memberStart := cfg.MemberBase + (channelIndex-cfg.ChannelRange.Start)*cfg.MembersPerChannel
	if cfg.MemberBase == 0 {
		memberStart = channelIndex * cfg.MembersPerChannel
	}
	indexes := make([]int, 0, cfg.MembersPerChannel)
	for idx := memberStart; idx < memberStart+cfg.MembersPerChannel; idx++ {
		indexes = append(indexes, idx)
	}
	return indexes
}

func deterministicGroupMembersByAvailability(runID, profileName string, channelIndex int, onlinePool, offlinePool model.Range, count, onlineCount int) []int {
	if count <= 0 {
		return nil
	}
	if onlineCount < 0 {
		onlineCount = 0
	}
	if onlineCount > count {
		onlineCount = count
	}
	online := DeterministicGroupMemberIndexes(runID, profileName+"/online", channelIndex, onlinePool, onlineCount)
	offline := DeterministicGroupMemberIndexes(runID, profileName+"/offline", channelIndex, offlinePool, count-len(online))
	return append(online, offline...)
}

// DeterministicGroupMemberIndexesByAvailability selects the configured online
// prefix first and fills the remaining membership from the offline identity pool.
func DeterministicGroupMemberIndexesByAvailability(runID, profileName string, channelIndex int, onlinePool, offlinePool model.Range, count int, onlineRatio float64) []int {
	return deterministicGroupMembersByAvailability(runID, profileName, channelIndex, onlinePool, offlinePool, count, groupOnlineMemberCount(count, onlineRatio))
}

func groupOnlineMemberCount(memberCount int, ratio float64) int {
	if memberCount <= 0 {
		return 0
	}
	if ratio <= 0 || ratio >= 1 {
		return memberCount
	}
	count := int(math.Round(float64(memberCount) * ratio))
	if count < 1 {
		return 1
	}
	if count > memberCount {
		return memberCount
	}
	return count
}

// DeterministicGroupMemberIndexes selects stable members from a shared group member pool.
func DeterministicGroupMemberIndexes(runID, profileName string, channelIndex int, pool model.Range, count int) []int {
	if count <= 0 || pool.Len() <= 0 {
		return nil
	}
	if count > pool.Len() {
		count = pool.Len()
	}
	indexes := make([]int, 0, count)
	seen := make(map[int]struct{}, count)
	for salt := 0; len(indexes) < count; salt++ {
		idx := pool.Start + stableGroupMemberHash(runID, profileName, channelIndex, salt)%pool.Len()
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	return indexes
}

func addSubscriberBatch(ctx context.Context, target GroupPrepareTarget, cfg GroupPrepareConfig, channelID string, start, end int) error {
	indexes := make([]int, 0, end-start)
	for idx := start; idx < end; idx++ {
		indexes = append(indexes, idx)
	}
	return addSubscriberIndexBatch(ctx, target, cfg, channelID, indexes)
}

func addSubscriberIndexBatch(ctx context.Context, target GroupPrepareTarget, cfg GroupPrepareConfig, channelID string, indexes []int) error {
	if len(indexes) == 0 {
		return nil
	}
	item := subscriberItemForIndexes(cfg, channelID, indexes)
	return target.AddSubscribers(ctx, model.BatchSubscribersRequest{
		RunID:   cfg.RunID,
		BatchID: groupSubscribersIndexBatchID(cfg, indexes),
		Items:   []model.SubscriberItem{item},
	})
}

func subscriberItemForIndexes(cfg GroupPrepareConfig, channelID string, indexes []int) model.SubscriberItem {
	subscribers := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		subscribers = append(subscribers, indexedBenchID(cfg.UIDPrefix, idx))
	}
	return model.SubscriberItem{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypeGroup,
		Reset:       false,
		Subscribers: subscribers,
	}
}

func groupSubscriberIndexBatchEnd(cfg GroupPrepareConfig, start, max int) int {
	end := start + cfg.SubscribersBatchSize
	if end > max {
		end = max
	}
	return end
}

func groupChannelsBatchID(cfg GroupPrepareConfig, start, end int) string {
	return fmt.Sprintf("%s-channels-%s-%s-%d-%d", cfg.RunID, cfg.ProfileName, cfg.WorkerID, start, end)
}

func groupSubscribersBatchID(cfg GroupPrepareConfig, start, end int) string {
	return fmt.Sprintf("%s-subs-%s-%s-%d-%d", cfg.RunID, cfg.ProfileName, cfg.WorkerID, start, end)
}

func groupSubscribersIndexBatchID(cfg GroupPrepareConfig, indexes []int) string {
	if len(indexes) == 0 {
		return groupSubscribersBatchID(cfg, 0, 0)
	}
	return groupSubscribersBatchID(cfg, indexes[0], indexes[len(indexes)-1]+1)
}

func stableGroupMemberHash(runID, profileName string, channelIndex, salt int) int {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%s/%d/%d", runID, profileName, channelIndex, salt)
	return int(h.Sum32())
}

func normalizeGroupChannels(cfg GroupConfig) ([]GroupChannel, error) {
	channels := make([]GroupChannel, 0, len(cfg.Channels))
	for idx, ch := range cfg.Channels {
		ch.ChannelID = strings.TrimSpace(ch.ChannelID)
		if ch.ChannelIndex < 0 {
			return nil, fmt.Errorf("group workload: channel %d index must not be negative", idx)
		}
		if ch.ChannelID == "" {
			ch.ChannelID = GroupChannelID(cfg.RunID, cfg.ProfileName, ch.ChannelIndex)
		}
		members := make([]string, 0, len(ch.OnlineMembers))
		seen := make(map[string]struct{}, len(ch.OnlineMembers))
		for _, uid := range ch.OnlineMembers {
			uid = strings.TrimSpace(uid)
			if uid == "" {
				continue
			}
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			members = append(members, uid)
		}
		if len(members) == 0 {
			return nil, fmt.Errorf("group workload: channel %d requires online members", ch.ChannelIndex)
		}
		sort.Strings(members)
		ch.OnlineMembers = members
		channels = append(channels, ch)
	}
	sort.SliceStable(channels, func(i, j int) bool { return channels[i].ChannelIndex < channels[j].ChannelIndex })
	return channels, nil
}

func (w *GroupWorkload) channelForIndex(channelIndex int) (GroupChannel, error) {
	for _, ch := range w.channels {
		if ch.ChannelIndex == channelIndex {
			return ch, nil
		}
	}
	return GroupChannel{}, fmt.Errorf("group workload: no channel assigned to index %d", channelIndex)
}

func (w *GroupWorkload) verificationMembers(ch GroupChannel, senderUID string) []string {
	mode := strings.ToLower(strings.TrimSpace(w.cfg.VerifyRecvMode))
	members := make([]string, 0, len(ch.OnlineMembers))
	for _, uid := range ch.OnlineMembers {
		if uid == senderUID {
			continue
		}
		members = append(members, uid)
	}
	switch mode {
	case verifyRecvModeFull:
		return members
	case verifyRecvModeSampled:
		if w.cfg.RecvSampleSize <= 0 {
			return nil
		}
		limit := w.cfg.RecvSampleSize
		if limit > len(members) {
			limit = len(members)
		}
		return append([]string(nil), members[:limit]...)
	default:
		return nil
	}
}

func (w *GroupWorkload) waitForSendack(ctx context.Context, client PersonClient, clientSeq uint64, clientMsgNo string) (*frame.SendackPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.AckTimeout)
	defer cancel()
	f, err := readFrameMatching(deadlineCtx, client, func(f frame.Frame) bool {
		ack, ok := f.(*frame.SendackPacket)
		return ok && matchesExpectedSendack(ack, clientSeq, clientMsgNo)
	})
	if err != nil {
		return nil, fmt.Errorf("group workload: sendack not received for %q: %w", clientMsgNo, err)
	}
	return f.(*frame.SendackPacket), nil
}

func (w *GroupWorkload) waitForRecv(ctx context.Context, client PersonClient, channelID, clientMsgNo, senderUID string, expectedMessageID uint64, expectedMessageSeq uint64) (*frame.RecvPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.RecvTimeout)
	defer cancel()
	f, err := readFrameMatching(deadlineCtx, client, func(f frame.Frame) bool {
		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			return false
		}
		if recv.FromUID != senderUID || recv.ChannelID != channelID || recv.ChannelType != frame.ChannelTypeGroup || recv.ClientMsgNo != clientMsgNo {
			return false
		}
		if expectedMessageID > 0 && recv.MessageID != int64(expectedMessageID) {
			return false
		}
		if expectedMessageSeq > 0 && recv.MessageSeq != expectedMessageSeq {
			return false
		}
		return true
	})
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("group workload: recv not received for %q: %w", clientMsgNo, err)
		}
		return nil, err
	}
	return f.(*frame.RecvPacket), nil
}

func (w *GroupWorkload) buildPayload(phase string, channelIndex, messageIndex int) []byte {
	base := w.payloadMarker(phase, channelIndex, messageIndex)
	if w.cfg.PayloadSizeBytes <= 0 {
		return []byte(base)
	}
	if len(base) >= w.cfg.PayloadSizeBytes {
		return []byte(base[:w.cfg.PayloadSizeBytes])
	}
	payload := make([]byte, 0, w.cfg.PayloadSizeBytes)
	payload = append(payload, base...)
	fill := fmt.Sprintf("|%s|%d|", w.cfg.ClientMsgPrefix, messageIndex)
	for len(payload) < w.cfg.PayloadSizeBytes {
		remaining := w.cfg.PayloadSizeBytes - len(payload)
		if remaining >= len(fill) {
			payload = append(payload, fill...)
			continue
		}
		payload = append(payload, fill[:remaining]...)
	}
	return payload
}

func (w *GroupWorkload) payloadMarker(phase string, channelIndex, messageIndex int) string {
	return fmt.Sprintf("run=%s profile=%s traffic=%s phase=%s channel=%d message=%d", w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, phase, channelIndex, messageIndex)
}

func (w *GroupWorkload) clientMsgNo(phase string, channelIndex, messageIndex int) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s-ch%d-msg%d", w.cfg.ClientMsgPrefix, w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, phase, channelIndex, messageIndex)
}

func (cfg GroupConfig) phaseName(phase string) string {
	phase = strings.TrimSpace(phase)
	if phase != "" {
		return phase
	}
	phase = strings.TrimSpace(cfg.Phase)
	if phase != "" {
		return phase
	}
	return "run"
}

func (w *GroupWorkload) recordError(name string, err error) {
	if w == nil || err == nil {
		return
	}
	w.metrics.RecordErrorSample(name, err)
}

func (w *GroupWorkload) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	timeout = boundedWarmupOperationTimeout(timeout, w.warmupOperationDeadline, time.Now())
	return context.WithTimeout(ctx, timeout)
}

func (w *GroupWorkload) useWarmupTimeouts() func() {
	ackTimeout := w.cfg.AckTimeout
	recvTimeout := w.cfg.RecvTimeout
	deadline := w.warmupOperationDeadline
	w.warmupOperationDeadline = time.Now().Add(w.cfg.WarmupDuration + warmupOperationTailTimeout(ackTimeout, recvTimeout))
	w.cfg.AckTimeout = warmupOperationTimeout(w.cfg.AckTimeout, w.cfg.WarmupDuration)
	w.cfg.RecvTimeout = warmupOperationTimeout(w.cfg.RecvTimeout, w.cfg.WarmupDuration)
	return func() {
		w.cfg.AckTimeout = ackTimeout
		w.cfg.RecvTimeout = recvTimeout
		w.warmupOperationDeadline = deadline
	}
}

func indexedBenchID(prefix string, index int) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "bench"
	}
	return fmt.Sprintf("%s-%d", prefix, index)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func groupSubscriberBatchLimit(batchSize int) int {
	if batchSize <= 0 || batchSize > maxGroupSubscriberBatchSize {
		return maxGroupSubscriberBatchSize
	}
	return batchSize
}

func groupSubscriberBatchEnd(cfg GroupPrepareConfig, start, end int) int {
	limit := groupSubscriberBatchLimit(cfg.SubscribersBatchSize)
	batchBytes := 0
	count := 0
	idx := start
	for idx < end {
		uid := indexedBenchID(cfg.UIDPrefix, idx)
		uidBytes := len(uid) + 3
		if count > 0 && (count >= limit || batchBytes+uidBytes > maxGroupSubscriberBatchBytes) {
			break
		}
		batchBytes += uidBytes
		count++
		idx++
		if count >= limit || batchBytes >= maxGroupSubscriberBatchBytes {
			break
		}
	}
	if idx == start {
		return start + 1
	}
	return idx
}
