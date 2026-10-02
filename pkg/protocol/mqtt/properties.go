package mqtt

import "unicode/utf8"

// PropertyID is the MQTT 5 wire property identifier.
type PropertyID byte

const (
	PayloadFormatIndicator          PropertyID = 0x01
	MessageExpiryInterval           PropertyID = 0x02
	ContentType                     PropertyID = 0x03
	ResponseTopic                   PropertyID = 0x08
	CorrelationData                 PropertyID = 0x09
	SubscriptionIdentifier          PropertyID = 0x0b
	SessionExpiryInterval           PropertyID = 0x11
	AssignedClientIdentifier        PropertyID = 0x12
	ServerKeepAlive                 PropertyID = 0x13
	AuthenticationMethod            PropertyID = 0x15
	AuthenticationData              PropertyID = 0x16
	RequestProblemInformation       PropertyID = 0x17
	WillDelayInterval               PropertyID = 0x18
	RequestResponseInformation      PropertyID = 0x19
	ResponseInformation             PropertyID = 0x1a
	ServerReference                 PropertyID = 0x1c
	ReasonString                    PropertyID = 0x1f
	ReceiveMaximum                  PropertyID = 0x21
	TopicAliasMaximum               PropertyID = 0x22
	TopicAlias                      PropertyID = 0x23
	MaximumQoS                      PropertyID = 0x24
	RetainAvailable                 PropertyID = 0x25
	UserProperty                    PropertyID = 0x26
	MaximumPacketSize               PropertyID = 0x27
	WildcardSubscriptionAvailable   PropertyID = 0x28
	SubscriptionIdentifierAvailable PropertyID = 0x29
	SharedSubscriptionAvailable     PropertyID = 0x2a
)

// Property stores exactly the value shape selected by ID. Number is used for
// integer properties, Text for UTF-8 strings, Data for binary, and Text/Value
// for User Property pairs. A slice preserves order and duplicate user keys.
type Property struct {
	ID     PropertyID
	Number uint32
	Text   string
	Value  string
	Data   []byte
}

type propertyKind byte

const (
	propertyByte propertyKind = iota + 1
	propertyUint16
	propertyUint32
	propertyVBI
	propertyText
	propertyBinary
	propertyPair
)

// willProperties is a property context, never a control packet type.
const willProperties Type = 0

func propertyShape(id PropertyID, context Type) propertyKind {
	var kind propertyKind
	var mask uint16
	switch id {
	case PayloadFormatIndicator:
		kind, mask = propertyByte, 1<<PUBLISH|1<<willProperties
	case MessageExpiryInterval:
		kind, mask = propertyUint32, 1<<PUBLISH|1<<willProperties
	case ContentType, ResponseTopic:
		kind, mask = propertyText, 1<<PUBLISH|1<<willProperties
	case CorrelationData:
		kind, mask = propertyBinary, 1<<PUBLISH|1<<willProperties
	case SubscriptionIdentifier:
		kind, mask = propertyVBI, 1<<PUBLISH|1<<SUBSCRIBE
	case SessionExpiryInterval:
		kind, mask = propertyUint32, 1<<CONNECT|1<<CONNACK|1<<DISCONNECT
	case AssignedClientIdentifier, ResponseInformation:
		kind, mask = propertyText, 1<<CONNACK
	case ServerKeepAlive:
		kind, mask = propertyUint16, 1<<CONNACK
	case AuthenticationMethod:
		kind, mask = propertyText, 1<<CONNECT|1<<CONNACK|1<<15
	case AuthenticationData:
		kind, mask = propertyBinary, 1<<CONNECT|1<<CONNACK|1<<15
	case RequestProblemInformation, RequestResponseInformation:
		kind, mask = propertyByte, 1<<CONNECT
	case WillDelayInterval:
		kind, mask = propertyUint32, 1<<willProperties
	case ServerReference:
		kind, mask = propertyText, 1<<CONNACK|1<<DISCONNECT
	case ReasonString:
		kind, mask = propertyText, 1<<CONNACK|1<<PUBACK|1<<5|1<<6|1<<7|1<<SUBACK|1<<UNSUBACK|1<<DISCONNECT|1<<15
	case ReceiveMaximum, TopicAliasMaximum:
		kind, mask = propertyUint16, 1<<CONNECT|1<<CONNACK
	case TopicAlias:
		kind, mask = propertyUint16, 1<<PUBLISH
	case MaximumQoS, RetainAvailable, WildcardSubscriptionAvailable, SubscriptionIdentifierAvailable, SharedSubscriptionAvailable:
		kind, mask = propertyByte, 1<<CONNACK
	case MaximumPacketSize:
		kind, mask = propertyUint32, 1<<CONNECT|1<<CONNACK
	case UserProperty:
		kind, mask = propertyPair, 0xcfff // all property contexts except PINGREQ/PINGRESP
	}
	if context > 15 || mask&(1<<context) == 0 {
		return 0
	}
	return kind
}

func decodeProperties(r *reader, context Type, limits Limits) []Property {
	n := r.variable()
	if r.err != nil {
		return nil
	}
	if n > uint32(limits.propertyBytes()) {
		r.err = &Error{Reason: PacketTooLarge, Detail: "property byte limit exceeded"}
		return nil
	}
	data := r.take(int(n))
	if r.err != nil {
		return nil
	}
	pr := reader{b: data}
	var result []Property
	var seen uint64
	for len(pr.b) > 0 && pr.err == nil {
		if len(result) >= limits.properties() {
			pr.err = &Error{Reason: 0x97, Detail: "property count limit exceeded"}
			break
		}
		id := pr.variable()
		if id > 0x2a {
			pr.err = malformed("unknown property")
			break
		}
		p := Property{ID: PropertyID(id)}
		kind := propertyShape(p.ID, context)
		if kind == 0 {
			pr.err = malformed("property not allowed on packet")
			break
		}
		if seen&(1<<id) != 0 && p.ID != UserProperty && !(p.ID == SubscriptionIdentifier && context == PUBLISH) {
			pr.err = &Error{Reason: ProtocolError, Detail: "duplicate singleton property"}
			break
		}
		seen |= 1 << id
		switch kind {
		case propertyByte:
			p.Number = uint32(pr.byte())
		case propertyUint16:
			p.Number = uint32(pr.uint16())
		case propertyUint32:
			p.Number = pr.uint32()
		case propertyVBI:
			p.Number = pr.variable()
		case propertyText:
			p.Text = pr.text()
		case propertyBinary:
			p.Data = pr.binary()
		case propertyPair:
			p.Text, p.Value = pr.text(), pr.text()
		}
		if pr.err == nil {
			pr.err = validateProperty(p, kind)
		}
		result = append(result, p)
	}
	if pr.err != nil {
		r.err = pr.err
		return nil
	}
	return result
}

func validateProperty(p Property, kind propertyKind) error {
	if kind == propertyByte && p.Number > 1 {
		return &Error{Reason: ProtocolError, Detail: "invalid boolean property"}
	}
	switch p.ID {
	case ReceiveMaximum, MaximumPacketSize, SubscriptionIdentifier, TopicAlias:
		if p.Number == 0 {
			return &Error{Reason: ProtocolError, Detail: "zero property value"}
		}
	case ResponseTopic:
		if !validTopicName(p.Text) {
			return &Error{Reason: ProtocolError, Detail: "invalid response topic"}
		}
	}
	return nil
}

func validTopicName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c == '+' || c == '#' {
			return false
		}
	}
	return true
}

func validatePayload(payload []byte, properties []Property) error {
	for _, p := range properties {
		if p.ID == PayloadFormatIndicator && p.Number == 1 && !utf8.Valid(payload) {
			return &Error{Reason: 0x99, Detail: "payload is not valid UTF-8"}
		}
	}
	return nil
}
