package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultCommittedReplayCursorName = "committed"
	defaultCommittedReplayBatchSize  = 512
	defaultCommittedReplayMaxBytes   = 4 << 20
	defaultCommittedReplayInterval   = 30 * time.Second
)

type committedReplayChannel struct {
	// Key is the durable channel log key used by the channel store.
	Key channel.ChannelKey
	// ID is the decoded channel identity used by channel runtime status APIs.
	ID channel.ChannelID
}

type committedReplayChannelState struct {
	// CommittedSeq is the local committed frontier visible for replay.
	CommittedSeq uint64
	// MinAvailableSeq is the first sequence that retention allows replay to read.
	MinAvailableSeq uint64
}

type committedReplayLog interface {
	ListCommittedReplayChannels(context.Context) ([]committedReplayChannel, error)
	LoadCommittedDispatchCursor(context.Context, channel.ChannelKey, string) (uint64, bool, error)
	StoreCommittedDispatchCursor(context.Context, channel.ChannelKey, string, uint64) error
	AdvanceCommittedDispatchCursorDurable(context.Context, channel.ChannelKey, string, uint64) error
	CommittedReplayState(context.Context, channel.ChannelKey, channel.ChannelID) (committedReplayChannelState, error)
	LoadCommittedMessages(context.Context, channel.ChannelKey, channel.ChannelID, uint64, int, int) ([]channel.Message, error)
}

type committedReplayDelivery interface {
	SubmitCommitted(context.Context, deliveryruntime.CommittedEnvelope) error
}

type committedReplayConversation interface {
	SubmitCommitted(context.Context, channel.Message) error
}

type committedReplayMetrics interface {
	SetCommittedReplayLag(channelType string, lag uint64)
	ObserveCommittedReplayPass(result string, dur time.Duration)
}

type committedReplayerConfig struct {
	// Log is the durable source of committed messages and replay cursors.
	Log committedReplayLog
	// Delivery receives replayed committed messages for realtime fanout.
	Delivery committedReplayDelivery
	// Conversation receives replayed committed messages as best-effort active hints.
	Conversation committedReplayConversation
	// CursorName separates replay lanes in the per-channel cursor keyspace.
	CursorName string
	// BatchSize bounds messages processed per channel pass.
	BatchSize int
	// MaxBytes bounds bytes loaded per channel pass.
	MaxBytes int
	// Interval controls the steady-state background replay cadence.
	Interval time.Duration
	// Logger records replay failures without failing the send hot path.
	Logger wklog.Logger
	// Metrics observes replay pass duration and per-channel committed lag.
	Metrics committedReplayMetrics
}

// committedReplayer repairs missed async side effects from the durable channel log.
type committedReplayer struct {
	cfg      committedReplayerConfig
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	stopping bool

	dirtyMu sync.Mutex
	dirty   map[channel.ChannelKey]committedReplayChannel
}

func newCommittedReplayer(cfg committedReplayerConfig) *committedReplayer {
	if cfg.CursorName == "" {
		cfg.CursorName = defaultCommittedReplayCursorName
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultCommittedReplayBatchSize
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultCommittedReplayMaxBytes
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultCommittedReplayInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return &committedReplayer{cfg: cfg}
}

// Start launches the lightweight replay loop. It does not block ingress startup;
// any missed committed side effects are replayed from the durable channel log.
func (r *committedReplayer) Start(ctx context.Context) error {
	if r == nil || r.cfg.Log == nil {
		return channel.ErrInvalidConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil
	}
	if r.stopping {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.run(runCtx, r.done)
	return nil
}

func (r *committedReplayer) Stop() error {
	return r.StopContext(context.Background())
}

// SubmitCommitted marks the committed channel dirty so steady-state replay can
// repair known updates without scanning every persisted channel key.
func (r *committedReplayer) SubmitCommitted(_ context.Context, event messageevents.MessageCommitted) error {
	if r == nil {
		return nil
	}
	id := channel.ChannelID{ID: event.Message.ChannelID, Type: event.Message.ChannelType}
	if id.ID == "" {
		return nil
	}
	r.markDirty(committedReplayChannel{
		Key: channelhandler.KeyFromChannelID(id),
		ID:  id,
	})
	return nil
}

// StopContext cancels replay and bounds shutdown with the caller context.
func (r *committedReplayer) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	if cancel == nil && r.stopping && done != nil {
		r.mu.Unlock()
		return r.waitStop(ctx, done)
	}
	r.cancel = nil
	if cancel != nil {
		r.stopping = true
	}
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return r.waitStop(ctx, done)
}

func (r *committedReplayer) waitStop(ctx context.Context, done chan struct{}) error {
	select {
	case <-done:
		r.finishStop(done)
		return nil
	case <-ctx.Done():
		go func() {
			<-done
			r.finishStop(done)
		}()
		return ctx.Err()
	}
}

func (r *committedReplayer) finishStop(done chan struct{}) {
	r.mu.Lock()
	if r.done == done {
		r.done = nil
		r.stopping = false
	}
	r.mu.Unlock()
}

func (r *committedReplayer) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	r.runFullScanOnceAndLog(ctx)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnceAndLog(ctx)
		}
	}
}

func (r *committedReplayer) runFullScanOnceAndLog(ctx context.Context) {
	if err := r.runOnce(ctx, true); err != nil && !errors.Is(err, context.Canceled) {
		r.cfg.Logger.Warn("committed replay pass failed", wklog.Error(err))
	}
}

func (r *committedReplayer) runOnceAndLog(ctx context.Context) {
	if err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.cfg.Logger.Warn("committed replay pass failed", wklog.Error(err))
	}
}

// RunOnce replays committed messages after the persisted cursor and advances the
// cursor after delivery side effects have accepted the batch. Conversation active
// hints are best-effort and do not gate cursor advancement.
func (r *committedReplayer) RunOnce(ctx context.Context) (err error) {
	return r.runOnce(ctx, false)
}

func (r *committedReplayer) runOnce(ctx context.Context, forceFullScan bool) (err error) {
	startedAt := time.Now()
	lagByType := map[uint8]uint64(nil)
	defer func() {
		r.recordReplayLagSnapshot(lagByType)
		r.recordReplayPass(committedReplayPassResult(err), time.Since(startedAt))
	}()
	if r == nil || r.cfg.Log == nil {
		return channel.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channels, err := r.replayChannels(ctx, forceFullScan)
	if err != nil {
		return err
	}
	lagByType = make(map[uint8]uint64, len(channels))
	for _, ch := range channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		lag, err := r.replayChannel(ctx, ch)
		if current, ok := lagByType[ch.ID.Type]; !ok || lag > current {
			lagByType[ch.ID.Type] = lag
		}
		if err != nil {
			return err
		}
		if lag == 0 {
			r.clearDirty(ch.Key)
		}
	}
	return nil
}

func (r *committedReplayer) replayChannels(ctx context.Context, forceFullScan bool) ([]committedReplayChannel, error) {
	if !forceFullScan {
		if channels := r.dirtyReplayChannels(); len(channels) > 0 {
			return channels, nil
		}
	}
	return r.cfg.Log.ListCommittedReplayChannels(ctx)
}

func (r *committedReplayer) markDirty(ch committedReplayChannel) {
	if ch.Key == "" {
		return
	}
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	if r.dirty == nil {
		r.dirty = make(map[channel.ChannelKey]committedReplayChannel)
	}
	r.dirty[ch.Key] = ch
}

func (r *committedReplayer) dirtyReplayChannels() []committedReplayChannel {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	if len(r.dirty) == 0 {
		return nil
	}
	channels := make([]committedReplayChannel, 0, len(r.dirty))
	for _, ch := range r.dirty {
		channels = append(channels, ch)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Key < channels[j].Key })
	return channels
}

func (r *committedReplayer) clearDirty(key channel.ChannelKey) {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	delete(r.dirty, key)
}

func (r *committedReplayer) replayChannel(ctx context.Context, ch committedReplayChannel) (uint64, error) {
	cursor, ok, err := r.cfg.Log.LoadCommittedDispatchCursor(ctx, ch.Key, r.cfg.CursorName)
	if err != nil {
		return 0, err
	}
	if !ok {
		cursor = 0
	}
	lag := uint64(0)
	for {
		state, err := r.cfg.Log.CommittedReplayState(ctx, ch.Key, ch.ID)
		if err != nil {
			return lag, err
		}
		minAvailableSeq := normalizeCommittedReplayMinAvailableSeq(state.MinAvailableSeq)
		startSeq := nextCommittedReplaySeq(cursor)
		if startSeq < minAvailableSeq {
			cursor = minAvailableSeq - 1
			if err := r.cfg.Log.AdvanceCommittedDispatchCursorDurable(ctx, ch.Key, r.cfg.CursorName, cursor); err != nil {
				return lag, err
			}
			startSeq = minAvailableSeq
		}
		lag = replayLag(cursor, state.CommittedSeq)
		if state.CommittedSeq == 0 || cursor >= state.CommittedSeq {
			return lag, nil
		}

		messages, err := r.cfg.Log.LoadCommittedMessages(ctx, ch.Key, ch.ID, startSeq, r.cfg.BatchSize, r.cfg.MaxBytes)
		if err != nil {
			return lag, err
		}
		if len(messages) == 0 {
			return lag, nil
		}
		lastSeq := cursor
		for _, msg := range messages {
			if err := ctx.Err(); err != nil {
				return lag, err
			}
			if msg.MessageSeq <= cursor {
				continue
			}
			if msg.MessageSeq != lastSeq+1 {
				return lag, channel.ErrCorruptState
			}
			if msg.MessageSeq > state.CommittedSeq {
				break
			}
			if err := r.submitMessage(ctx, msg); err != nil {
				return lag, err
			}
			lastSeq = msg.MessageSeq
		}
		if lastSeq == cursor {
			return lag, nil
		}
		if err := r.cfg.Log.StoreCommittedDispatchCursor(ctx, ch.Key, r.cfg.CursorName, lastSeq); err != nil {
			return lag, err
		}
		cursor = lastSeq
		lag = replayLag(cursor, state.CommittedSeq)
		if len(messages) < r.cfg.BatchSize {
			return lag, nil
		}
	}
}

func (r *committedReplayer) submitMessage(ctx context.Context, msg channel.Message) error {
	if r.cfg.Delivery != nil {
		if err := r.cfg.Delivery.SubmitCommitted(ctx, deliveryruntime.CommittedEnvelope{Message: msg}); err != nil {
			return err
		}
	}
	if r.cfg.Conversation != nil {
		if err := r.cfg.Conversation.SubmitCommitted(ctx, msg); err != nil {
			r.cfg.Logger.Warn(
				"committed replay active hint submit failed",
				wklog.String("channelID", msg.ChannelID),
				wklog.Int("channelType", int(msg.ChannelType)),
				wklog.Uint64("messageSeq", msg.MessageSeq),
				wklog.Error(err),
			)
		}
	}
	return nil
}

func (r *committedReplayer) recordReplayLag(channelType uint8, lag uint64) {
	if r == nil || r.cfg.Metrics == nil {
		return
	}
	r.cfg.Metrics.SetCommittedReplayLag(deliveryChannelTypeLabel(channelType), lag)
}

func (r *committedReplayer) recordReplayLagSnapshot(lagByType map[uint8]uint64) {
	if r == nil || r.cfg.Metrics == nil {
		return
	}
	for channelType, lag := range lagByType {
		r.recordReplayLag(channelType, lag)
	}
}

func (r *committedReplayer) recordReplayPass(result string, dur time.Duration) {
	if r == nil || r.cfg.Metrics == nil {
		return
	}
	r.cfg.Metrics.ObserveCommittedReplayPass(result, dur)
}

func committedReplayPassResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

func replayLag(cursor, committedSeq uint64) uint64 {
	if committedSeq <= cursor {
		return 0
	}
	return committedSeq - cursor
}

func nextCommittedReplaySeq(cursor uint64) uint64 {
	if cursor == ^uint64(0) {
		return cursor
	}
	return cursor + 1
}

func normalizeCommittedReplayMinAvailableSeq(seq uint64) uint64 {
	if seq == 0 {
		return 1
	}
	return seq
}

type channelStoreCommittedReplayLog struct {
	// engine owns the durable structured channel message rows and cursors.
	engine *channelstore.Engine
	// status reports the local channel runtime committed frontier.
	status interface {
		Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
	// localNodeID limits replay to channels led by this node.
	localNodeID uint64
}

func (l channelStoreCommittedReplayLog) ListCommittedReplayChannels(ctx context.Context) ([]committedReplayChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys, err := l.engine.ListChannelKeys()
	if err != nil {
		return nil, err
	}
	channels := make([]committedReplayChannel, 0, len(keys))
	for _, key := range keys {
		id, err := channelhandler.ParseChannelKey(key)
		if err != nil {
			return nil, err
		}
		channels = append(channels, committedReplayChannel{Key: key, ID: id})
	}
	return channels, nil
}

func (l channelStoreCommittedReplayLog) LoadCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, name string) (uint64, bool, error) {
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return 0, false, err
	}
	return l.engine.ForChannel(key, id).LoadCommittedDispatchCursor(name)
}

func (l channelStoreCommittedReplayLog) StoreCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, name string, seq uint64) error {
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return err
	}
	return l.engine.ForChannel(key, id).StoreCommittedDispatchCursor(name, seq)
}

func (l channelStoreCommittedReplayLog) AdvanceCommittedDispatchCursorDurable(_ context.Context, key channel.ChannelKey, name string, seq uint64) error {
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return err
	}
	return l.engine.ForChannel(key, id).AdvanceCommittedDispatchCursorDurable(name, seq)
}

func (l channelStoreCommittedReplayLog) CommittedReplayState(_ context.Context, _ channel.ChannelKey, id channel.ChannelID) (committedReplayChannelState, error) {
	if l.status == nil {
		return committedReplayChannelState{}, nil
	}
	status, err := l.status.Status(id)
	if err != nil {
		return committedReplayChannelState{}, nil
	}
	if l.localNodeID != 0 && uint64(status.Leader) != l.localNodeID {
		return committedReplayChannelState{}, nil
	}
	return committedReplayChannelState{
		CommittedSeq:    status.CommittedSeq,
		MinAvailableSeq: status.MinAvailableSeq,
	}, nil
}

func (l channelStoreCommittedReplayLog) LoadCommittedMessages(_ context.Context, key channel.ChannelKey, id channel.ChannelID, fromSeq uint64, limit int, maxBytes int) ([]channel.Message, error) {
	return l.engine.ForChannel(key, id).ListMessagesBySeq(fromSeq, limit, maxBytes, false)
}
