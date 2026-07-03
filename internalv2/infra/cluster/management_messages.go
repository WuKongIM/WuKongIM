package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ManagementMessageReader adapts cluster committed message reads to manager message pages.
type ManagementMessageReader struct {
	node ChannelMessageReadNode
}

// NewManagementMessageReader creates a manager message reader.
func NewManagementMessageReader(node ChannelMessageReadNode) *ManagementMessageReader {
	return &ManagementMessageReader{node: node}
}

// QueryMessages returns one descending committed message page for manager display.
func (r *ManagementMessageReader) QueryMessages(ctx context.Context, req managementusecase.MessageQueryRequest) (managementusecase.MessageQueryPage, error) {
	if r == nil || r.node == nil {
		return managementusecase.MessageQueryPage{}, nil
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, managementReadCommittedRequest(req))
	if err != nil {
		return managementusecase.MessageQueryPage{}, mapAppendError(err)
	}
	messages := filterManagementMessages(req, managementMessagesFromChannel(read.Messages))
	hasMore := len(messages) > req.Limit
	if hasMore {
		messages = messages[:req.Limit]
	}
	page := managementusecase.MessageQueryPage{
		Items:   messages,
		HasMore: hasMore,
	}
	if hasMore && len(messages) > 0 {
		page.NextBeforeSeq = messages[len(messages)-1].MessageSeq
	}
	return page, nil
}

// MaxMessageSeqForMeta returns the highest committed message sequence for one runtime metadata row.
func (r *ManagementMessageReader) MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error) {
	if r == nil || r.node == nil {
		return 0, nil
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}, channelstore.ReadCommittedRequest{
		FromSeq:  maxUint64(),
		MaxSeq:   maxUint64(),
		Limit:    1,
		MaxBytes: maxInt(),
		Reverse:  true,
	})
	if err != nil {
		return 0, mapAppendError(err)
	}
	if len(read.Messages) == 0 {
		return 0, nil
	}
	return read.Messages[0].MessageSeq, nil
}

func managementReadCommittedRequest(req managementusecase.MessageQueryRequest) channelstore.ReadCommittedRequest {
	fromSeq := req.BeforeSeq
	if fromSeq == 0 {
		fromSeq = maxUint64()
	} else {
		fromSeq--
	}
	return channelstore.ReadCommittedRequest{
		FromSeq:  fromSeq,
		MaxSeq:   maxUint64(),
		Limit:    req.Limit + 1,
		MaxBytes: maxInt(),
		Reverse:  true,
	}
}

func managementMessagesFromChannel(items []channelv2.Message) []managementusecase.Message {
	out := make([]managementusecase.Message, 0, len(items))
	for _, item := range items {
		out = append(out, managementusecase.Message{
			MessageID:   item.MessageID,
			MessageSeq:  item.MessageSeq,
			ClientMsgNo: item.ClientMsgNo,
			ChannelID:   item.ChannelID,
			ChannelType: int64(item.ChannelType),
			FromUID:     item.FromUID,
			Timestamp:   item.ServerTimestampMS / 1000,
			Payload:     append([]byte(nil), item.Payload...),
		})
	}
	return out
}

func filterManagementMessages(req managementusecase.MessageQueryRequest, items []managementusecase.Message) []managementusecase.Message {
	if req.MessageID == 0 && req.ClientMsgNo == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if req.MessageID != 0 && item.MessageID != req.MessageID {
			continue
		}
		if req.ClientMsgNo != "" && item.ClientMsgNo != req.ClientMsgNo {
			continue
		}
		out = append(out, item)
	}
	return out
}
