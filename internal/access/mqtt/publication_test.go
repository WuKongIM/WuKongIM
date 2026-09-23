package mqtt_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

func publicationPacket() *wire.Publish {
	return &wire.Publish{
		Topic: "wk/v1/users/Ym9i/messages", Payload: []byte("body"), QoS: 1, PacketID: 7,
		Properties: []wire.Property{
			{ID: wire.UserProperty, Text: "x", Value: "first"},
			{ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "message-1"},
			{ID: wire.MessageExpiryInterval, Number: 60},
			{ID: wire.PayloadFormatIndicator, Number: 1},
			{ID: wire.ContentType, Text: "text/plain"},
			{ID: wire.ResponseTopic, Text: "reply/topic"},
			{ID: wire.CorrelationData, Data: []byte{0, 255}},
			{ID: wire.UserProperty, Text: "x", Value: "second"},
		},
	}
}

func TestPublishMappingOwnsContentAndKeepsRecoverySemantics(t *testing.T) {
	p := publicationPacket()
	input, err := access.MapPublish(p, "main", "client-1", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if input.Target != (access.Target{ChannelID: "bob", ChannelType: 1}) || input.ClientMsgNo != "message-1" || string(input.Payload) != "body" {
		t.Fatalf("mapping: %#v", input)
	}
	clear(p.Payload)
	clear(p.Properties[6].Data)
	m, err := publication.Decode(input.Metadata)
	want := publication.Metadata{
		Source: publication.SourceMQTT, QoS: 1, AcceptedAtMS: 1000,
		PublisherNamespace: "main", PublisherClientID: "client-1", OriginalTopic: "wk/v1/users/Ym9i/messages",
		Properties: []publication.Property{
			{Kind: publication.UserProperty, Text: "x", Value: "first"},
			{Kind: publication.MessageExpiry, Number: 60},
			{Kind: publication.PayloadFormat, Number: 1},
			{Kind: publication.ContentType, Text: "text/plain"},
			{Kind: publication.ResponseTopic, Text: "reply/topic"},
			{Kind: publication.CorrelationData, Binary: []byte{0, 255}},
			{Kind: publication.UserProperty, Text: "x", Value: "second"},
		},
	}
	if err != nil || !reflect.DeepEqual(m, want) || string(input.Payload) != "body" {
		t.Fatalf("durable content: %#v %v", m, err)
	}
	p = publicationPacket()
	p.QoS, p.PacketID = 0, 0
	input, err = access.MapPublish(p, "main", "client-1", 1000)
	if err != nil {
		t.Fatal(err)
	}
	m, err = publication.Decode(input.Metadata)
	if err != nil || m.QoS != 0 || !bytes.Equal(input.Payload, p.Payload) {
		t.Fatal("QoS 0 content lost")
	}
}

func TestWillMappingSeparatesScheduleFromPublicationExpiry(t *testing.T) {
	p := publicationPacket()
	w := &wire.Will{Topic: p.Topic, Payload: p.Payload, QoS: p.QoS, Properties: append(p.Properties, wire.Property{ID: wire.WillDelayInterval, Number: 300})}
	input, err := access.MapWill(w, "main", "client-1")
	if err != nil || input.DelaySec != 300 || input.ClientMsgNo != "message-1" {
		t.Fatalf("Will: %#v %v", input, err)
	}
	m, err := publication.Decode(input.Metadata)
	if err != nil || m.Source != publication.SourceWill || m.AcceptedAtMS != 0 || len(m.Properties) != 7 {
		t.Fatalf("Will metadata: %#v %v", m, err)
	}
	deadline, present, err := m.ExpiryDeadlineMS(900000)
	if err != nil || !present || deadline != 960000 {
		t.Fatalf("Will expiry: %d %v %v", deadline, present, err)
	}
	clear(w.Payload)
	if string(input.Payload) != "body" {
		t.Fatal("Will borrowed mutable packet payload")
	}
}

func TestPublicationMappingRejectsUnsupportedOrForgedContent(t *testing.T) {
	for name, tc := range map[string]struct {
		modify func(*wire.Publish)
		reason byte
	}{
		"QoS 2":       {func(p *wire.Publish) { p.QoS = 2 }, 0x9b},
		"retain":      {func(p *wire.Publish) { p.Retain = true }, 0x9a},
		"bad topic":   {func(p *wire.Publish) { p.Topic = "wk/v1/users/+/messages" }, 0x90},
		"missing key": {func(p *wire.Publish) { p.Properties = nil }, 0x83},
		"forged server key": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "wk.message_id", Value: "42"})
		}, 0x87},
		"subscription id": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.SubscriptionIdentifier, Number: 1})
		}, 0x82},
		"topic alias": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.TopicAlias, Number: 1})
		}, 0x94},
		"Will Delay in PUBLISH": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.WillDelayInterval, Number: 1})
		}, 0x82},
		"duplicate expiry": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.MessageExpiryInterval, Number: 1})
		}, 0x82},
		"metadata overhead": {func(p *wire.Publish) {
			p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "extra", Value: strings.Repeat("s", 32650)})
		}, 0x95},
	} {
		t.Run(name, func(t *testing.T) {
			p := publicationPacket()
			tc.modify(p)
			_, err := access.MapPublish(p, "main", "client-1", 1000)
			var protocolErr *wire.Error
			if !errors.As(err, &protocolErr) || protocolErr.Reason != tc.reason {
				t.Fatalf("expected %x, got %v", tc.reason, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("content leaked in error")
			}
		})
	}
	if _, err := access.MapPublish(nil, "main", "client-1", 1000); err == nil {
		t.Fatal("nil publication accepted")
	}
	if _, err := access.MapWill(nil, "main", "client-1"); err == nil {
		t.Fatal("nil Will accepted")
	}
	for _, tc := range []struct {
		namespace, clientID string
		at                  int64
	}{{"", "c", 1000}, {"n", "", 1000}, {"n", "c", 0}} {
		if _, err := access.MapPublish(publicationPacket(), tc.namespace, tc.clientID, tc.at); err == nil {
			t.Fatal("missing publisher or clock accepted")
		}
	}
	p := publicationPacket()
	w := &wire.Will{Topic: p.Topic, Payload: bytes.Repeat([]byte{1}, 65536), QoS: 1, Properties: p.Properties}
	if _, err := access.MapWill(w, "main", "client-1"); err == nil {
		t.Fatal("oversized Will accepted")
	}
	w.Payload = nil
	w.Properties = append(w.Properties, wire.Property{ID: wire.WillDelayInterval, Number: 1}, wire.Property{ID: wire.WillDelayInterval, Number: 2})
	if _, err := access.MapWill(w, "main", "client-1"); err == nil {
		t.Fatal("duplicate Will schedule silently changed")
	}
}
