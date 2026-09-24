package mqtt

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

var (
	ErrOutboundInvalid = errors.New("mqtt: invalid outbound exchange or order")
	ErrOutboundBusy    = errors.New("mqtt: outbound connection capacity unavailable")
	// ErrOutboundExpired means a new QoS 0 candidate was not enqueued.
	ErrOutboundExpired = errors.New("mqtt: outbound publication expired before enqueue")
)

// OutboundAcknowledgements owns current authority checks and durable completion.
// Implementations must bound the call and retain owner execution through commit.
type OutboundAcknowledgements interface {
	Acknowledge(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error)
}

// OutboundDelivery is trusted caller input after durable window admission,
// original content verification and current receive permission. Matching hashes
// here establish association only; entry cannot independently prove commitment.
// Resume supplies old exchanges in original order with Redelivery true.
type OutboundDelivery struct {
	Owner       contract.Owner
	Exchange    meta.MQTTInflight
	Publication ch.MQTTReplayPublication
	Redelivery  bool
}

// SendQoS1 binds an exchange before enqueue and retains exact owner execution
// through the gateway writer. The nonblocking send gate adds no worker or queue.
// Success means enqueue only, never transport flush, client receipt or PUBACK.
func (h *Handler) SendQoS1(g gt.Context, d OutboundDelivery) (err error) {
	s := h.state(g)
	defer func() {
		if recover() != nil {
			h.terminate(g, s, 0)
			err = ErrHandlerCallback
		}
	}()
	if s == nil || g.RequestContext == nil || h.options.Acknowledgements == nil || d.Owner != s.connection.Owner || (d.Redelivery && !s.connection.SessionPresent) {
		return ErrOutboundInvalid
	}
	s.mu.Lock()
	if !s.opened || s.closing {
		s.mu.Unlock()
		return ErrHandlerClosed
	}
	if d.Exchange.DeliveryOrder == 0 || d.Exchange.DeliveryOrder <= s.lastDeliveryOrder {
		s.mu.Unlock()
		return ErrOutboundInvalid
	}
	limit := min(s.receiveMaximum, meta.MQTTMaxInflight)
	if s.sending || len(s.sent) >= int(limit) {
		s.mu.Unlock()
		return ErrOutboundBusy
	}
	if _, exists := s.sent[d.Exchange.PacketID]; exists {
		s.mu.Unlock()
		return ErrOutboundInvalid
	}
	s.sending = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.sending = false; s.mu.Unlock() }()
	op, err := h.options.Owners.Begin(g.RequestContext, d.Owner)
	if err != nil {
		return ErrHandlerClosed
	}
	defer op.Done()
	if op.UID() != s.connection.UID {
		return ErrOutboundInvalid
	}
	p, err := mapOutbound(d, s.connection.UID, h.options.Now().UnixMilli())
	if err != nil {
		return err
	}
	if op.Check() != nil || g.RequestContext.Err() != nil {
		return ErrHandlerClosed
	}
	q := sessioncase.AcknowledgementCommand{Owner: d.Owner, Key: d.Exchange.Key, PacketID: d.Exchange.PacketID, DeliveryOrder: d.Exchange.DeliveryOrder}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return ErrHandlerClosed
	}
	if s.sent == nil {
		s.sent = make(map[uint16]sessioncase.AcknowledgementCommand)
	}
	s.sent[q.PacketID] = q
	s.lastDeliveryOrder = q.DeliveryOrder
	s.mu.Unlock()
	// Neither gateway nor business callbacks may run under the connection lock.
	if op.Check() != nil || g.RequestContext.Err() != nil || g.WritePacket(p) != nil {
		return h.terminate(g, s, 0)
	}
	if op.Check() != nil || g.RequestContext.Err() != nil {
		return h.terminate(g, s, 0)
	}
	return nil
}

// acknowledge captures an immutable binding before entering business work. The
// usecase owns the execution scope; nesting one here would consume capacity twice.
func (h *Handler) acknowledge(g gt.Context, s *connectionState, p *wire.Puback) error {
	if h.options.Acknowledgements == nil {
		return h.terminate(g, s, 0x83)
	}
	if _, err := wire.Encode(p, wire.Limits{}); err != nil {
		return h.terminate(g, s, wire.ProtocolError)
	}
	s.mu.Lock()
	q, found := s.sent[p.PacketID]
	s.mu.Unlock()
	if !found {
		op, err := h.options.Owners.Begin(g.RequestContext, s.connection.Owner)
		if err != nil {
			return h.terminate(g, s, 0)
		}
		defer op.Done()
		if op.Check() != nil || g.RequestContext.Err() != nil {
			return h.terminate(g, s, 0)
		}
		return nil // No exchange means no durable write and no additional send credit.
	}
	result, err := h.options.Acknowledgements.Acknowledge(g.RequestContext, q)
	if err != nil || (result.Changed && result.Absent) || g.RequestContext.Err() != nil {
		return h.terminate(g, s, 0)
	}
	s.mu.Lock()
	if !s.closing && s.sent[p.PacketID] == q {
		delete(s.sent, p.PacketID)
	}
	closed := s.closing
	s.mu.Unlock()
	if closed {
		return ErrHandlerClosed
	}
	return nil
}

// mapOutbound translates original content only, preserving ordered duplicates
// and reducing expiry from its original basis. Begun exchanges are never expired
// out of this path; their retransmissions carry a remaining interval of zero.
func mapOutbound(d OutboundDelivery, uid string, nowMS int64) (*wire.Publish, error) {
	e, r := d.Exchange, d.Publication
	m, k := r.Message, e.Key
	sourceID := strconv.FormatUint(uint64(m.ChannelType), 10) + ":" + m.ChannelID
	if meta.ValidateMQTTInflight(e) != nil || k.Namespace != d.Owner.Key.Namespace || k.ClientID != d.Owner.Key.ClientID || k.SessionGeneration != d.Owner.SessionGeneration ||
		k.SourceID != sourceID || r.Internal || r.ContentVersion != 1 || r.ContentVersion != e.Publication.ContentVersion || r.ContentHash == [32]byte{} || hex.EncodeToString(r.ContentHash[:]) != e.Publication.ContentHash ||
		m.MessageID != e.Publication.MessageID || m.MessageSeq != e.Publication.MessageSeq || m.MessageSeq != e.Publication.Position || m.Version != 0 || m.UpdatedAtMS != 0 || m.TraceID != "" || m.ChannelKey != "" ||
		r.AccountedBytes != uint64(len(m.Payload))+uint64(len(m.PublicationMetadata)) || r.AccountedBytes != e.Publication.Bytes || len(m.Payload) > wire.DefaultMaxPacketBytes || nowMS < e.UpdatedAtMS {
		return nil, ErrOutboundInvalid
	}
	p, err := mapOutboundPublication(r, e.Topic, e.Publication.SubscriptionIdentifier, uid, 1, nowMS)
	if err != nil {
		return nil, err
	}
	p.PacketID, p.Dup = e.PacketID, d.Redelivery
	return p, nil
}

// mapOutboundPublication preserves original metadata for either delivery QoS. Effective
// expiry is the earlier native/publication deadline; begun QoS 1 is never dropped.
func mapOutboundPublication(r ch.MQTTReplayPublication, topic string, subscriptionID uint32, uid string, qos byte, nowMS int64) (*wire.Publish, error) {
	m := r.Message
	if r.Internal || r.ContentVersion != 1 || r.ContentHash == [32]byte{} || m.MessageID == 0 || m.MessageSeq == 0 || m.Version != 0 || m.UpdatedAtMS != 0 || m.TraceID != "" || m.ChannelKey != "" ||
		r.AccountedBytes != uint64(len(m.Payload))+uint64(len(m.PublicationMetadata)) || len(m.Payload) > wire.DefaultMaxPacketBytes || subscriptionID > 268435455 || nowMS <= 0 || m.ServerTimestampMS > nowMS {
		return nil, ErrOutboundInvalid
	}
	target, err := ParseTopic(topic)
	if err != nil || target.ChannelType != m.ChannelType || (target.ChannelType == 2 && target.ChannelID != m.ChannelID) || (target.ChannelType == 1 && target.ChannelID != uid) {
		return nil, ErrOutboundInvalid
	}
	var deadline int64
	expires := false
	if m.Expire != 0 {
		duration := int64(m.Expire) * 1000
		if m.ServerTimestampMS <= 0 || m.ServerTimestampMS > math.MaxInt64-duration {
			return nil, ErrOutboundInvalid
		}
		deadline, expires = m.ServerTimestampMS+duration, true
	}
	var metadata publication.Metadata
	if len(m.PublicationMetadata) > 0 {
		metadata, err = publication.Decode(m.PublicationMetadata)
		if err != nil || qos > metadata.QoS {
			return nil, ErrOutboundInvalid
		}
		mdDeadline, mdExpires, e := metadata.ExpiryDeadlineMS(m.ServerTimestampMS)
		if e != nil {
			return nil, ErrOutboundInvalid
		}
		basis := metadata.AcceptedAtMS
		if metadata.Source == publication.SourceWill {
			basis = m.ServerTimestampMS
		}
		if nowMS < basis {
			return nil, ErrOutboundInvalid
		}
		if mdExpires && (!expires || mdDeadline < deadline) {
			deadline, expires = mdDeadline, true
		}
	}
	if qos == 0 && expires && nowMS >= deadline {
		return nil, ErrOutboundExpired
	}
	remaining := uint32(0)
	if expires && deadline > nowMS {
		remaining = uint32((deadline - nowMS + 999) / 1000)
	}
	p := &wire.Publish{Topic: topic, QoS: qos, Payload: bytes.Clone(m.Payload)}
	hasExpiryProperty := false
	for _, v := range metadata.Properties {
		prop := wire.Property{Number: v.Number, Text: v.Text, Value: v.Value, Data: v.Binary}
		switch v.Kind {
		case publication.PayloadFormat:
			prop.ID = wire.PayloadFormatIndicator
		case publication.MessageExpiry:
			prop.ID = wire.MessageExpiryInterval
			prop.Number = remaining
			hasExpiryProperty = true
		case publication.ContentType:
			prop.ID = wire.ContentType
		case publication.ResponseTopic:
			prop.ID = wire.ResponseTopic
		case publication.CorrelationData:
			prop.ID = wire.CorrelationData
		case publication.UserProperty:
			if strings.HasPrefix(v.Text, "wk.") {
				return nil, ErrOutboundInvalid
			}
			prop.ID = wire.UserProperty
		default:
			return nil, ErrOutboundInvalid
		}
		p.Properties = append(p.Properties, prop)
	}
	if expires && !hasExpiryProperty {
		p.Properties = append(p.Properties, wire.Property{ID: wire.MessageExpiryInterval, Number: remaining})
	}
	for _, pair := range [][2]string{{"wk.message_id", strconv.FormatUint(m.MessageID, 10)}, {"wk.message_seq", strconv.FormatUint(m.MessageSeq, 10)}, {"wk.from_uid", m.FromUID}, {"wk.channel_id", m.ChannelID}, {"wk.channel_type", strconv.FormatUint(uint64(m.ChannelType), 10)}, {clientMessageProperty, m.ClientMsgNo}} {
		p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: pair[0], Value: pair[1]})
	}
	if subscriptionID > 0 {
		p.Properties = append(p.Properties, wire.Property{ID: wire.SubscriptionIdentifier, Number: subscriptionID})
	}
	return p, nil
}
