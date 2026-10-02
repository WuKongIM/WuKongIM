package mqtt

import "strings"

func decodePublish(r *reader, flags byte, limits Limits) (Packet, error) {
	p := &Publish{QoS: (flags >> 1) & 3, Dup: flags&8 != 0, Retain: flags&1 != 0}
	if p.QoS == 3 || (p.QoS == 0 && p.Dup) {
		return nil, malformed("invalid PUBLISH flags")
	}
	p.Topic = r.text()
	if p.QoS > 0 {
		p.PacketID = r.packetID()
	}
	p.Properties = decodeProperties(r, PUBLISH, limits)
	if r.err != nil {
		return nil, r.err
	}
	alias := false
	for _, prop := range p.Properties {
		if prop.ID == TopicAlias {
			alias = true
		}
	}
	if !validTopicName(p.Topic) && !(p.Topic == "" && alias) {
		return nil, &Error{Reason: ProtocolError, Detail: "invalid PUBLISH topic"}
	}
	if err := validatePayload(r.b, p.Properties); err != nil {
		return nil, err
	}
	p.Payload = append([]byte(nil), r.b...)
	r.b = nil
	return p, nil
}

func decodeSubscribe(r *reader, limits Limits) (Packet, error) {
	p := &Subscribe{PacketID: r.packetID()}
	p.Properties = decodeProperties(r, SUBSCRIBE, limits)
	for len(r.b) > 0 && r.err == nil {
		if len(p.Subscriptions) >= limits.subscriptions() {
			return nil, &Error{Reason: 0x97, Detail: "subscription count limit exceeded"}
		}
		s := Subscription{Filter: r.text()}
		options := r.byte()
		if r.err != nil {
			return nil, r.err
		}
		s.QoS, s.NoLocal, s.RetainAsPublished, s.RetainHandling = options&3, options&4 != 0, options&8 != 0, (options>>4)&3
		if options&0xc0 != 0 || s.QoS == 3 || s.RetainHandling == 3 {
			return nil, malformed("invalid subscription options")
		}
		if !validFilter(s.Filter) || (s.NoLocal && strings.HasPrefix(s.Filter, "$share/")) {
			return nil, &Error{Reason: ProtocolError, Detail: "invalid subscription filter"}
		}
		p.Subscriptions = append(p.Subscriptions, s)
	}
	if len(p.Subscriptions) == 0 {
		return nil, malformed("empty SUBSCRIBE")
	}
	return p, nil
}

func decodeUnsubscribe(r *reader, limits Limits) (Packet, error) {
	p := &Unsubscribe{PacketID: r.packetID()}
	p.Properties = decodeProperties(r, UNSUBSCRIBE, limits)
	for len(r.b) > 0 && r.err == nil {
		if len(p.Filters) >= limits.subscriptions() {
			return nil, &Error{Reason: 0x97, Detail: "subscription count limit exceeded"}
		}
		filter := r.text()
		if r.err != nil {
			return nil, r.err
		}
		if !validFilter(filter) {
			return nil, &Error{Reason: ProtocolError, Detail: "invalid unsubscribe filter"}
		}
		p.Filters = append(p.Filters, filter)
	}
	if len(p.Filters) == 0 {
		return nil, malformed("empty UNSUBSCRIBE")
	}
	return p, nil
}

func decodePuback(r *reader, limits Limits) (Packet, error) {
	p := &Puback{PacketID: r.packetID()}
	if len(r.b) > 0 {
		p.Reason = r.byte()
	}
	if !validReason(PUBACK, p.Reason) {
		return nil, &Error{Reason: ProtocolError, Detail: "invalid PUBACK reason"}
	}
	if len(r.b) > 0 {
		p.Properties = decodeProperties(r, PUBACK, limits)
	}
	return p, nil
}

func decodeDisconnect(r *reader, limits Limits) (Packet, error) {
	p := &Disconnect{}
	if len(r.b) > 0 {
		p.Reason = r.byte()
	}
	if !validReason(DISCONNECT, p.Reason) {
		return nil, &Error{Reason: ProtocolError, Detail: "invalid DISCONNECT reason"}
	}
	if len(r.b) > 0 {
		p.Properties = decodeProperties(r, DISCONNECT, limits)
	}
	return p, nil
}

func (r *reader) packetID() uint16 {
	id := r.uint16()
	if id == 0 && r.err == nil {
		r.err = &Error{Reason: ProtocolError, Detail: "zero packet identifier"}
	}
	return id
}

// validFilter validates MQTT syntax only. Exact-topic product policy is applied
// later so valid but unsupported wildcard subscriptions receive a SUBACK reason.
func validFilter(s string) bool {
	if strings.HasPrefix(s, "$share/") {
		group, filter, ok := strings.Cut(s[7:], "/")
		if !ok || group == "" || strings.ContainsAny(group, "+#") {
			return false
		}
		s = filter
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '+' && s[i] != '#' {
			continue
		}
		if i > 0 && s[i-1] != '/' {
			return false
		}
		if s[i] == '#' {
			return i == len(s)-1
		}
		if i+1 < len(s) && s[i+1] != '/' {
			return false
		}
	}
	return true
}

func validReason(typ Type, reason byte) bool {
	switch typ {
	case CONNACK:
		switch reason {
		case 0, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8c, 0x90, 0x95, 0x97, 0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9f:
			return true
		}
	case SUBACK:
		switch reason {
		case 0, 1, 2, 0x80, 0x83, 0x87, 0x8f, 0x91, 0x97, 0x9e, 0xa1, 0xa2:
			return true
		}
	case UNSUBACK:
		switch reason {
		case 0, 0x11, 0x80, 0x83, 0x87, 0x8f, 0x91:
			return true
		}
	case PUBACK:
		switch reason {
		case 0, 0x10, 0x80, 0x83, 0x87, 0x90, 0x91, 0x97, 0x99:
			return true
		}
	case DISCONNECT:
		switch reason {
		case 0, 4, 0x80, 0x81, 0x82, 0x83, 0x87, 0x89, 0x8b, 0x8d, 0x8e, 0x8f, 0x90, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9e, 0x9f, 0xa0, 0xa1, 0xa2:
			return true
		}
	}
	return false
}
