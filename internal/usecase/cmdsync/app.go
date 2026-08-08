package cmdsync

import (
	"context"
	"sort"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
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
	candidates := make([]syncMessageCandidate, 0, limit)
	cursor := metadb.UserCMDChannelMembershipCursor{}
	for {
		memberships, nextCursor, done, err := a.states.ListUserCMDChannelMembershipPage(ctx, uid, cursor, a.activeScanLimit)
		if err != nil {
			return SyncResult{}, err
		}
		channels := cmdSyncCandidatesFromMemberships(memberships)
		sortSyncChannelCandidates(channels)
		for _, candidate := range channels {
			key := candidate.key
			msgs, err := a.messages.LoadCommandMessages(ctx, key, candidate.fromSeq, limit)
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
		candidates = trimSyncMessageCandidates(candidates, limit)
		if done {
			break
		}
		if nextCursor == cursor {
			return SyncResult{}, ErrStateCursorDidNotAdvance
		}
		cursor = nextCursor
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

	memberships := make([]metadb.UserCMDChannelMembership, 0, len(validRecords))
	for _, record := range validRecords {
		memberships = append(memberships, metadb.UserCMDChannelMembership{
			UID:              uid,
			CommandChannelID: record.CommandChannelID,
			ChannelType:      int64(record.ChannelType),
			AckSeq:           record.LastReturnedMsgSeq,
			UpdatedAt:        updatedAt,
		})
	}
	if err := a.states.AdvanceUserCMDChannelMembershipAcks(ctx, memberships); err != nil {
		return err
	}
	a.records.DeleteIfUnchanged(uid, records)
	return nil
}

// Bind enables durable offline discovery for future messages in one command channel.
func (a *App) Bind(ctx context.Context, cmd BindCommand) error {
	uid, channelID, err := validateBindingIdentity(cmd.UID, cmd.ChannelID, cmd.ChannelType)
	if err != nil {
		return err
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	if a.messages == nil {
		return ErrMessageStoreRequired
	}
	key := CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel(channelID), ChannelType: cmd.ChannelType}
	tail, err := a.messages.CommandChannelTail(ctx, key)
	if err != nil {
		return err
	}
	if tail == ^uint64(0) {
		return ErrSequenceExhausted
	}
	return a.states.UpsertUserCMDChannelMemberships(ctx, []metadb.UserCMDChannelMembership{{
		UID:              uid,
		CommandChannelID: key.ChannelID,
		ChannelType:      int64(key.ChannelType),
		StartSeq:         tail + 1,
		UpdatedAt:        a.now().UnixNano(),
	}})
}

// Unbind disables durable offline discovery without touching command messages.
func (a *App) Unbind(ctx context.Context, cmd UnbindCommand) error {
	uid, channelID, err := validateBindingIdentity(cmd.UID, cmd.ChannelID, cmd.ChannelType)
	if err != nil {
		return err
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	now := a.now().UnixNano()
	return a.states.TombstoneUserCMDChannelMemberships(ctx, []metadb.UserCMDChannelMembership{{
		UID:              uid,
		CommandChannelID: runtimechannelid.ToCommandChannel(channelID),
		ChannelType:      int64(cmd.ChannelType),
		Tombstone:        true,
		TombstoneAt:      now,
		UpdatedAt:        now,
	}})
}

func validateBindingIdentity(uid, channelID string, channelType uint8) (string, string, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return "", "", ErrUIDRequired
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", "", ErrChannelRequired
	}
	if channelType == 0 {
		return "", "", ErrChannelTypeRequired
	}
	return uid, channelID, nil
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
	key     CommandChannelKey
	fromSeq uint64
}

type syncMessageCandidate struct {
	commandChannelID string
	channelType      uint8
	message          SyncedMessage
}

func cmdSyncCandidatesFromMemberships(memberships []metadb.UserCMDChannelMembership) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(memberships))
	for _, membership := range memberships {
		if membership.Tombstone || membership.CommandChannelID == "" || membership.ChannelType <= 0 || membership.ChannelType > 255 || membership.AckSeq == ^uint64(0) {
			continue
		}
		fromSeq := membership.StartSeq
		if fromSeq == 0 {
			fromSeq = 1
		}
		if ackNext := membership.AckSeq + 1; ackNext > fromSeq {
			fromSeq = ackNext
		}
		candidates = append(candidates, syncChannelCandidate{
			key:     CommandChannelKey{ChannelID: membership.CommandChannelID, ChannelType: uint8(membership.ChannelType)},
			fromSeq: fromSeq,
		})
	}
	return candidates
}

func sortSyncChannelCandidates(candidates []syncChannelCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].key.ChannelID != candidates[j].key.ChannelID {
			return candidates[i].key.ChannelID < candidates[j].key.ChannelID
		}
		return candidates[i].key.ChannelType < candidates[j].key.ChannelType
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

func trimSyncMessageCandidates(candidates []syncMessageCandidate, limit int) []syncMessageCandidate {
	sort.Slice(candidates, func(i, j int) bool {
		return syncMessageLess(candidates[i], candidates[j])
	})
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
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
