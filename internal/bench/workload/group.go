package workload

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultGroupClientPrefix = "bench-msg"
	groupPreparedBarrierName = "channel_prepared"
	verifyRecvModeSampled    = "sampled"
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
	// OwnsChannel marks the split huge-group worker that should create the channel.
	OwnsChannel bool
	// ChannelRange is the half-open group channel range assigned to this worker.
	ChannelRange model.Range
	// MemberRange is the half-open member range assigned for split huge groups.
	MemberRange model.Range
	// MembersPerChannel is the deterministic member count per non-split group channel.
	MembersPerChannel int
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
	// GlobalRate is the configured per-channel rate before split partitioning.
	GlobalRate model.Rate
	// LocalRate is the worker-local effective rate.
	LocalRate model.Rate
	// TrafficPartitionCount is the global split traffic partition count.
	TrafficPartitionCount int
	// OwnedTrafficPartitions are the split traffic partitions assigned to this worker.
	OwnedTrafficPartitions []int
	// Channels are group channels assigned to this worker.
	Channels []GroupChannel
	// Metrics stores counters, latencies, and error samples for this workload.
	Metrics *metrics.Registry
}

// GroupWorkload executes deterministic group-channel benchmark traffic.
type GroupWorkload struct {
	cfg      GroupConfig
	metrics  *metrics.Registry
	channels []GroupChannel
	clients  map[string]PersonClient
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

// GroupChannelID returns the stable generated channel ID for a group channel.
func GroupChannelID(runID, profileName string, channelIndex int) string {
	return fmt.Sprintf("%s-%s-%d", strings.TrimSpace(runID), strings.TrimSpace(profileName), channelIndex)
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
	if cfg.ClientMsgPrefix == "" {
		cfg.ClientMsgPrefix = defaultGroupClientPrefix
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NewRegistry()
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

// Warmup is reserved for future warmup traffic and currently does not emit load in v1.
func (w *GroupWorkload) Warmup(ctx context.Context) error { return nil }

// Run sends deterministic group messages for every assigned channel or split partition.
func (w *GroupWorkload) Run(ctx context.Context) error {
	for _, ch := range w.channels {
		indexes := ch.TrafficIndexes
		if len(indexes) == 0 && len(w.cfg.OwnedTrafficPartitions) > 0 {
			indexes = append([]int(nil), w.cfg.OwnedTrafficPartitions...)
		}
		if len(indexes) == 0 {
			indexes = []int{ch.ChannelIndex}
		}
		for _, messageIndex := range indexes {
			if err := w.SendOne(ctx, ch.ChannelIndex, messageIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

// Cooldown is reserved for future draining behavior and currently performs no additional work.
func (w *GroupWorkload) Cooldown(ctx context.Context) error { return nil }

// SendOne sends one deterministic group-channel message.
func (w *GroupWorkload) SendOne(ctx context.Context, channelIndex, messageIndex int) error {
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
	senderUID := ch.OnlineMembers[0]
	sender := w.clients[senderUID]
	payload := w.buildPayload(channelIndex, messageIndex)
	clientSeq := uint64(messageIndex)
	clientMsgNo := w.clientMsgNo(channelIndex, messageIndex)
	pkt := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   ch.ChannelID,
		ChannelType: frame.ChannelTypeGroup,
		Payload:     payload,
	}

	sendStart := time.Now()
	if err := sender.Send(ctx, pkt); err != nil {
		w.recordError("group_send_error", err)
		w.metrics.IncCounter("group_send_error_total", nil)
		return err
	}
	ack, err := w.waitForSendack(ctx, sender, clientSeq, clientMsgNo)
	if err != nil {
		w.recordError("group_send_error", err)
		w.metrics.IncCounter("group_send_error_total", nil)
		return err
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		err := fmt.Errorf("group workload: sendack rejected message %q with reason %s", clientMsgNo, ack.ReasonCode)
		w.recordError("group_send_error", err)
		w.metrics.IncCounter("group_send_error_total", nil)
		return err
	}
	w.metrics.IncCounter("group_send_success_total", nil)
	w.metrics.ObserveLatency("group_send_latency_seconds", nil, time.Since(sendStart))

	recipients := w.verificationMembers(ch)
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
			w.recordError("group_recv_error", err)
			w.metrics.IncCounter("group_recv_error_total", nil)
			return err
		}
		if string(recv.Payload) != string(payload) {
			err := fmt.Errorf("group workload: recv payload mismatch for %q", clientMsgNo)
			w.recordError("group_recv_error", err)
			w.metrics.IncCounter("group_recv_error_total", nil)
			return err
		}
		if w.cfg.RecvAck {
			if err := w.clients[uid].RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
				w.recordError("group_recv_error", err)
				w.metrics.IncCounter("group_recv_error_total", nil)
				return err
			}
		}
		w.metrics.IncCounter("group_recv_success_total", nil)
		w.metrics.ObserveLatency("group_recv_latency_seconds", nil, time.Since(recvStart))
	}
	return nil
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
		channels = append(channels, model.ChannelItem{ChannelID: GroupChannelID(cfg.RunID, cfg.ProfileName, channelIndex), ChannelType: frame.ChannelTypeGroup, Large: large})
	}
	if len(channels) == 0 {
		return nil
	}
	return target.UpsertChannels(ctx, model.BatchChannelsRequest{
		RunID:    cfg.RunID,
		BatchID:  groupChannelsBatchID(cfg),
		Upsert:   true,
		Channels: channels,
	})
}

func addHugeGroupSubscribers(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget) error {
	if cfg.MemberRange.Len() <= 0 || cfg.ChannelRange.Len() <= 0 {
		return nil
	}
	channelID := GroupChannelID(cfg.RunID, cfg.ProfileName, cfg.ChannelRange.Start)
	for start := cfg.MemberRange.Start; start < cfg.MemberRange.End; start += cfg.SubscribersBatchSize {
		end := start + cfg.SubscribersBatchSize
		if end > cfg.MemberRange.End {
			end = cfg.MemberRange.End
		}
		if err := addSubscriberBatch(ctx, target, cfg, channelID, start, end); err != nil {
			return err
		}
	}
	return nil
}

func addSmallGroupSubscribers(ctx context.Context, cfg GroupPrepareConfig, target GroupPrepareTarget) error {
	if cfg.MembersPerChannel <= 0 {
		return nil
	}
	for channelIndex := cfg.ChannelRange.Start; channelIndex < cfg.ChannelRange.End; channelIndex++ {
		memberStart := channelIndex * cfg.MembersPerChannel
		memberEnd := memberStart + cfg.MembersPerChannel
		channelID := GroupChannelID(cfg.RunID, cfg.ProfileName, channelIndex)
		for start := memberStart; start < memberEnd; start += cfg.SubscribersBatchSize {
			end := start + cfg.SubscribersBatchSize
			if end > memberEnd {
				end = memberEnd
			}
			if err := addSubscriberBatch(ctx, target, cfg, channelID, start, end); err != nil {
				return err
			}
		}
	}
	return nil
}

func addSubscriberBatch(ctx context.Context, target GroupPrepareTarget, cfg GroupPrepareConfig, channelID string, start, end int) error {
	subscribers := make([]string, 0, end-start)
	for idx := start; idx < end; idx++ {
		subscribers = append(subscribers, indexedBenchID(cfg.UIDPrefix, idx))
	}
	return target.AddSubscribers(ctx, model.BatchSubscribersRequest{
		RunID:   cfg.RunID,
		BatchID: groupSubscribersBatchID(cfg, start, end),
		Items: []model.SubscriberItem{{
			ChannelID:   channelID,
			ChannelType: frame.ChannelTypeGroup,
			Reset:       false,
			Subscribers: subscribers,
		}},
	})
}

func groupChannelsBatchID(cfg GroupPrepareConfig) string {
	return fmt.Sprintf("%s-channels-%s-%s-%d-%d", cfg.RunID, cfg.ProfileName, cfg.WorkerID, cfg.ChannelRange.Start, cfg.ChannelRange.End)
}

func groupSubscribersBatchID(cfg GroupPrepareConfig, start, end int) string {
	return fmt.Sprintf("%s-subs-%s-%s-%d-%d", cfg.RunID, cfg.ProfileName, cfg.WorkerID, start, end)
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

func (w *GroupWorkload) verificationMembers(ch GroupChannel) []string {
	mode := strings.ToLower(strings.TrimSpace(w.cfg.VerifyRecvMode))
	switch mode {
	case verifyRecvModeFull:
		return append([]string(nil), ch.OnlineMembers...)
	case verifyRecvModeSampled:
		if w.cfg.RecvSampleSize <= 0 {
			return nil
		}
		limit := w.cfg.RecvSampleSize
		if limit > len(ch.OnlineMembers) {
			limit = len(ch.OnlineMembers)
		}
		return append([]string(nil), ch.OnlineMembers[:limit]...)
	default:
		return nil
	}
}

func (w *GroupWorkload) waitForSendack(ctx context.Context, client PersonClient, clientSeq uint64, clientMsgNo string) (*frame.SendackPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.AckTimeout)
	defer cancel()
	for {
		f, err := client.ReadFrame(deadlineCtx)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("group workload: sendack not received for %q: %w", clientMsgNo, err)
			}
			return nil, err
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			continue
		}
		if ack.ClientSeq != clientSeq || ack.ClientMsgNo != clientMsgNo {
			continue
		}
		return ack, nil
	}
}

func (w *GroupWorkload) waitForRecv(ctx context.Context, client PersonClient, channelID, clientMsgNo, senderUID string, expectedMessageID uint64, expectedMessageSeq uint64) (*frame.RecvPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.RecvTimeout)
	defer cancel()
	for {
		f, err := client.ReadFrame(deadlineCtx)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("group workload: recv not received for %q: %w", clientMsgNo, err)
			}
			return nil, err
		}
		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			continue
		}
		if recv.FromUID != senderUID || recv.ChannelID != channelID || recv.ChannelType != frame.ChannelTypeGroup || recv.ClientMsgNo != clientMsgNo {
			continue
		}
		if expectedMessageID > 0 && recv.MessageID != int64(expectedMessageID) {
			continue
		}
		if expectedMessageSeq > 0 && recv.MessageSeq != expectedMessageSeq {
			continue
		}
		return recv, nil
	}
}

func (w *GroupWorkload) buildPayload(channelIndex, messageIndex int) []byte {
	base := w.payloadMarker(channelIndex, messageIndex)
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

func (w *GroupWorkload) payloadMarker(channelIndex, messageIndex int) string {
	return fmt.Sprintf("run=%s profile=%s traffic=%s channel=%d message=%d", w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, channelIndex, messageIndex)
}

func (w *GroupWorkload) clientMsgNo(channelIndex, messageIndex int) string {
	return fmt.Sprintf("%s-%s-%s-%s-ch%d-msg%d", w.cfg.ClientMsgPrefix, w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, channelIndex, messageIndex)
}

func (w *GroupWorkload) recordError(name string, err error) {
	if w == nil || err == nil {
		return
	}
	w.metrics.RecordErrorSample(name, err)
}

func (w *GroupWorkload) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithTimeout(ctx, 5*time.Second)
	}
	return context.WithTimeout(ctx, timeout)
}

func indexedBenchID(prefix string, index int) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "bench"
	}
	return fmt.Sprintf("%s-%d", prefix, index)
}
