package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// ChannelMessageReadNode is the cluster committed message read surface used by internalv2.
type ChannelMessageReadNode interface {
	ReadChannelCommitted(context.Context, channelv2.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
}

// ChannelMessageReader adapts cluster committed reads to the message usecase sync port.
type ChannelMessageReader struct {
	node ChannelMessageReadNode
}

// NewChannelMessageReader creates a ChannelMessageReader.
func NewChannelMessageReader(node ChannelMessageReadNode) *ChannelMessageReader {
	return &ChannelMessageReader{node: node}
}

// SyncMessages returns one compatible channel message page.
func (r *ChannelMessageReader) SyncMessages(ctx context.Context, query message.ChannelMessageQuery) (message.ChannelMessagePage, error) {
	if r == nil || r.node == nil {
		return message.ChannelMessagePage{}, message.ErrMessageReaderRequired
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: query.ChannelID.ID, Type: query.ChannelID.Type}, readCommittedRequest(query, limit))
	if err != nil {
		return message.ChannelMessagePage{}, mapAppendError(err)
	}
	messages := syncedMessagesFromChannel(read.Messages)
	messages = filterSyncedMessages(query, messages)
	reverse := query.PullMode == message.PullModeDown || (query.StartSeq == 0 && query.EndSeq == 0)
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	if reverse {
		reverseSyncedMessages(messages)
	}
	return message.ChannelMessagePage{Messages: messages, HasMore: hasMore}, nil
}

func readCommittedRequest(query message.ChannelMessageQuery, limit int) channelstore.ReadCommittedRequest {
	req := channelstore.ReadCommittedRequest{
		FromSeq:  query.StartSeq,
		MaxSeq:   queryMaxSeq(query),
		Limit:    limit + 1,
		MaxBytes: maxInt(),
	}
	if query.PullMode == message.PullModeDown || (query.StartSeq == 0 && query.EndSeq == 0) {
		req.Reverse = true
		if req.FromSeq == 0 {
			req.FromSeq = maxUint64()
			req.MaxSeq = maxUint64()
		}
	}
	if req.FromSeq == 0 && !req.Reverse {
		req.FromSeq = 1
	}
	return req
}

func queryMaxSeq(query message.ChannelMessageQuery) uint64 {
	if query.PullMode == message.PullModeUp && query.EndSeq > 0 {
		return query.EndSeq - 1
	}
	if query.StartSeq > 0 {
		return query.StartSeq
	}
	return maxUint64()
}

func syncedMessagesFromChannel(in []channelv2.Message) []message.SyncedMessage {
	out := make([]message.SyncedMessage, 0, len(in))
	for _, msg := range in {
		out = append(out, message.SyncedMessage{
			MessageID:   msg.MessageID,
			MessageSeq:  msg.MessageSeq,
			ChannelID:   msg.ChannelID,
			ChannelType: msg.ChannelType,
			FromUID:     msg.FromUID,
			ClientMsgNo: msg.ClientMsgNo,
			Payload:     append([]byte(nil), msg.Payload...),
		})
	}
	return out
}

func filterSyncedMessages(query message.ChannelMessageQuery, messages []message.SyncedMessage) []message.SyncedMessage {
	if query.PullMode == message.PullModeDown && query.EndSeq > 0 {
		kept := messages[:0]
		for _, msg := range messages {
			if msg.MessageSeq <= query.EndSeq {
				continue
			}
			kept = append(kept, msg)
		}
		return kept
	}
	if query.PullMode == message.PullModeUp && query.EndSeq > 0 {
		kept := messages[:0]
		for _, msg := range messages {
			if msg.MessageSeq >= query.EndSeq {
				continue
			}
			kept = append(kept, msg)
		}
		return kept
	}
	return messages
}

func reverseSyncedMessages(messages []message.SyncedMessage) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func maxUint64() uint64 {
	return ^uint64(0)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
