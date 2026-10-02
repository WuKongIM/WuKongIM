package mqtt

import "encoding/binary"

// Encode produces one owned server packet, subject to negotiated packet limits.
// It refuses invalid values instead of truncating fields or serializing a packet
// whose declared lengths differ from its content. It does not write the socket.
func Encode(packet Packet, limits Limits) ([]byte, error) {
	w := writer{max: limits.packetBytes()}
	var typ Type
	var flags byte
	switch p := packet.(type) {
	case *Connack:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = CONNACK
		if !validReason(typ, p.Reason) || (p.SessionPresent && p.Reason != 0) {
			return nil, malformed("invalid CONNACK")
		}
		if p.SessionPresent {
			w.byte(1)
		} else {
			w.byte(0)
		}
		w.byte(p.Reason)
		w.properties(p.Properties, typ, limits)
	case *Publish:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = PUBLISH
		if !validTopicName(p.Topic) || p.QoS > 2 || (p.QoS == 0 && (p.Dup || p.PacketID != 0)) {
			return nil, malformed("invalid outbound PUBLISH")
		}
		flags = p.QoS << 1
		if p.Dup {
			flags |= 8
		}
		if p.Retain {
			flags |= 1
		}
		w.text(p.Topic)
		if p.QoS > 0 {
			w.packetID(p.PacketID)
		}
		w.properties(p.Properties, typ, limits)
		if err := validatePayload(p.Payload, p.Properties); err != nil {
			return nil, err
		}
		w.raw(p.Payload)
	case *Puback:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = PUBACK
		w.packetID(p.PacketID)
		if !validReason(typ, p.Reason) {
			return nil, malformed("invalid PUBACK reason")
		}
		if p.Reason != 0 || len(p.Properties) > 0 {
			w.byte(p.Reason)
		}
		if len(p.Properties) > 0 {
			w.properties(p.Properties, typ, limits)
		}
	case *Suback:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = SUBACK
		w.ack(p.PacketID, p.Properties, p.Reasons, typ, limits)
	case *Unsuback:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = UNSUBACK
		w.ack(p.PacketID, p.Properties, p.Reasons, typ, limits)
	case *Pingresp:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = PINGRESP
	case *Disconnect:
		if p == nil {
			return nil, malformed("nil packet")
		}
		typ = DISCONNECT
		if !validReason(typ, p.Reason) {
			return nil, malformed("invalid DISCONNECT reason")
		}
		// Include even the normal reason for clients whose decoders reject the
		// legal zero-length DISCONNECT form (including Paho v0.23.0).
		w.byte(p.Reason)
		if len(p.Properties) > 0 {
			w.properties(p.Properties, typ, limits)
		}
	default:
		return nil, malformed("unsupported outbound packet")
	}
	if w.err != nil {
		return nil, w.err
	}
	header := writer{max: 5}
	header.byte(byte(typ)<<4 | flags)
	header.variable(uint32(len(w.b)))
	if header.err != nil {
		return nil, header.err
	}
	if len(header.b)+len(w.b) > limits.packetBytes() {
		return nil, &Error{Reason: PacketTooLarge, Detail: "outbound packet limit exceeded"}
	}
	return append(header.b, w.b...), nil
}

type writer struct {
	b   []byte
	max int
	err error
}

func (w *writer) raw(b []byte) {
	if w.err != nil {
		return
	}
	if len(b) > w.max-len(w.b) {
		w.err = &Error{Reason: PacketTooLarge, Detail: "outbound byte limit exceeded"}
		return
	}
	w.b = append(w.b, b...)
}
func (w *writer) byte(b byte)     { w.raw([]byte{b}) }
func (w *writer) uint16(v uint16) { var b [2]byte; binary.BigEndian.PutUint16(b[:], v); w.raw(b[:]) }
func (w *writer) uint32(v uint32) { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); w.raw(b[:]) }
func (w *writer) variable(v uint32) {
	if v > 268435455 {
		w.err = malformed("variable integer overflow")
		return
	}
	for {
		b := byte(v & 127)
		v >>= 7
		if v != 0 {
			b |= 128
		}
		w.byte(b)
		if v == 0 {
			return
		}
	}
}
func (w *writer) text(s string) {
	if len(s) > 65535 || !validText([]byte(s)) {
		w.err = malformed("invalid outbound UTF-8 string")
		return
	}
	w.uint16(uint16(len(s)))
	w.raw([]byte(s))
}
func (w *writer) binary(b []byte) {
	if len(b) > 65535 {
		w.err = malformed("binary field overflow")
		return
	}
	w.uint16(uint16(len(b)))
	w.raw(b)
}
func (w *writer) packetID(id uint16) {
	if id == 0 {
		w.err = malformed("zero packet identifier")
		return
	}
	w.uint16(id)
}
func (w *writer) ack(id uint16, properties []Property, reasons []byte, typ Type, limits Limits) {
	if len(reasons) == 0 || len(reasons) > limits.subscriptions() {
		w.err = malformed("invalid ACK reason count")
		return
	}
	for _, reason := range reasons {
		if !validReason(typ, reason) {
			w.err = malformed("invalid ACK reason")
			return
		}
	}
	w.packetID(id)
	w.properties(properties, typ, limits)
	w.raw(reasons)
}
func (w *writer) properties(properties []Property, context Type, limits Limits) {
	if w.err != nil {
		return
	}
	if len(properties) > limits.properties() {
		w.err = &Error{Reason: 0x97, Detail: "property count limit exceeded"}
		return
	}
	pw := writer{max: min(limits.propertyBytes(), limits.packetBytes())}
	var seen uint64
	for _, p := range properties {
		kind := propertyShape(p.ID, context)
		if kind == 0 {
			w.err = malformed("property not allowed on packet")
			return
		}
		if seen&(uint64(1)<<p.ID) != 0 && p.ID != UserProperty && !(p.ID == SubscriptionIdentifier && context == PUBLISH) {
			w.err = malformed("duplicate singleton property")
			return
		}
		seen |= uint64(1) << p.ID
		if err := validateProperty(p, kind); err != nil {
			w.err = err
			return
		}
		if (kind == propertyUint16 && p.Number > 65535) || (kind == propertyVBI && p.Number > 268435455) {
			w.err = malformed("integer property overflow")
			return
		}
		pw.byte(byte(p.ID))
		switch kind {
		case propertyByte:
			pw.byte(byte(p.Number))
		case propertyUint16:
			pw.uint16(uint16(p.Number))
		case propertyUint32:
			pw.uint32(p.Number)
		case propertyVBI:
			pw.variable(p.Number)
		case propertyText:
			pw.text(p.Text)
		case propertyBinary:
			pw.binary(p.Data)
		case propertyPair:
			pw.text(p.Text)
			pw.text(p.Value)
		}
		if pw.err != nil {
			w.err = pw.err
			return
		}
	}
	w.variable(uint32(len(pw.b)))
	w.raw(pw.b)
}
