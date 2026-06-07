package cluster

import (
	"context"
	"errors"
	"fmt"

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
	node ConversationNode
}

var _ conversationusecase.Store = (*ConversationStore)(nil)
var _ conversationusecase.MessageStore = (*ConversationStore)(nil)

// NewConversationStore creates a clusterv2-backed conversation store.
func NewConversationStore(node ConversationNode) *ConversationStore {
	return &ConversationStore{node: node}
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
	for _, req := range requests {
		if req.ChannelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 {
			return nil, fmt.Errorf("internalv2/infra/cluster: invalid conversation message request")
		}
		key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		msg, ok, err := s.node.ReadChannelLastVisible(ctx, channelv2.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, req.VisibleAfterSeq)
		if err != nil {
			if isMissingLastMessage(err) {
				continue
			}
			return nil, err
		}
		if !ok {
			continue
		}
		out[key] = lastMessageFromChannel(msg)
	}
	return out, nil
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
