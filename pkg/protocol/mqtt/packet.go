// Package mqtt implements bounded MQTT 5 wire packets without IM or session policy.
package mqtt

import "fmt"

// Type is the four-bit MQTT control packet identifier.
type Type byte

const (
	CONNECT     Type = 1
	CONNACK     Type = 2
	PUBLISH     Type = 3
	PUBACK      Type = 4
	SUBSCRIBE   Type = 8
	SUBACK      Type = 9
	UNSUBSCRIBE Type = 10
	UNSUBACK    Type = 11
	PINGREQ     Type = 12
	PINGRESP    Type = 13
	DISCONNECT  Type = 14
)

// Packet deliberately does not inherit WK frame semantics.
type Packet interface{ Type() Type }

// Error carries a MQTT reason code and a fixed diagnostic; it never includes
// credentials, client properties or payload bytes from the malformed packet.
type Error struct {
	Reason byte
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("mqtt: %s (0x%02x)", e.Detail, e.Reason) }

const (
	MalformedPacket            byte = 0x81
	ProtocolError              byte = 0x82
	UnsupportedProtocolVersion byte = 0x84
	PacketTooLarge             byte = 0x95
)

func malformed(detail string) error { return &Error{Reason: MalformedPacket, Detail: detail} }

// Limits bounds wire allocation before a packet enters the dispatch queue.
// Zero values select safe defaults; MaxPacketBytes includes the fixed header.
type Limits struct {
	MaxPacketBytes int
	// MaxPropertyBytes and MaxProperties bound each property block, including Will.
	MaxPropertyBytes int
	MaxProperties    int
	MaxSubscriptions int
}

func (l Limits) propertyBytes() int {
	if l.MaxPropertyBytes <= 0 {
		return 32 << 10
	}
	return l.MaxPropertyBytes
}
func (l Limits) properties() int {
	if l.MaxProperties <= 0 {
		return 128
	}
	return l.MaxProperties
}

func (l Limits) packetBytes() int {
	if l.MaxPacketBytes <= 0 {
		return 1 << 20
	}
	return l.MaxPacketBytes
}

// Connect is the MQTT handshake. Authentication and identity policy belong to
// the access adapter, after complete wire validation.
type Connect struct {
	ClientID     string
	CleanStart   bool
	KeepAlive    uint16
	UsernameFlag bool
	PasswordFlag bool
	Username     string
	Password     []byte
	Properties   []Property
	Will         *Will
}

func (*Connect) Type() Type { return CONNECT }

// Will is the CONNECT payload's publication, before authorization or scheduling.
type Will struct {
	QoS        byte
	Retain     bool
	Topic      string
	Payload    []byte
	Properties []Property
}

type Pingreq struct{}

func (*Pingreq) Type() Type { return PINGREQ }

type Pingresp struct{}

func (*Pingresp) Type() Type { return PINGRESP }

// Publish owns the immutable application bytes for one wire publication.
// PacketID belongs to the protocol exchange and is zero at QoS 0.
type Publish struct {
	Topic      string
	Payload    []byte
	Properties []Property
	PacketID   uint16
	QoS        byte
	Dup        bool
	Retain     bool
}

func (*Publish) Type() Type { return PUBLISH }

type Subscription struct {
	Filter            string
	QoS               byte
	NoLocal           bool
	RetainAsPublished bool
	RetainHandling    byte
}

type Subscribe struct {
	PacketID      uint16
	Properties    []Property
	Subscriptions []Subscription
}

func (*Subscribe) Type() Type { return SUBSCRIBE }

type Unsubscribe struct {
	PacketID   uint16
	Properties []Property
	Filters    []string
}

func (*Unsubscribe) Type() Type { return UNSUBSCRIBE }

type Puback struct {
	PacketID   uint16
	Reason     byte
	Properties []Property
}

func (*Puback) Type() Type { return PUBACK }

type Disconnect struct {
	Reason     byte
	Properties []Property
}

func (*Disconnect) Type() Type { return DISCONNECT }

func (l Limits) subscriptions() int {
	if l.MaxSubscriptions <= 0 {
		return 128
	}
	return l.MaxSubscriptions
}

// Connack advertises only capabilities ready at the composition root.
type Connack struct {
	SessionPresent bool
	Reason         byte
	Properties     []Property
}

func (*Connack) Type() Type { return CONNACK }

type Suback struct {
	PacketID   uint16
	Properties []Property
	Reasons    []byte
}

func (*Suback) Type() Type { return SUBACK }

type Unsuback struct {
	PacketID   uint16
	Properties []Property
	Reasons    []byte
}

func (*Unsuback) Type() Type { return UNSUBACK }
