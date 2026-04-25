package store

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ChannelStore owns the channel-scoped durable state and compatibility adapters.
type ChannelStore struct {
	engine   *Engine
	key      channel.ChannelKey
	id       channel.ChannelID
	messages messageTable

	writeMu sync.Mutex
	mu      sync.Mutex
	leo     atomic.Uint64
	loaded  atomic.Bool

	writeInProgress    atomic.Bool
	durableCommitCount atomic.Int64
}

func (s *ChannelStore) recordDurableCommit() {
	if s == nil {
		return
	}
	s.durableCommitCount.Add(1)
}

func (s *ChannelStore) publishDurableWrite(nextLEO uint64) {
	if s == nil {
		return
	}
	s.recordDurableCommit()
	s.mu.Lock()
	s.leo.Store(nextLEO)
	s.loaded.Store(true)
	s.writeInProgress.Store(false)
	s.mu.Unlock()
}

func (s *ChannelStore) publishWrite(nextLEO uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.leo.Store(nextLEO)
	s.loaded.Store(true)
	s.writeInProgress.Store(false)
	s.mu.Unlock()
}

func (s *ChannelStore) failPendingWrite() {
	if s == nil {
		return
	}
	s.writeInProgress.Store(false)
}

func (s *ChannelStore) commitCoordinator() *commitCoordinator {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.commitCoordinator()
}

func (s *ChannelStore) checkpointCoordinator() *commitCoordinator {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.checkpointCoordinator()
}

func (s *ChannelStore) messageTable() *messageTable {
	if s == nil {
		return nil
	}
	return &s.messages
}

// GetMessageBySeq loads one persisted message by its durable message sequence.
func (s *ChannelStore) GetMessageBySeq(seq uint64) (channel.Message, bool, error) {
	if err := s.validate(); err != nil {
		return channel.Message{}, false, err
	}
	row, ok, err := s.messageTable().getBySeq(seq)
	if err != nil || !ok {
		return channel.Message{}, ok, err
	}
	return row.toChannelMessage(), true, nil
}

// GetMessageByMessageID loads one persisted message through the unique message_id index.
func (s *ChannelStore) GetMessageByMessageID(messageID uint64) (channel.Message, bool, error) {
	if err := s.validate(); err != nil {
		return channel.Message{}, false, err
	}
	row, ok, err := s.messageTable().getByMessageID(messageID)
	if err != nil || !ok {
		return channel.Message{}, ok, err
	}
	return row.toChannelMessage(), true, nil
}

// ListMessagesBySeq scans persisted messages by message sequence in forward or reverse order.
func (s *ChannelStore) ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	var (
		rows []messageRow
		err  error
	)
	if reverse {
		if fromSeq == 0 {
			fromSeq = math.MaxUint64
		}
		rows, err = s.messageTable().scanBySeqReverse(fromSeq, limit, maxBytes)
	} else {
		rows, err = s.messageTable().scanBySeq(fromSeq, limit, maxBytes)
	}
	if err != nil {
		return nil, err
	}
	messages := make([]channel.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, row.toChannelMessage())
	}
	return messages, nil
}

// ListMessagesByClientMsgNo scans one client_msg_no page in descending message sequence order.
func (s *ChannelStore) ListMessagesByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]channel.Message, uint64, bool, error) {
	if err := s.validate(); err != nil {
		return nil, 0, false, err
	}
	rows, nextBeforeSeq, hasMore, err := s.messageTable().scanByClientMsgNo(clientMsgNo, beforeSeq, limit)
	if err != nil {
		return nil, 0, false, err
	}
	messages := make([]channel.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, row.toChannelMessage())
	}
	return messages, nextBeforeSeq, hasMore, nil
}

// LookupIdempotency loads the idempotency hit and payload hash from the structured unique index.
func (s *ChannelStore) LookupIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error) {
	if err := s.validateIdempotencyKey(key); err != nil {
		return channel.IdempotencyEntry{}, 0, false, err
	}
	hit, ok, err := s.messageTable().lookupIdempotency(key.FromUID, key.ClientMsgNo)
	if err != nil || !ok {
		return channel.IdempotencyEntry{}, 0, ok, err
	}
	return channel.IdempotencyEntry{
		MessageID:  hit.MessageID,
		MessageSeq: hit.MessageSeq,
		Offset:     hit.MessageSeq - 1,
	}, hit.PayloadHash, true, nil
}
