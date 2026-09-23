// Package publication defines bounded, entry-neutral publication attributes
// that must follow a message through durable replication and recovery.
package publication

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	// MaxEncodedBytes bounds the complete value, including publisher identity.
	MaxEncodedBytes  = 32 << 10
	MaxProperties    = 128
	MaxIdentityBytes = 1024
	MaxTopicBytes    = 2048
)

var (
	ErrInvalid     = errors.New("invalid publication metadata")
	ErrTooLarge    = errors.New("publication metadata limit exceeded")
	ErrUnsupported = errors.New("unsupported publication metadata format")
)

// Source identifies the immutable origin, not the current connection or owner.
type Source byte

const (
	SourceMQTT Source = 1
	SourceWill Source = 2
)

// Kind is a durable semantic property identifier, independent of wire IDs.
type Kind byte

const (
	PayloadFormat   Kind = 1
	MessageExpiry   Kind = 2
	ContentType     Kind = 3
	ResponseTopic   Kind = 4
	CorrelationData Kind = 5
	UserProperty    Kind = 6
)

// Property is one ordered content attribute. Only the fields used by Kind may
// be populated; user properties alone may occur more than once.
type Property struct {
	Kind   Kind
	Number uint32
	Text   string
	Value  string
	Binary []byte
}

// Metadata is immutable publication provenance and content, never an authority
// proof. Native publications omit this optional value entirely.
type Metadata struct {
	Source Source
	// QoS is the original publication QoS, not a recipient's negotiated QoS.
	QoS byte
	// AcceptedAtMS is MQTT ingress time. Will uses zero and starts its expiry
	// clock at the immutable source message's ServerTimestampMS instead.
	AcceptedAtMS int64
	// PublisherNamespace and PublisherClientID identify the stable publisher
	// across reconnects; they do not confer current connection authority.
	PublisherNamespace string
	PublisherClientID  string
	// OriginalTopic is provenance, not necessarily the recipient's output topic.
	OriginalTopic string
	Properties    []Property
}

// ExpiryDeadlineMS preserves the original lifetime without restarting it on a
// retry or replay copy. Will requires the original source append timestamp.
// A present zero interval expires immediately; absence means no message expiry.
// Delivery policy must not abandon an already begun QoS exchange at this time.
func (m Metadata) ExpiryDeadlineMS(serverTimestampMS int64) (int64, bool, error) {
	if _, err := m.encodedSize(); err != nil {
		return 0, false, err
	}
	for _, p := range m.Properties {
		if p.Kind != MessageExpiry {
			continue
		}
		basis := m.AcceptedAtMS
		if m.Source == SourceWill {
			basis = serverTimestampMS
		}
		durationMS := int64(p.Number) * 1000
		if basis <= 0 || basis > math.MaxInt64-durationMS {
			return 0, false, ErrInvalid
		}
		return basis + durationMS, true, nil
	}
	return 0, false, nil
}

// Encode returns an owned canonical v1 value. The full allocation is bounded.
func Encode(m Metadata) ([]byte, error) {
	n, err := m.encodedSize()
	if err != nil {
		return nil, err
	}
	b := make([]byte, 3, n)
	b[0], b[1], b[2] = 1, byte(m.Source), m.QoS
	b = binary.BigEndian.AppendUint64(b, uint64(m.AcceptedAtMS))
	b = appendString(b, m.PublisherNamespace)
	b = appendString(b, m.PublisherClientID)
	b = appendString(b, m.OriginalTopic)
	b = binary.BigEndian.AppendUint16(b, uint16(len(m.Properties)))
	for _, p := range m.Properties {
		b = append(b, byte(p.Kind))
		switch p.Kind {
		case PayloadFormat:
			b = append(b, byte(p.Number))
		case MessageExpiry:
			b = binary.BigEndian.AppendUint32(b, p.Number)
		case ContentType, ResponseTopic:
			b = appendString(b, p.Text)
		case CorrelationData:
			b = binary.BigEndian.AppendUint16(b, uint16(len(p.Binary)))
			b = append(b, p.Binary...)
		case UserProperty:
			b = appendString(b, p.Text)
			b = appendString(b, p.Value)
		}
	}
	return b, nil
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

// Decode returns owned strings and binary values, rejecting partial or future
// data instead of dropping attributes that affect delivery after recovery.
func Decode(b []byte) (Metadata, error) {
	var m Metadata
	if len(b) > MaxEncodedBytes {
		return m, ErrTooLarge
	}
	if len(b) == 0 {
		return m, ErrInvalid
	}
	if b[0] != 1 {
		return m, ErrUnsupported
	}
	d := decoder{b: b[1:]}
	m.Source, m.QoS = Source(d.number(1)), byte(d.number(1))
	m.AcceptedAtMS = int64(d.number(8))
	m.PublisherNamespace, m.PublisherClientID, m.OriginalTopic = string(d.sized()), string(d.sized()), string(d.sized())
	count := d.number(2)
	if count > MaxProperties {
		return Metadata{}, ErrTooLarge
	}
	if count > 0 {
		m.Properties = make([]Property, 0, count)
	}
	for i := uint64(0); i < count && !d.failed; i++ {
		p := Property{Kind: Kind(d.number(1))}
		switch p.Kind {
		case PayloadFormat:
			p.Number = uint32(d.number(1))
		case MessageExpiry:
			p.Number = uint32(d.number(4))
		case ContentType, ResponseTopic:
			p.Text = string(d.sized())
		case CorrelationData:
			p.Binary = bytes.Clone(d.sized())
		case UserProperty:
			p.Text, p.Value = string(d.sized()), string(d.sized())
		default:
			return Metadata{}, ErrUnsupported
		}
		m.Properties = append(m.Properties, p)
	}
	if d.failed || len(d.b) != 0 {
		return Metadata{}, ErrInvalid
	}
	if _, err := m.encodedSize(); err != nil {
		return Metadata{}, err
	}
	return m, nil
}

type decoder struct {
	b      []byte
	failed bool
}

func (d *decoder) number(n int) uint64 {
	if len(d.b) < n {
		d.failed = true
		return 0
	}
	var v uint64
	for _, b := range d.b[:n] {
		v = v<<8 | uint64(b)
	}
	d.b = d.b[n:]
	return v
}
func (d *decoder) sized() []byte {
	n := int(d.number(2))
	if d.failed || len(d.b) < n {
		d.failed = true
		return nil
	}
	b := d.b[:n]
	d.b = d.b[n:]
	return b
}

// encodedSize validates all semantic fields before either allocation or use.
func (m Metadata) encodedSize() (int, error) {
	if m.Source != SourceMQTT && m.Source != SourceWill || m.QoS > 1 ||
		m.Source == SourceMQTT && m.AcceptedAtMS <= 0 || m.Source == SourceWill && m.AcceptedAtMS != 0 ||
		!validIdentity(m.PublisherNamespace) || !validIdentity(m.PublisherClientID) ||
		len(m.OriginalTopic) > MaxTopicBytes || !validTopic(m.OriginalTopic) {
		return 0, ErrInvalid
	}
	if len(m.Properties) > MaxProperties {
		return 0, ErrTooLarge
	}
	n := 19 + len(m.PublisherNamespace) + len(m.PublisherClientID) + len(m.OriginalTopic)
	var seen uint8
	for _, p := range m.Properties {
		if p.Kind < PayloadFormat || p.Kind > UserProperty {
			return 0, ErrUnsupported
		}
		if p.Kind != UserProperty && seen&(1<<p.Kind) != 0 {
			return 0, ErrInvalid
		}
		seen |= 1 << p.Kind
		if len(p.Text) > MaxEncodedBytes || len(p.Value) > MaxEncodedBytes || len(p.Binary) > MaxEncodedBytes {
			return 0, ErrTooLarge
		}
		if !validString(p.Text) || !validString(p.Value) {
			return 0, ErrInvalid
		}
		n++
		switch p.Kind {
		case PayloadFormat, MessageExpiry:
			if p.Text != "" || p.Value != "" || len(p.Binary) != 0 || p.Kind == PayloadFormat && p.Number > 1 {
				return 0, ErrInvalid
			}
			if p.Kind == PayloadFormat {
				n++
			} else {
				n += 4
			}
		case ContentType, ResponseTopic:
			if p.Number != 0 || p.Value != "" || len(p.Binary) != 0 || p.Kind == ResponseTopic && !validTopic(p.Text) {
				return 0, ErrInvalid
			}
			n += 2 + len(p.Text)
		case CorrelationData:
			if p.Number != 0 || p.Text != "" || p.Value != "" {
				return 0, ErrInvalid
			}
			n += 2 + len(p.Binary)
		case UserProperty:
			if p.Number != 0 || len(p.Binary) != 0 {
				return 0, ErrInvalid
			}
			n += 4 + len(p.Text) + len(p.Value)
		}
		if n > MaxEncodedBytes {
			return 0, ErrTooLarge
		}
	}
	return n, nil
}

func validString(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func validIdentity(s string) bool {
	return len(s) <= MaxIdentityBytes && strings.TrimSpace(s) != "" && validString(s)
}
func validTopic(s string) bool { return s != "" && validString(s) && !strings.ContainsAny(s, "#+") }

// SameContent compares canonical publication content for an application-key
// retry. It validates both optional values and excludes only the server-assigned
// ingress clock. Callers must retain the original record/clock on a match and
// compare the body separately. This is not an MQTT packet-exchange identity.
func SameContent(left, right []byte) (bool, error) {
	for _, value := range [][]byte{left, right} {
		if len(value) != 0 {
			if _, err := Decode(value); err != nil {
				return false, err
			}
		}
	}
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right), nil
	}
	// Version 1 has version/source/QoS, then the eight-byte AcceptedAtMS clock.
	return bytes.Equal(left[:3], right[:3]) && bytes.Equal(left[11:], right[11:]), nil
}

// ContentFingerprint returns a bounded lookup hash that excludes the ingress
// clock. It validates optional metadata and returns zero for native messages.
// Collisions remain possible: callers must confirm matches with SameContent.
func ContentFingerprint(value []byte) (uint64, error) {
	if len(value) == 0 {
		return 0, nil
	}
	if _, err := Decode(value); err != nil {
		return 0, err
	}
	hash := uint64(14695981039346656037)
	for _, part := range [][]byte{value[:3], value[11:]} {
		for _, b := range part {
			hash ^= uint64(b)
			hash *= 1099511628211
		}
	}
	return hash, nil
}
