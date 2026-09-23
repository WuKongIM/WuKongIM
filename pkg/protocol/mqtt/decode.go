package mqtt

import (
	"encoding/binary"
	"unicode/utf8"
)

// Decode consumes exactly one packet. Incomplete input returns (nil, 0, nil),
// malformed input consumes nothing, and successful values own their bytes.
// A complete header is sufficient to reject an oversized announced packet.
func Decode(in []byte, limits Limits) (Packet, int, error) {
	if len(in) == 0 {
		return nil, 0, nil
	}
	typ, flags := Type(in[0]>>4), in[0]&15
	if typ == 0 {
		return nil, 0, malformed("reserved packet type")
	}
	if typ != PUBLISH {
		want := byte(0)
		if typ == SUBSCRIBE || typ == UNSUBSCRIBE || typ == 6 {
			want = 2
		}
		if flags != want {
			return nil, 0, malformed("reserved fixed header flags")
		}
	}
	size, n, err := variableInteger(in[1:])
	if err != nil || n == 0 {
		return nil, 0, err
	}
	total := 1 + n + int(size)
	if total > limits.packetBytes() {
		return nil, 0, &Error{Reason: PacketTooLarge, Detail: "packet limit exceeded"}
	}
	if len(in) < total {
		return nil, 0, nil
	}
	r := reader{b: in[1+n : total]}
	var p Packet
	switch typ {
	case CONNECT:
		p, err = decodeConnect(&r, limits)
	case PUBLISH:
		p, err = decodePublish(&r, flags, limits)
	case SUBSCRIBE:
		p, err = decodeSubscribe(&r, limits)
	case UNSUBSCRIBE:
		p, err = decodeUnsubscribe(&r, limits)
	case PUBACK:
		p, err = decodePuback(&r, limits)
	case DISCONNECT:
		p, err = decodeDisconnect(&r, limits)
	case PINGREQ:
		p = &Pingreq{}
	case PINGRESP:
		p = &Pingresp{}
	default:
		err = &Error{Reason: ProtocolError, Detail: "unsupported packet type"}
	}
	if err != nil {
		return nil, 0, err
	}
	if r.err != nil {
		return nil, 0, r.err
	}
	if len(r.b) != 0 {
		return nil, 0, malformed("trailing packet bytes")
	}
	return p, total, nil
}

func decodeConnect(r *reader, limits Limits) (Packet, error) {
	name := r.text()
	version := r.byte()
	if r.err != nil {
		return nil, r.err
	}
	if name != "MQTT" || version != 5 {
		return nil, &Error{Reason: UnsupportedProtocolVersion, Detail: "requires MQTT 5"}
	}
	flags := r.byte()
	if flags&1 != 0 || flags&0x18 == 0x18 || (flags&4 == 0 && flags&0x38 != 0) {
		return nil, malformed("invalid CONNECT flags")
	}
	c := &Connect{CleanStart: flags&2 != 0, KeepAlive: r.uint16()}
	c.Properties = decodeProperties(r, CONNECT, limits)
	if r.err != nil {
		return nil, r.err
	}
	var method, authData bool
	for _, prop := range c.Properties {
		if prop.ID == AuthenticationMethod {
			method = true
		}
		if prop.ID == AuthenticationData {
			authData = true
		}
	}
	if authData && !method {
		return nil, &Error{Reason: ProtocolError, Detail: "authentication data without method"}
	}
	c.ClientID = r.text()
	if flags&4 != 0 {
		c.Will = &Will{QoS: (flags >> 3) & 3, Retain: flags&0x20 != 0}
		c.Will.Properties = decodeProperties(r, willProperties, limits)
		c.Will.Topic = r.text()
		c.Will.Payload = r.binary()
		if r.err != nil {
			return nil, r.err
		}
		if err := validatePayload(c.Will.Payload, c.Will.Properties); err != nil {
			return nil, err
		}
		if !validTopicName(c.Will.Topic) && r.err == nil {
			r.err = &Error{Reason: ProtocolError, Detail: "invalid Will topic"}
		}
	}
	c.UsernameFlag, c.PasswordFlag = flags&0x80 != 0, flags&0x40 != 0
	if c.UsernameFlag {
		c.Username = r.text()
	}
	if c.PasswordFlag {
		c.Password = r.binary()
	}
	return c, nil
}

// variableInteger distinguishes an incomplete transport header from invalid
// encoding. MQTT 5 requires the minimum number of bytes (section 1.5.5).
func variableInteger(b []byte) (uint32, int, error) {
	var value uint32
	for i := 0; i < 4; i++ {
		if i >= len(b) {
			return 0, 0, nil
		}
		value |= uint32(b[i]&127) << (7 * i)
		if b[i]&128 == 0 {
			if i > 0 && b[i] == 0 {
				return 0, 0, malformed("nonminimal variable integer")
			}
			return value, i + 1, nil
		}
	}
	return 0, 0, malformed("overlong variable integer")
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n > len(r.b) {
		r.err = malformed("truncated field")
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}
func (r *reader) byte() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (r *reader) uint16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}

func (r *reader) uint32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (r *reader) variable() uint32 {
	if r.err != nil {
		return 0
	}
	v, n, err := variableInteger(r.b)
	if err != nil {
		r.err = err
		return 0
	}
	if n == 0 {
		r.err = malformed("truncated variable integer")
		return 0
	}
	r.b = r.b[n:]
	return v
}
func (r *reader) binary() []byte { b := r.take(int(r.uint16())); return append([]byte(nil), b...) }
func (r *reader) text() string {
	b := r.take(int(r.uint16()))
	if !validText(b) {
		r.err = malformed("invalid UTF-8 string")
		return ""
	}
	return string(b)
}
func validText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}
