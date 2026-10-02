package mqtt

import (
	"bytes"
	"errors"

	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// PublicationInput owns mapped content. The authenticated sender and all IM
// permission decisions must still come from the session/usecase boundary.
// QoS 0 does not request NoPersist. Metadata is canonical publication v1 bytes.
type PublicationInput struct {
	Target      Target
	ClientMsgNo string
	Payload     []byte
	Metadata    []byte
}

// WillInput separates the schedule from content. ClientMsgNo is client metadata,
// never the server-owned, generation-bound Will append idempotency identity.
type WillInput struct {
	PublicationInput
	DelaySec uint32
}

// MapPublish maps a wire-validated client packet. Namespace, ClientID and the
// original ingress timestamp are supplied by the accepted connection, not user
// properties. The timestamp must be preserved across routing and append retries.
func MapPublish(p *wire.Publish, namespace, clientID string, acceptedAtMS int64) (PublicationInput, error) {
	if p == nil {
		return PublicationInput{}, publicationError(wire.ProtocolError)
	}
	input, _, err := mapPublication(p.Topic, p.Payload, p.QoS, p.Retain, p.Properties, publication.Metadata{
		Source: publication.SourceMQTT, PublisherNamespace: namespace, PublisherClientID: clientID, AcceptedAtMS: acceptedAtMS,
	})
	return input, err
}

// MapWill maps CONNECT content without starting the Will's publication clock.
// Scheduling and current authorization remain authoritative usecase work.
func MapWill(w *wire.Will, namespace, clientID string) (WillInput, error) {
	if w == nil {
		return WillInput{}, publicationError(wire.ProtocolError)
	}
	if len(w.Payload) > 65535 {
		return WillInput{}, publicationError(wire.PacketTooLarge)
	}
	input, delay, err := mapPublication(w.Topic, w.Payload, w.QoS, w.Retain, w.Properties, publication.Metadata{
		Source: publication.SourceWill, PublisherNamespace: namespace, PublisherClientID: clientID,
	})
	if err != nil {
		return WillInput{}, err
	}
	return WillInput{PublicationInput: input, DelaySec: delay}, nil
}

func mapPublication(topic string, payload []byte, qos byte, retain bool, properties []wire.Property, m publication.Metadata) (PublicationInput, uint32, error) {
	var input PublicationInput
	if qos > 1 {
		return input, 0, publicationError(0x9b)
	}
	if retain {
		return input, 0, publicationError(0x9a)
	}
	if len(properties) > publication.MaxProperties || len(payload) > wire.DefaultMaxPacketBytes {
		return input, 0, publicationError(wire.PacketTooLarge)
	}
	target, err := ParseTopic(topic)
	if err != nil {
		return input, 0, err
	}
	clientMsgNo, err := ClientMessageNumber(properties)
	if err != nil {
		return input, 0, err
	}
	m.OriginalTopic, m.QoS = topic, qos
	m.Properties = make([]publication.Property, 0, len(properties))
	var delay uint32
	var delaySeen bool
	for _, p := range properties {
		v := publication.Property{Number: p.Number, Text: p.Text, Value: p.Value, Binary: p.Data}
		switch p.ID {
		case wire.PayloadFormatIndicator:
			v.Kind = publication.PayloadFormat
		case wire.MessageExpiryInterval:
			v.Kind = publication.MessageExpiry
		case wire.ContentType:
			v.Kind = publication.ContentType
		case wire.ResponseTopic:
			v.Kind = publication.ResponseTopic
		case wire.CorrelationData:
			v.Kind = publication.CorrelationData
		case wire.UserProperty:
			if p.Text == clientMessageProperty {
				continue
			}
			v.Kind = publication.UserProperty
		case wire.WillDelayInterval:
			if m.Source != publication.SourceWill || delaySeen {
				return input, 0, publicationError(wire.ProtocolError)
			}
			delay, delaySeen = p.Number, true
			continue
		case wire.TopicAlias:
			return input, 0, publicationError(0x94)
		default:
			return input, 0, publicationError(wire.ProtocolError)
		}
		m.Properties = append(m.Properties, v)
	}
	encoded, err := publication.Encode(m)
	if err == nil && m.Source == publication.SourceWill && len(encoded) > publication.MaxWillTemplateBytes {
		// The execution identity is assigned later by the fenced lifecycle,
		// but its bytes must fit before the CONNECT configuration is accepted.
		err = publication.ErrTooLarge
	}
	if err != nil {
		if errors.Is(err, publication.ErrTooLarge) {
			return input, 0, publicationError(wire.PacketTooLarge)
		}
		return input, 0, publicationError(wire.ProtocolError)
	}
	return PublicationInput{Target: target, ClientMsgNo: clientMsgNo, Payload: bytes.Clone(payload), Metadata: encoded}, delay, nil
}

func publicationError(reason byte) error {
	return &wire.Error{Reason: reason, Detail: "invalid or unsupported publication"}
}
