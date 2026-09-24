// Package mqtt adapts the independent MQTT 5 codec to the reusable gateway.
package mqtt

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

const Name = "mqtt"

// SessionMaximumPacketSize carries the peer's negotiated nonzero uint32 limit.
// It limits every server packet, including the authentication reply.
const SessionMaximumPacketSize = "gateway.mqtt.maximum_packet_size"

const disconnectObservationKey = "gateway.mqtt.disconnect_observation"

// DisconnectObservation preserves one fully decoded peer intent across TCP EOF
// racing the ordered packet mailbox. It retains no payload, strings or properties;
// entry policy must still validate direction and the negotiated Session expiry.
type DisconnectObservation struct {
	Reason                               byte
	SessionExpirySec                     uint32
	HasSessionExpiry, HasServerReference bool
	ObservedAt                           time.Time
}

// ReceivedDisconnect returns a value copy of the first complete DISCONNECT.
// Decode owns its monotonic observation; clients cannot supply this timestamp.
func ReceivedDisconnect(s session.Session) (DisconnectObservation, bool) {
	if s == nil {
		return DisconnectObservation{}, false
	}
	o, ok := s.Value(disconnectObservationKey).(DisconnectObservation)
	return o, ok
}

// Adapter owns immutable codec limits; client/session policy stays outside it.
type Adapter struct{ limits, outboundLimits wire.Limits }

// New applies the same codec limits in both directions.
func New(limits wire.Limits) *Adapter { return NewWithOutboundLimits(limits, limits) }

// NewWithOutboundLimits reserves independent bounded server output without
// weakening inbound admission. The peer packet limit still caps every write.
func NewWithOutboundLimits(inbound, outbound wire.Limits) *Adapter {
	return &Adapter{limits: inbound, outboundLimits: outbound}
}
func (*Adapter) Name() string                  { return Name }
func (*Adapter) OnOpen(session.Session) error  { return nil }
func (*Adapter) OnClose(session.Session) error { return nil }

func (a *Adapter) DecodePackets(sess session.Session, in []byte) ([]protocol.InboundPacket, int, error) {
	var out []protocol.InboundPacket
	consumed := 0
	// A coalesced network read must not allocate one object per tiny packet.
	for consumed < len(in) && len(out) < 128 {
		p, n, err := wire.Decode(in[consumed:], a.limits)
		if err != nil {
			return nil, 0, err
		}
		if n == 0 {
			break
		}
		packet := protocol.InboundPacket{Value: p, Bytes: n, Connect: p.Type() == wire.CONNECT}
		if connect, ok := p.(*wire.Connect); ok {
			timeout := time.Duration(connect.KeepAlive) * 1500 * time.Millisecond
			packet.ReadIdleTimeout = &timeout
		}
		if disconnect, ok := p.(*wire.Disconnect); ok && sess != nil {
			if _, seen := ReceivedDisconnect(sess); !seen {
				o := DisconnectObservation{Reason: disconnect.Reason, ObservedAt: time.Now()}
				for _, property := range disconnect.Properties {
					switch property.ID {
					case wire.SessionExpiryInterval:
						o.HasSessionExpiry = true
						o.SessionExpirySec = property.Number
					case wire.ServerReference:
						o.HasServerReference = true
					}
				}
				sess.SetValue(disconnectObservationKey, o)
			}
		}
		out = append(out, packet)
		consumed += n
	}
	return out, consumed, nil
}
func (a *Adapter) EncodePacket(sess session.Session, value any, _ session.OutboundMeta) ([]byte, error) {
	p, ok := value.(wire.Packet)
	if !ok {
		return nil, errors.New("gateway/mqtt: unsupported outbound value")
	}
	limits := a.outboundLimits
	if limits.MaxPacketBytes <= 0 {
		limits.MaxPacketBytes = wire.DefaultMaxPacketBytes
	}
	if sess != nil {
		if peer, ok := sess.Value(SessionMaximumPacketSize).(uint32); ok && peer > 0 && uint64(peer) < uint64(limits.MaxPacketBytes) {
			limits.MaxPacketBytes = int(peer)
		}
	}
	return wire.Encode(p, limits)
}

var _ protocol.PacketAdapter = (*Adapter)(nil)

func (*Adapter) PacketName(value any) string {
	p, ok := value.(wire.Packet)
	if !ok {
		return "UNKNOWN"
	}
	switch p.Type() {
	case wire.CONNECT:
		return "CONNECT"
	case wire.CONNACK:
		return "CONNACK"
	case wire.PUBLISH:
		return "PUBLISH"
	case wire.PUBACK:
		return "PUBACK"
	case wire.SUBSCRIBE:
		return "SUBSCRIBE"
	case wire.SUBACK:
		return "SUBACK"
	case wire.UNSUBSCRIBE:
		return "UNSUBSCRIBE"
	case wire.UNSUBACK:
		return "UNSUBACK"
	case wire.PINGREQ:
		return "PINGREQ"
	case wire.PINGRESP:
		return "PINGRESP"
	case wire.DISCONNECT:
		return "DISCONNECT"
	default:
		return "UNKNOWN"
	}
}
