package cmdsync

import (
	"context"
	"sort"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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
		opts.Records = NewSyncRecordCache(SyncRecordCacheOptions{Now: opts.Now})
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &App{
		states:          opts.States,
		messages:        opts.Messages,
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

	candidates := make([]syncMessageCandidate, 0, limit)
	for _, state := range states {
		key := CommandChannelKey{ChannelID: state.ChannelID, ChannelType: uint8(state.ChannelType)}
		fromSeq := maxUint64(state.ReadSeq, state.DeletedToSeq) + 1
		msgs, err := a.messages.LoadCommandMessages(ctx, key, fromSeq, limit)
		if err != nil {
			return SyncResult{}, err
		}
		for _, msg := range msgs {
			candidates = append(candidates, syncMessageCandidate{
				commandChannelID: state.ChannelID,
				channelType:      uint8(state.ChannelType),
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
	recordsByKey := make(map[CommandChannelKey]uint64, len(candidates))
	for _, candidate := range candidates {
		msg := candidate.message
		if sourceID, ok := runtimechannelid.FromCommandChannel(msg.ChannelID); ok {
			msg.ChannelID = sourceID
		}
		result.Messages = append(result.Messages, msg)

		key := CommandChannelKey{ChannelID: candidate.commandChannelID, ChannelType: candidate.channelType}
		if msg.MessageSeq > recordsByKey[key] {
			recordsByKey[key] = msg.MessageSeq
		}
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

	records := a.records.Pop(uid)
	if len(records) == 0 {
		return nil
	}
	updatedAt := a.now().UnixNano()
	patches := make([]metadb.CMDConversationReadPatch, 0, len(records))
	for _, record := range records {
		if record.LastReturnedMsgSeq == 0 || !runtimechannelid.IsCommandChannel(record.CommandChannelID) {
			continue
		}
		patches = append(patches, metadb.CMDConversationReadPatch{
			UID:         uid,
			ChannelID:   record.CommandChannelID,
			ChannelType: int64(record.ChannelType),
			ReadSeq:     record.LastReturnedMsgSeq,
			UpdatedAt:   updatedAt,
		})
	}
	if len(patches) == 0 {
		return nil
	}
	return a.states.AdvanceCMDConversationReadSeq(ctx, patches)
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
	message          channel.Message
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

func syncRecordsFromMap(recordsByKey map[CommandChannelKey]uint64) []SyncRecord {
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
		records = append(records, SyncRecord{
			CommandChannelID:   key.ChannelID,
			ChannelType:        key.ChannelType,
			LastReturnedMsgSeq: recordsByKey[key],
		})
	}
	return records
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
