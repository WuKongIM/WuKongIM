package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConversationNode exposes clusterv2 reads needed by conversation lists.
type ConversationNode interface {
	ListUserConversationActivePage(context.Context, string, metadb.UserConversationActiveCursor, int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	ReadChannelLastVisible(context.Context, channelv2.ChannelID, uint64) (channelv2.Message, bool, error)
}

// ConversationStore adapts clusterv2 reads to the conversation usecase ports.
type ConversationStore struct {
	node                      ConversationNode
	maxLastMessageConcurrency int
}

var _ conversationusecase.Store = (*ConversationStore)(nil)
var _ conversationusecase.MessageStore = (*ConversationStore)(nil)

// ConversationStoreOptions configures clusterv2-backed conversation reads.
type ConversationStoreOptions struct {
	// MaxLastMessageConcurrency bounds concurrent channel tail reads for one list page.
	MaxLastMessageConcurrency int
}

// NewConversationStore creates a clusterv2-backed conversation store.
func NewConversationStore(node ConversationNode, options ...ConversationStoreOptions) *ConversationStore {
	opts := ConversationStoreOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	return &ConversationStore{node: node, maxLastMessageConcurrency: opts.MaxLastMessageConcurrency}
}

// ListUserConversationActivePage reads UID-owned active conversation rows.
func (s *ConversationStore) ListUserConversationActivePage(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	if s == nil || s.node == nil {
		return nil, metadb.UserConversationActiveCursor{}, true, metadb.ErrNotFound
	}
	rows, cursor, done, err := s.node.ListUserConversationActivePage(ctx, uid, after, limit)
	if err != nil {
		return nil, metadb.UserConversationActiveCursor{}, false, err
	}
	return append([]metadb.UserConversationState(nil), rows...), cursor, done, nil
}

// GetLastVisibleMessages reads each returned row's newest visible channel message.
func (s *ConversationStore) GetLastVisibleMessages(ctx context.Context, requests []conversationusecase.LastVisibleMessageRequest) (map[metadb.ConversationKey]conversationusecase.LastMessage, error) {
	out := make(map[metadb.ConversationKey]conversationusecase.LastMessage, len(requests))
	if len(requests) == 0 {
		return out, nil
	}
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if s.maxLastMessageConcurrency > 1 && len(requests) > 1 {
		return s.getLastVisibleMessagesConcurrent(ctx, requests)
	}
	for _, req := range requests {
		key, msg, ok, err := s.readLastVisibleMessage(ctx, req)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out[key] = msg
	}
	return out, nil
}

func (s *ConversationStore) getLastVisibleMessagesConcurrent(ctx context.Context, requests []conversationusecase.LastVisibleMessageRequest) (map[metadb.ConversationKey]conversationusecase.LastMessage, error) {
	workers := s.maxLastMessageConcurrency
	if workers > len(requests) {
		workers = len(requests)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan conversationusecase.LastVisibleMessageRequest)
	out := make(map[metadb.ConversationKey]conversationusecase.LastMessage, len(requests))
	var outMu sync.Mutex
	var firstErr error
	var firstErrOnce sync.Once
	setErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for req := range jobs {
				key, msg, ok, err := s.readLastVisibleMessage(workCtx, req)
				if err != nil {
					setErr(err)
					continue
				}
				if ok {
					outMu.Lock()
					out[key] = msg
					outMu.Unlock()
				}
			}
		}()
	}
send:
	for _, req := range requests {
		select {
		case <-workCtx.Done():
			break send
		case jobs <- req:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ConversationStore) readLastVisibleMessage(ctx context.Context, req conversationusecase.LastVisibleMessageRequest) (metadb.ConversationKey, conversationusecase.LastMessage, bool, error) {
	if req.ChannelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 {
		return metadb.ConversationKey{}, conversationusecase.LastMessage{}, false, fmt.Errorf("internalv2/infra/cluster: invalid conversation message request")
	}
	key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
	msg, ok, err := s.node.ReadChannelLastVisible(ctx, channelv2.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, req.VisibleAfterSeq)
	if err != nil {
		if isMissingLastMessage(err) {
			return key, conversationusecase.LastMessage{}, false, nil
		}
		return metadb.ConversationKey{}, conversationusecase.LastMessage{}, false, err
	}
	if !ok {
		return key, conversationusecase.LastMessage{}, false, nil
	}
	return key, lastMessageFromChannel(msg), true, nil
}

func lastMessageFromChannel(msg channelv2.Message) conversationusecase.LastMessage {
	return conversationusecase.LastMessage{
		MessageID:         msg.MessageID,
		MessageSeq:        msg.MessageSeq,
		FromUID:           msg.FromUID,
		ClientMsgNo:       msg.ClientMsgNo,
		ServerTimestampMS: msg.ServerTimestampMS,
		Payload:           append([]byte(nil), msg.Payload...),
	}
}

func isMissingLastMessage(err error) bool {
	return errors.Is(err, metadb.ErrNotFound) || appendErrorMatches(err, channelv2.ErrChannelNotFound)
}
