package cluster

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// MQTTWillReceiptNode routes retained evidence through current recovered Channel
// authority, independently rechecking fresh Slot placement before and after it.
type MQTTWillReceiptNode interface {
	ReadChannelWillReceipt(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error)
}

// MQTTWillReceipts translates verified retained content into execution receipts.
// No missing/unavailable runtime is normalized into nonpublication evidence.
type MQTTWillReceipts struct {
	metadata channels.FreshChannelMetaSource
	node     MQTTWillReceiptNode
}

func NewMQTTWillReceipts(metadata channels.FreshChannelMetaSource, node MQTTWillReceiptNode) (*MQTTWillReceipts, error) {
	if metadata == nil || node == nil {
		return nil, mqttsession.ErrInvalid
	}
	return &MQTTWillReceipts{metadata: metadata, node: node}, nil
}

// LookupWillPublication uses the server domain and the same person normalization
// as SEND. The content hash covers original bytes, including the complete v2 value.
func (a *MQTTWillReceipts) LookupWillPublication(ctx context.Context, q mqttsession.WillPublication) (mqttsession.WillPublicationReceipt, bool, error) {
	var zero mqttsession.WillPublicationReceipt
	if a == nil || ctx == nil {
		return zero, false, mqttsession.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	md, err := publication.Decode(q.PublicationMetadata)
	if err != nil || md.Source != publication.SourceWill || !publication.ValidServerWillKey(md.ServerWillKey) || md.OriginalTopic != q.Target.Topic || q.Target.TargetType != 1 && q.Target.TargetType != 2 {
		return zero, false, mqttsession.ErrInvalid
	}
	hash, err := messagedb.WillPublicationHash(q.UID, q.ClientMsgNo, q.Payload, q.PublicationMetadata)
	if err != nil {
		return zero, false, mqttsession.ErrInvalid
	}
	id := ch.ChannelID{ID: q.Target.TargetID, Type: q.Target.TargetType}
	if id.Type == 1 {
		id.ID, err = channelid.NormalizePersonChannel(q.UID, id.ID)
		if err != nil {
			return zero, false, mqttsession.ErrInvalid
		}
	}
	m, err := a.metadata.ResolveChannelMetaFresh(ctx, id)
	if errors.Is(err, ch.ErrChannelNotFound) {
		// Fresh Slot target absence remains an error. Only an independent exact
		// sealed dispatch attempt may let the usecase resume before first SEND.
		return zero, false, mqttsession.ErrWillReceiptTargetMissing
	}
	if err != nil {
		return zero, false, err
	}
	request := ch.WillReceiptRequest{ChannelID: id, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, FromUID: q.UID, ServerWillKey: md.ServerWillKey}
	if m.ID != id || m.Leader == 0 || !request.Valid() {
		return zero, false, mqttsession.ErrEvidence
	}
	r, err := a.node.ReadChannelWillReceipt(ctx, request)
	if err != nil {
		return zero, false, err
	}
	if !r.Valid() {
		return zero, false, mqttsession.ErrEvidence
	}
	if !r.Found {
		return zero, false, nil
	}
	if r.Receipt.ContentHash != hash {
		return zero, false, mqttsession.ErrEvidence
	}
	return mqttsession.WillPublicationReceipt{MessageID: r.Receipt.MessageID, MessageSeq: r.Receipt.MessageSeq, PublishedAtMS: r.Receipt.ServerTimestampMS}, true, nil
}
