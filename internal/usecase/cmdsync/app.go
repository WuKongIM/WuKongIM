package cmdsync

import (
	"context"
	"sort"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultActiveScanLimit = 2000
	defaultSyncLimit       = 200
	defaultMaxSyncLimit    = 10000
)

// Options configures the CMD sync usecase.
type Options struct {
	States          StateStore
	Messages        MessageStore
	Pending         ConversationPendingStore
	Records         *SyncRecordCache
	Now             func() time.Time
	ActiveScanLimit int
	DefaultLimit    int
	MaxLimit        int
	Logger          wklog.Logger
}

// App owns durable CMD sync and ack business rules.
type App struct {
	states          StateStore
	messages        MessageStore
	pending         ConversationPendingStore
	records         *SyncRecordCache
	now             func() time.Time
	activeScanLimit int
	defaultLimit    int
	maxLimit        int
	logger          wklog.Logger
}

// New creates a CMD sync app with safe defaults.
func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ActiveScanLimit <= 0 {
		opts.ActiveScanLimit = defaultActiveScanLimit
	}
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = defaultSyncLimit
	}
	if opts.MaxLimit <= 0 {
		opts.MaxLimit = defaultMaxSyncLimit
	}
	if opts.DefaultLimit > opts.MaxLimit {
		opts.DefaultLimit = opts.MaxLimit
	}
	if opts.Records == nil {
		opts.Records = NewSyncRecordCache(SyncRecordCacheOptions{Now: opts.Now, MaxRecordsPerUID: opts.MaxLimit})
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &App{
		states:          opts.States,
		messages:        opts.Messages,
		pending:         opts.Pending,
		records:         opts.Records,
		now:             opts.Now,
		activeScanLimit: opts.ActiveScanLimit,
		defaultLimit:    opts.DefaultLimit,
		maxLimit:        opts.MaxLimit,
		logger:          opts.Logger,
	}
}

// Sync loads durable command-channel messages and records the latest sync generation.
func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error) {
	uid := strings.TrimSpace(query.UID)
	if uid == "" {
		return SyncResult{}, ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return SyncResult{}, ErrStateStoreRequired
	}
	if a.messages == nil {
		return SyncResult{}, ErrMessageStoreRequired
	}

	limit := a.normalizeLimit(query.Limit)
	states, err := a.states.ListCMDConversationActive(ctx, uid, a.activeScanLimit)
	if err != nil {
		return SyncResult{}, err
	}

	channels := cmdSyncCandidatesFromStates(states)
	if a.pending != nil {
		channels = mergePendingViews(channels, a.pending.ListPending(ctx, uid, a.activeScanLimit))
	}
	sortSyncChannelCandidates(channels)

	candidates := make([]syncMessageCandidate, 0, limit)
	for _, candidate := range channels {
		fromSeq := candidate.readSeq + 1
		key := candidate.key
		msgs, err := a.messages.LoadCommandMessages(ctx, key, fromSeq, limit)
		if err != nil {
			return SyncResult{}, err
		}
		for _, msg := range msgs {
			candidates = append(candidates, syncMessageCandidate{
				commandChannelID: key.ChannelID,
				channelType:      key.ChannelType,
				pending:          candidate.pending,
				pendingActiveAt:  candidate.pendingActiveAt,
				message:          msg,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return syncMessageLess(candidates[i], candidates[j])
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	result := SyncResult{Messages: make([]channel.Message, 0, len(candidates))}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(candidates))
	for _, candidate := range candidates {
		msg := candidate.message
		if sourceID, ok := runtimechannelid.FromCommandChannel(msg.ChannelID); ok {
			msg.ChannelID = sourceID
		}
		result.Messages = append(result.Messages, msg)

		key := CommandChannelKey{ChannelID: candidate.commandChannelID, ChannelType: candidate.channelType}
		record := recordsByKey[key]
		record.CommandChannelID = key.ChannelID
		record.ChannelType = key.ChannelType
		if msg.MessageSeq > record.LastReturnedMsgSeq {
			record.LastReturnedMsgSeq = msg.MessageSeq
		}
		if candidate.pending {
			record.Pending = true
			record.PendingActiveAt = maxInt64(record.PendingActiveAt, candidate.pendingActiveAt)
		}
		recordsByKey[key] = record
	}
	a.records.Replace(uid, syncRecordsFromMap(recordsByKey))
	return result, nil
}

// SyncAck advances read cursors for the latest sync generation only.
func (a *App) SyncAck(ctx context.Context, cmd SyncAckCommand) error {
	uid := strings.TrimSpace(cmd.UID)
	if uid == "" {
		return ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}

	records := a.records.Peek(uid)
	if len(records) == 0 {
		return nil
	}
	updatedAt := a.now().UnixNano()
	validRecords := validSyncRecords(records)
	if len(validRecords) == 0 {
		a.records.DeleteIfUnchanged(uid, records)
		return nil
	}

	patches := make([]metadb.CMDConversationReadPatch, 0, len(validRecords))
	for _, record := range validRecords {
		patches = append(patches, metadb.CMDConversationReadPatch{
			UID:         uid,
			ChannelID:   record.CommandChannelID,
			ChannelType: int64(record.ChannelType),
			ReadSeq:     record.LastReturnedMsgSeq,
			UpdatedAt:   updatedAt,
		})
	}
	if err := a.states.AdvanceCMDConversationReadSeq(ctx, patches); err != nil {
		return err
	}

	if a.pending == nil {
		a.records.DeleteIfUnchanged(uid, records)
		return nil
	}
	pendingByKey := pendingViewsByKey(a.pending.ListPending(ctx, uid, a.activeScanLimit))
	for _, record := range validRecords {
		key := CommandChannelKey{ChannelID: record.CommandChannelID, ChannelType: record.ChannelType}
		activeAt, pendingBacked := pendingRecordActiveAt(record, pendingByKey[key])
		if pendingBacked {
			state := metadb.CMDConversationState{
				UID:         uid,
				ChannelID:   key.ChannelID,
				ChannelType: int64(key.ChannelType),
				ReadSeq:     record.LastReturnedMsgSeq,
				ActiveAt:    activeAt,
				UpdatedAt:   updatedAt,
			}
			if err := a.states.UpsertCMDConversationStates(ctx, []metadb.CMDConversationState{state}); err != nil {
				return err
			}
			if err := a.pending.MarkSynced(ctx, uid, key, record.LastReturnedMsgSeq); err != nil {
				a.logger.Warn("mark pending CMD conversation synced failed", wklog.String("uid", uid), wklog.String("channelID", key.ChannelID), wklog.Uint64("throughSeq", record.LastReturnedMsgSeq), wklog.Error(err))
			}
		}
	}
	a.records.DeleteIfUnchanged(uid, records)
	return nil
}

func (a *App) normalizeLimit(limit int) int {
	if limit <= 0 {
		return a.defaultLimit
	}
	if limit > a.maxLimit {
		return a.maxLimit
	}
	return limit
}

type syncMessageCandidate struct {
	commandChannelID string
	channelType      uint8
	pending          bool
	pendingActiveAt  int64
	message          channel.Message
}

type syncChannelCandidate struct {
	key             CommandChannelKey
	readSeq         uint64
	activeAt        int64
	pending         bool
	pendingActiveAt int64
}

func cmdSyncCandidatesFromStates(states []metadb.CMDConversationState) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(states))
	for _, state := range states {
		candidates = append(candidates, syncChannelCandidate{
			key:      CommandChannelKey{ChannelID: state.ChannelID, ChannelType: uint8(state.ChannelType)},
			readSeq:  maxUint64(state.ReadSeq, state.DeletedToSeq),
			activeAt: state.ActiveAt,
		})
	}
	return candidates
}

func mergePendingViews(candidates []syncChannelCandidate, views []PendingConversationView) []syncChannelCandidate {
	index := make(map[CommandChannelKey]int, len(candidates))
	for i, candidate := range candidates {
		index[candidate.key] = i
	}
	for _, view := range views {
		key := CommandChannelKey{ChannelID: view.CommandChannelID, ChannelType: view.ChannelType}
		if i, ok := index[key]; ok {
			candidates[i].readSeq = maxUint64(candidates[i].readSeq, view.ReadSeq)
			candidates[i].activeAt = maxInt64(candidates[i].activeAt, view.ActiveAt)
			candidates[i].pending = true
			candidates[i].pendingActiveAt = maxInt64(candidates[i].pendingActiveAt, view.ActiveAt)
			continue
		}
		index[key] = len(candidates)
		candidates = append(candidates, syncChannelCandidate{
			key:             key,
			readSeq:         view.ReadSeq,
			activeAt:        view.ActiveAt,
			pending:         true,
			pendingActiveAt: view.ActiveAt,
		})
	}
	return candidates
}

func sortSyncChannelCandidates(candidates []syncChannelCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].activeAt != candidates[j].activeAt {
			return candidates[i].activeAt > candidates[j].activeAt
		}
		if candidates[i].key.ChannelType != candidates[j].key.ChannelType {
			return candidates[i].key.ChannelType < candidates[j].key.ChannelType
		}
		return candidates[i].key.ChannelID < candidates[j].key.ChannelID
	})
}

func syncMessageLess(left, right syncMessageCandidate) bool {
	if left.message.Timestamp != right.message.Timestamp {
		return left.message.Timestamp < right.message.Timestamp
	}
	if left.commandChannelID != right.commandChannelID {
		return left.commandChannelID < right.commandChannelID
	}
	if left.channelType != right.channelType {
		return left.channelType < right.channelType
	}
	if left.message.MessageSeq != right.message.MessageSeq {
		return left.message.MessageSeq < right.message.MessageSeq
	}
	return left.message.MessageID < right.message.MessageID
}

func syncRecordsFromMap(recordsByKey map[CommandChannelKey]SyncRecord) []SyncRecord {
	if len(recordsByKey) == 0 {
		return nil
	}
	keys := make([]CommandChannelKey, 0, len(recordsByKey))
	for key := range recordsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChannelType != keys[j].ChannelType {
			return keys[i].ChannelType < keys[j].ChannelType
		}
		return keys[i].ChannelID < keys[j].ChannelID
	})
	records := make([]SyncRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, recordsByKey[key])
	}
	return records
}

func pendingViewsByKey(views []PendingConversationView) map[CommandChannelKey]PendingConversationView {
	out := make(map[CommandChannelKey]PendingConversationView, len(views))
	for _, view := range views {
		key := CommandChannelKey{ChannelID: view.CommandChannelID, ChannelType: view.ChannelType}
		out[key] = view
	}
	return out
}

func validSyncRecords(records []SyncRecord) []SyncRecord {
	valid := make([]SyncRecord, 0, len(records))
	for _, record := range records {
		if record.LastReturnedMsgSeq == 0 || !runtimechannelid.IsCommandChannel(record.CommandChannelID) {
			continue
		}
		valid = append(valid, record)
	}
	return valid
}

func pendingRecordActiveAt(record SyncRecord, view PendingConversationView) (int64, bool) {
	if record.Pending {
		return maxInt64(record.PendingActiveAt, view.ActiveAt), true
	}
	if view.CommandChannelID != "" {
		return view.ActiveAt, true
	}
	return 0, false
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
