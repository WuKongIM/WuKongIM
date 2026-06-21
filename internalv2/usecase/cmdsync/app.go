package cmdsync

import (
	"context"
	"sort"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultActiveScanLimit = 2000
	defaultSyncLimit       = 200
	defaultMaxSyncLimit    = 10000
)

// App owns durable CMD sync and ack business rules.
type App struct {
	states          StateStore
	messages        MessageStore
	records         *SyncRecordCache
	now             func() time.Time
	activeScanLimit int
	defaultLimit    int
	maxLimit        int
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
	return &App{
		states:          opts.States,
		messages:        opts.Messages,
		records:         opts.Records,
		now:             opts.Now,
		activeScanLimit: opts.ActiveScanLimit,
		defaultLimit:    opts.DefaultLimit,
		maxLimit:        opts.MaxLimit,
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
	states, err := a.states.ListConversationActiveView(ctx, uid, a.activeScanLimit)
	if err != nil {
		return SyncResult{}, err
	}

	channels := cmdSyncCandidatesFromStates(states)
	sortSyncChannelCandidates(channels)
	candidates := make([]syncMessageCandidate, 0, limit)
	for _, candidate := range channels {
		key := candidate.key
		msgs, err := a.messages.LoadCommandMessages(ctx, key, candidate.readSeq+1, limit)
		if err != nil {
			return SyncResult{}, err
		}
		for _, msg := range msgs {
			candidates = append(candidates, syncMessageCandidate{
				commandChannelID: key.ChannelID,
				channelType:      key.ChannelType,
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

	result := SyncResult{Messages: make([]SyncedMessage, 0, len(candidates))}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(candidates))
	for _, candidate := range candidates {
		msg := cloneSyncedMessage(candidate.message)
		if sourceID, ok := runtimechannelid.FromCommandChannel(msg.ChannelID); ok {
			msg.ChannelID = sourceID
		}
		result.Messages = append(result.Messages, msg)

		key := CommandChannelKey{ChannelID: candidate.commandChannelID, ChannelType: candidate.channelType}
		record := recordsByKey[key]
		record.CommandChannelID = key.ChannelID
		record.ChannelType = key.ChannelType
		if candidate.message.MessageSeq > record.LastReturnedMsgSeq {
			record.LastReturnedMsgSeq = candidate.message.MessageSeq
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

	states := make([]metadb.ConversationState, 0, len(validRecords))
	for _, record := range validRecords {
		states = append(states, metadb.ConversationState{
			UID:         uid,
			Kind:        metadb.ConversationKindCMD,
			ChannelID:   record.CommandChannelID,
			ChannelType: int64(record.ChannelType),
			ReadSeq:     record.LastReturnedMsgSeq,
			UpdatedAt:   updatedAt,
		})
	}
	if err := a.states.UpsertConversationStates(ctx, states); err != nil {
		return err
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

type syncChannelCandidate struct {
	key      CommandChannelKey
	readSeq  uint64
	activeAt int64
}

type syncMessageCandidate struct {
	commandChannelID string
	channelType      uint8
	message          SyncedMessage
}

func cmdSyncCandidatesFromStates(states []metadb.ConversationState) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(states))
	for _, state := range states {
		if state.Kind != metadb.ConversationKindCMD || state.ChannelID == "" || state.ChannelType <= 0 || state.ChannelType > 255 {
			continue
		}
		candidates = append(candidates, syncChannelCandidate{
			key:      CommandChannelKey{ChannelID: state.ChannelID, ChannelType: uint8(state.ChannelType)},
			readSeq:  maxUint64(state.ReadSeq, state.DeletedToSeq),
			activeAt: state.ActiveAt,
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
	if left.message.ServerTimestampMS != right.message.ServerTimestampMS {
		return left.message.ServerTimestampMS < right.message.ServerTimestampMS
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

func validSyncRecords(records []SyncRecord) []SyncRecord {
	valid := make([]SyncRecord, 0, len(records))
	for _, record := range records {
		if record.LastReturnedMsgSeq == 0 || strings.TrimSpace(record.CommandChannelID) == "" || record.ChannelType == 0 {
			continue
		}
		valid = append(valid, record)
	}
	return valid
}

func cloneSyncedMessage(msg SyncedMessage) SyncedMessage {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
