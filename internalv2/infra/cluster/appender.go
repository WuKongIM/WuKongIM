package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// ChannelAppendNode is the clusterv2 append surface used by internalv2.
type ChannelAppendNode interface {
	AppendChannelBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
}

// ChannelAppender adapts clusterv2 channel append to the message usecase port.
type ChannelAppender struct {
	node ChannelAppendNode
}

// NewChannelAppender creates a ChannelAppender.
func NewChannelAppender(node ChannelAppendNode) *ChannelAppender {
	return &ChannelAppender{node: node}
}

// AppendBatch appends a message batch through clusterv2.
func (a *ChannelAppender) AppendBatch(ctx context.Context, req message.AppendBatchRequest) (message.AppendBatchResult, error) {
	if a == nil || a.node == nil {
		return message.AppendBatchResult{}, message.ErrAppenderRequired
	}
	res, err := a.node.AppendChannelBatch(ctx, channelv2.AppendBatchRequest{
		ChannelID:  channelv2.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type},
		Messages:   toChannelMessages(req.Messages),
		CommitMode: toChannelCommitMode(req.CommitMode),
	})
	if err != nil {
		return message.AppendBatchResult{}, mapAppendError(err)
	}
	return fromChannelAppendResult(res), nil
}

func toChannelMessages(in []message.Message) []channelv2.Message {
	out := make([]channelv2.Message, 0, len(in))
	for _, msg := range in {
		out = append(out, channelv2.Message{
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

func toChannelCommitMode(mode message.CommitMode) channelv2.CommitMode {
	if mode == message.CommitModeLocal {
		return channelv2.CommitModeLocal
	}
	return channelv2.CommitModeQuorum
}

func fromChannelAppendResult(res channelv2.AppendBatchResult) message.AppendBatchResult {
	items := make([]message.AppendBatchItemResult, 0, len(res.Items))
	for _, item := range res.Items {
		items = append(items, message.AppendBatchItemResult{
			MessageID:  item.MessageID,
			MessageSeq: item.MessageSeq,
			Message:    fromChannelMessage(item.Message),
			Err:        mapAppendError(item.Err),
		})
	}
	return message.AppendBatchResult{Items: items}
}

func fromChannelMessage(msg channelv2.Message) message.Message {
	return message.Message{
		MessageID:   msg.MessageID,
		MessageSeq:  msg.MessageSeq,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		FromUID:     msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
		Payload:     append([]byte(nil), msg.Payload...),
	}
}
