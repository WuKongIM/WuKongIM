package handler

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const maxLegacyMessageSeq = uint64(^uint32(0))

func (s *service) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	messages := [1]channel.Message{req.Message}
	result, err := s.appendBatch(ctx, appendBatchRequestView{
		channelID:             req.ChannelID,
		messages:              messages[:],
		supportsMessageSeqU64: req.SupportsMessageSeqU64,
		commitMode:            req.CommitMode,
		expectedChannelEpoch:  req.ExpectedChannelEpoch,
		expectedLeaderEpoch:   req.ExpectedLeaderEpoch,
	})
	if err != nil {
		return channel.AppendResult{}, err
	}
	if len(result.Items) == 0 {
		return channel.AppendResult{}, nil
	}
	item := result.Items[0]
	if item.Err != nil {
		return channel.AppendResult{}, item.Err
	}
	return channel.AppendResult{MessageID: item.MessageID, MessageSeq: item.MessageSeq, Message: item.Message}, nil
}

func (s *service) AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return s.appendBatch(ctx, appendBatchRequestView{
		channelID:             req.ChannelID,
		messages:              req.Messages,
		supportsMessageSeqU64: req.SupportsMessageSeqU64,
		commitMode:            req.CommitMode,
		expectedChannelEpoch:  req.ExpectedChannelEpoch,
		expectedLeaderEpoch:   req.ExpectedLeaderEpoch,
	})
}

// appendBatchRequestView is the normalized internal shape shared by single and batch append paths.
type appendBatchRequestView struct {
	channelID             channel.ChannelID
	messages              []channel.Message
	supportsMessageSeqU64 bool
	commitMode            channel.CommitMode
	expectedChannelEpoch  uint64
	expectedLeaderEpoch   uint64
}

func (s *service) appendBatch(ctx context.Context, req appendBatchRequestView) (channel.AppendBatchResult, error) {
	// Keep item results aligned with the original request indexes, including per-item failures.
	result := channel.AppendBatchResult{Items: make([]channel.AppendBatchItemResult, len(req.messages))}
	if len(req.messages) == 0 {
		return result, nil
	}

	// Validate authoritative metadata before touching the runtime or allocating append state.
	key := s.channelKeyForID(req.channelID)
	meta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	if err := validateAppendMeta(meta, req.expectedChannelEpoch, req.expectedLeaderEpoch, req.supportsMessageSeqU64); err != nil {
		return channel.AppendBatchResult{}, err
	}

	group, ok := s.cfg.Runtime.Channel(key)
	if !ok {
		return channel.AppendBatchResult{}, channel.ErrStaleMeta
	}
	state := group.Status()
	if state.Role != channel.ReplicaRoleLeader {
		return channel.AppendBatchResult{}, channel.ErrNotLeader
	}

	// Quorum is the default; only carry explicit non-default commit modes through context.
	if req.commitMode != 0 && req.commitMode != channel.CommitModeQuorum {
		ctx = channel.WithCommitMode(ctx, req.commitMode)
	}

	store := s.cfg.Store.ForChannel(key, req.channelID)
	batch := newAppendBatchDraft(req.channelID, req.messages)
	// Serialize unresolved same-key appends so concurrent retries observe the first durable result.
	unlockIdempotencyKeys := s.lockAppendIdempotencyKeys(batch.idempotencyKeys)
	defer unlockIdempotencyKeys()

	// Idempotency hits are resolved before write admission, so retries are not blocked by new fences.
	batch.resolveIdempotency(store, result.Items)
	if !batch.hasNewMessages() {
		batch.copyDuplicateResults(result.Items)
		return result, nil
	}

	// Admission checks apply only to genuinely new messages that still need replica append.
	batch.applyAdmission(meta, state, result.Items)
	if !batch.hasNewMessages() {
		batch.copyDuplicateResults(result.Items)
		return result, nil
	}

	// Encoding failures remain per-item errors; successfully encoded records append as one ordered batch.
	records, recordItemIndexes := batch.encodeRecords(s.cfg.MessageIDs, result.Items)
	if len(records) == 0 {
		batch.copyDuplicateResults(result.Items)
		return result, nil
	}
	commit, err := group.Append(ctx, records)
	if err != nil {
		if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrLeaseExpired) {
			return channel.AppendBatchResult{}, channel.ErrNotLeader
		}
		return channel.AppendBatchResult{}, err
	}

	// Re-check channel availability after append before exposing committed results to callers.
	currentMeta, err := s.metaForKey(key)
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	if err := validateChannelAvailable(currentMeta); err != nil {
		return channel.AppendBatchResult{}, err
	}

	// Convert the committed base offset into per-record message sequences and mirror batch duplicates.
	batch.fillCommittedResults(result.Items, recordItemIndexes, commit.BaseOffset, meta.Features.MessageSeqFormat)
	batch.copyDuplicateResults(result.Items)
	return result, nil
}

func validateAppendMeta(meta channel.Meta, expectedChannelEpoch, expectedLeaderEpoch uint64, supportsMessageSeqU64 bool) error {
	if err := compatibleWithExpectation(meta, expectedChannelEpoch, expectedLeaderEpoch); err != nil {
		return err
	}
	if err := validateChannelAvailable(meta); err != nil {
		return err
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatU64 && !supportsMessageSeqU64 {
		return channel.ErrProtocolUpgradeRequired
	}
	return nil
}

func validateChannelAvailable(meta channel.Meta) error {
	switch meta.Status {
	case channel.StatusDeleting:
		return channel.ErrChannelDeleting
	case channel.StatusDeleted:
		return channel.ErrChannelNotFound
	default:
		return nil
	}
}

// appendBatchDraft keeps per-item append state while preserving request order in the final result.
type appendBatchDraft struct {
	// channelID is the authoritative channel identity copied into every draft message.
	channelID       channel.ChannelID
	drafts          []channel.Message // Draft messages with channel fields and generated MessageID applied.
	payloadHashes   []uint64          // Precomputed payload hashes reused by idempotency checks and encoding.
	idempotencyKeys []channel.IdempotencyKey
	newIndexes      []int // Request indexes still requiring a replica append.
	duplicateOf     []int // duplicateOf[i] points at the first same-key item in this batch, or -1.
}

func newAppendBatchDraft(channelID channel.ChannelID, messages []channel.Message) appendBatchDraft {
	batch := appendBatchDraft{
		channelID:       channelID,
		drafts:          make([]channel.Message, len(messages)),
		payloadHashes:   make([]uint64, len(messages)),
		idempotencyKeys: make([]channel.IdempotencyKey, 0, len(messages)),
		duplicateOf:     make([]int, len(messages)),
	}
	for i := range batch.duplicateOf {
		batch.duplicateOf[i] = -1
	}
	for i, msg := range messages {
		draft := msg
		draft.ChannelID = channelID.ID
		draft.ChannelType = channelID.Type
		batch.drafts[i] = draft
		batch.payloadHashes[i] = hashPayload(draft.Payload)
		if key, ok := idempotencyKeyForDraft(channelID, draft); ok {
			batch.idempotencyKeys = append(batch.idempotencyKeys, key)
		}
	}
	return batch
}

func (b *appendBatchDraft) resolveIdempotency(store appendIdempotencyStore, items []channel.AppendBatchItemResult) {
	idempotentNew := make(map[channel.IdempotencyKey]int, len(b.idempotencyKeys))
	for i, draft := range b.drafts {
		idKey, ok := idempotencyKeyForDraft(b.channelID, draft)
		if !ok {
			b.newIndexes = append(b.newIndexes, i)
			continue
		}
		stored, found, err := resolveIdempotentAppendFromStore(store, idKey, b.payloadHashes[i])
		if err != nil {
			items[i].Err = err
			continue
		}
		if found {
			items[i] = appendResultToBatchItem(stored)
			continue
		}
		if first, ok := idempotentNew[idKey]; ok {
			if b.payloadHashes[first] != b.payloadHashes[i] {
				items[i].Err = channel.ErrIdempotencyConflict
				continue
			}
			b.duplicateOf[i] = first
			continue
		}
		idempotentNew[idKey] = i
		b.newIndexes = append(b.newIndexes, i)
	}
}

func (b *appendBatchDraft) applyAdmission(meta channel.Meta, state channel.ReplicaState, items []channel.AppendBatchItemResult) {
	if meta.WriteFence.BlocksAppend() {
		b.rejectNew(items, channel.ErrWriteFenced)
		return
	}
	if meta.Features.MessageSeqFormat == channel.MessageSeqFormatLegacyU32 {
		b.applyLegacySeqBudget(state.HW, items)
	}
}

func (b *appendBatchDraft) applyLegacySeqBudget(committedHW uint64, items []channel.AppendBatchItemResult) {
	if committedHW >= maxLegacyMessageSeq {
		b.rejectNew(items, channel.ErrMessageSeqExhausted)
		return
	}
	remaining := int(maxLegacyMessageSeq - committedHW)
	if remaining >= len(b.newIndexes) {
		return
	}
	for _, idx := range b.newIndexes[remaining:] {
		items[idx].Err = channel.ErrMessageSeqExhausted
	}
	b.newIndexes = b.newIndexes[:remaining]
}

func (b *appendBatchDraft) rejectNew(items []channel.AppendBatchItemResult, err error) {
	for _, idx := range b.newIndexes {
		items[idx].Err = err
	}
	b.newIndexes = b.newIndexes[:0]
}

func (b *appendBatchDraft) encodeRecords(generator channel.MessageIDGenerator, items []channel.AppendBatchItemResult) ([]channel.Record, []int) {
	records := make([]channel.Record, 0, len(b.newIndexes))
	recordItemIndexes := make([]int, 0, len(b.newIndexes))
	for _, idx := range b.newIndexes {
		draft := b.drafts[idx]
		draft.MessageID = generator.Next()
		encoded, err := encodeMessageWithPayloadHash(draft, b.payloadHashes[idx])
		if err != nil {
			items[idx].Err = err
			continue
		}
		b.drafts[idx] = draft
		records = append(records, channel.Record{
			ID:        draft.MessageID,
			Payload:   encoded,
			SizeBytes: len(encoded),
		})
		recordItemIndexes = append(recordItemIndexes, idx)
	}
	return records, recordItemIndexes
}

func (b *appendBatchDraft) fillCommittedResults(items []channel.AppendBatchItemResult, recordItemIndexes []int, baseOffset uint64, format channel.MessageSeqFormat) {
	for recordIndex, itemIndex := range recordItemIndexes {
		messageSeq := baseOffset + uint64(recordIndex) + 1
		if format == channel.MessageSeqFormatLegacyU32 && messageSeq > maxLegacyMessageSeq {
			items[itemIndex].Err = channel.ErrMessageSeqExhausted
			continue
		}
		committed := b.drafts[itemIndex]
		committed.MessageSeq = messageSeq
		items[itemIndex] = channel.AppendBatchItemResult{
			MessageID:  committed.MessageID,
			MessageSeq: messageSeq,
			Message:    committed,
		}
	}
}

func (b *appendBatchDraft) copyDuplicateResults(items []channel.AppendBatchItemResult) {
	for i, first := range b.duplicateOf {
		if first >= 0 && first < len(items) {
			items[i] = items[first]
		}
	}
}

func (b *appendBatchDraft) hasNewMessages() bool {
	return len(b.newIndexes) > 0
}

func idempotencyKeyForDraft(channelID channel.ChannelID, draft channel.Message) (channel.IdempotencyKey, bool) {
	if draft.FromUID == "" || draft.ClientMsgNo == "" {
		return channel.IdempotencyKey{}, false
	}
	return channel.IdempotencyKey{
		ChannelID:   channelID,
		FromUID:     draft.FromUID,
		ClientMsgNo: draft.ClientMsgNo,
	}, true
}

func appendResultToBatchItem(result channel.AppendResult) channel.AppendBatchItemResult {
	return channel.AppendBatchItemResult{
		MessageID:  result.MessageID,
		MessageSeq: result.MessageSeq,
		Message:    result.Message,
	}
}

type appendIdempotencyStore interface {
	LookupIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error)
	GetMessageBySeq(seq uint64) (channel.Message, bool, error)
}

func resolveIdempotentAppendFromStore(store appendIdempotencyStore, key channel.IdempotencyKey, payloadHash uint64) (channel.AppendResult, bool, error) {
	entry, storedPayloadHash, ok, err := store.LookupIdempotency(key)
	if err != nil {
		return channel.AppendResult{}, false, err
	}
	if !ok {
		return channel.AppendResult{}, false, nil
	}
	if storedPayloadHash != payloadHash {
		return channel.AppendResult{}, true, channel.ErrIdempotencyConflict
	}
	msg, ok, err := store.GetMessageBySeq(entry.MessageSeq)
	if err != nil {
		return channel.AppendResult{}, true, err
	}
	if !ok {
		return channel.AppendResult{}, true, channel.ErrStaleMeta
	}
	msg.MessageSeq = entry.MessageSeq
	return channel.AppendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}, true, nil
}
