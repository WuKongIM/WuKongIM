package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	conversationFactsOpLatest = "latest"
	conversationFactsOpRecent = "recent"
)

type conversationFactsChannelKey struct {
	ID   string
	Type uint8
}

type conversationFactsRequest struct {
	Op       string                        `json:"op"`
	Key      conversationFactsChannelKey   `json:"key"`
	Keys     []conversationFactsChannelKey `json:"keys,omitempty"`
	Limit    int                           `json:"limit,omitempty"`
	MaxBytes int                           `json:"max_bytes,omitempty"`
}

type conversationFactsEntry struct {
	Key      conversationFactsChannelKey `json:"key"`
	Messages []channel.Message           `json:"messages,omitempty"`
}

type conversationFactsResponse struct {
	Status   string                   `json:"status"`
	Messages []channel.Message        `json:"messages,omitempty"`
	Entries  []conversationFactsEntry `json:"entries,omitempty"`
}

func newConversationFactsChannelKey(id channel.ChannelID) conversationFactsChannelKey {
	return conversationFactsChannelKey{ID: id.ID, Type: id.Type}
}

func (k conversationFactsChannelKey) channelID() channel.ChannelID {
	return channel.ChannelID{ID: k.ID, Type: k.Type}
}

func (k conversationFactsChannelKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ChannelID   string `json:"ChannelID"`
		ChannelType uint8  `json:"ChannelType"`
	}{
		ChannelID:   k.ID,
		ChannelType: k.Type,
	})
}

func (k *conversationFactsChannelKey) UnmarshalJSON(body []byte) error {
	var payload struct {
		ChannelID   *string `json:"ChannelID"`
		ChannelType *uint8  `json:"ChannelType"`
		ID          *string `json:"ID"`
		Type        *uint8  `json:"Type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	switch {
	case payload.ChannelID != nil || payload.ChannelType != nil:
		if payload.ChannelID != nil {
			k.ID = *payload.ChannelID
		}
		if payload.ChannelType != nil {
			k.Type = *payload.ChannelType
		}
	default:
		if payload.ID != nil {
			k.ID = *payload.ID
		}
		if payload.Type != nil {
			k.Type = *payload.Type
		}
	}
	return nil
}

func (a *Adapter) handleConversationFactsRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req conversationFactsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	var (
		messages []channel.Message
		entries  []conversationFactsEntry
		err      error
	)
	if len(req.Keys) > 0 {
		entries = make([]conversationFactsEntry, 0, len(req.Keys))
		for _, rawKey := range req.Keys {
			key := rawKey.channelID()
			entry := conversationFactsEntry{Key: newConversationFactsChannelKey(key)}
			switch req.Op {
			case conversationFactsOpLatest:
				var msg channel.Message
				var ok bool
				msg, ok, err = a.loadLatestConversationFact(ctx, key, req.MaxBytes)
				if ok {
					entry.Messages = []channel.Message{msg}
				}
			case conversationFactsOpRecent:
				entry.Messages, err = a.loadRecentConversationFacts(ctx, key, req.Limit, req.MaxBytes)
			default:
				return nil, fmt.Errorf("access/node: unknown conversation facts op %q", req.Op)
			}
			if errors.Is(err, channel.ErrChannelNotFound) {
				err = nil
			}
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		return encodeConversationFactsResponse(conversationFactsResponse{
			Status:  rpcStatusOK,
			Entries: entries,
		})
	}

	key := req.Key.channelID()
	switch req.Op {
	case conversationFactsOpLatest:
		var msg channel.Message
		var ok bool
		msg, ok, err = a.loadLatestConversationFact(ctx, key, req.MaxBytes)
		if ok {
			messages = []channel.Message{msg}
		}
	case conversationFactsOpRecent:
		messages, err = a.loadRecentConversationFacts(ctx, key, req.Limit, req.MaxBytes)
	default:
		return nil, fmt.Errorf("access/node: unknown conversation facts op %q", req.Op)
	}
	if errors.Is(err, channel.ErrChannelNotFound) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return encodeConversationFactsResponse(conversationFactsResponse{
		Status:   rpcStatusOK,
		Messages: messages,
	})
}

func (a *Adapter) loadLatestConversationFact(ctx context.Context, id channel.ChannelID, maxBytes int) (channel.Message, bool, error) {
	msg, ok, err := loadLatestConversationMessage(ctx, a.channelLog, id, maxBytes)
	if !errors.Is(err, channel.ErrStaleMeta) {
		return msg, ok, err
	}
	if _, refreshErr := a.refreshConversationFactsMeta(ctx, id); refreshErr != nil {
		return channel.Message{}, false, refreshErr
	}
	return loadLatestConversationMessage(ctx, a.channelLog, id, maxBytes)
}

func (a *Adapter) loadRecentConversationFacts(ctx context.Context, id channel.ChannelID, limit, maxBytes int) ([]channel.Message, error) {
	messages, err := loadRecentConversationMessages(ctx, a.channelLog, id, limit, maxBytes)
	if !errors.Is(err, channel.ErrStaleMeta) {
		return messages, err
	}
	if _, refreshErr := a.refreshConversationFactsMeta(ctx, id); refreshErr != nil {
		return nil, refreshErr
	}
	return loadRecentConversationMessages(ctx, a.channelLog, id, limit, maxBytes)
}

func (a *Adapter) refreshConversationFactsMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	if a == nil || a.channelMeta == nil {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	return a.channelMeta.RefreshChannelMeta(ctx, id)
}

func loadLatestConversationMessage(ctx context.Context, cluster ChannelLog, id channel.ChannelID, maxBytes int) (channel.Message, bool, error) {
	if cluster == nil {
		return channel.Message{}, false, channel.ErrStaleMeta
	}
	status, err := cluster.Status(id)
	if errors.Is(err, channel.ErrNotReady) {
		return channel.Message{}, false, nil
	}
	if err != nil {
		return channel.Message{}, false, err
	}
	if status.CommittedSeq == 0 {
		return channel.Message{}, false, nil
	}

	fetch, err := cluster.Fetch(ctx, channel.FetchRequest{
		ChannelID: id,
		FromSeq:   status.CommittedSeq,
		Limit:     1,
		MaxBytes:  maxBytes,
	})
	if errors.Is(err, channel.ErrNotReady) {
		return channel.Message{}, false, nil
	}
	if err != nil {
		return channel.Message{}, false, err
	}
	if len(fetch.Messages) == 0 {
		return channel.Message{}, false, nil
	}
	return fetch.Messages[0], true, nil
}

func loadRecentConversationMessages(ctx context.Context, cluster ChannelLog, id channel.ChannelID, limit, maxBytes int) ([]channel.Message, error) {
	if cluster == nil || limit <= 0 {
		return nil, nil
	}
	status, err := cluster.Status(id)
	if errors.Is(err, channel.ErrNotReady) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status.CommittedSeq == 0 {
		return nil, nil
	}

	fromSeq := uint64(1)
	if status.CommittedSeq >= uint64(limit) {
		fromSeq = status.CommittedSeq - uint64(limit) + 1
	}
	fetch, err := cluster.Fetch(ctx, channel.FetchRequest{
		ChannelID: id,
		FromSeq:   fromSeq,
		Limit:     limit,
		MaxBytes:  maxBytes,
	})
	if errors.Is(err, channel.ErrNotReady) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]channel.Message(nil), fetch.Messages...), nil
}

func encodeConversationFactsResponse(resp conversationFactsResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeConversationFactsResponse(body []byte) (conversationFactsResponse, error) {
	var resp conversationFactsResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}
