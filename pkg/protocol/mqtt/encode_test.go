package mqtt_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/eclipse/paho.golang/packets"
)

func TestStandardClientDecodesServerPackets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		packet mqtt.Packet
		want   string
	}{
		{"connack capabilities", &mqtt.Connack{SessionPresent: true, Properties: []mqtt.Property{{ID: mqtt.MaximumQoS, Number: 1}, {ID: mqtt.RetainAvailable}, {ID: mqtt.WildcardSubscriptionAvailable}, {ID: mqtt.SharedSubscriptionAvailable}}}, "200b0100082401250028002a00"},
		{"publish", &mqtt.Publish{Topic: "a", QoS: 1, PacketID: 7, Dup: true, Payload: []byte{0, 255}, Properties: []mqtt.Property{{ID: mqtt.UserProperty, Text: "x", Value: "a"}, {ID: mqtt.UserProperty, Text: "x", Value: "b"}}}, "3a1600016100070e260001780001612600017800016200ff"},
		{"puback success", &mqtt.Puback{PacketID: 7}, "40020007"},
		{"puback rejected", &mqtt.Puback{PacketID: 7, Reason: 0x87}, "4003000787"},
		{"suback", &mqtt.Suback{PacketID: 7, Reasons: []byte{1, 0x87}}, "90050007000187"},
		{"unsuback", &mqtt.Unsuback{PacketID: 7, Reasons: []byte{0, 0x11}}, "b0050007000011"},
		{"pingresp", &mqtt.Pingresp{}, "d000"},
		{"normal disconnect", &mqtt.Disconnect{}, "e00100"},
		{"takeover", &mqtt.Disconnect{Reason: 0x8e}, "e0018e"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := mqtt.Encode(tc.packet, mqtt.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(b) != tc.want {
				t.Fatalf("want %s, got %x", tc.want, b)
			}
			decoded, err := packets.ReadPacket(bytes.NewReader(b))
			if err != nil || decoded.Type != byte(tc.packet.Type()) {
				t.Fatalf("standard client: %v", err)
			}
			if pub, ok := decoded.Content.(*packets.Publish); ok {
				if pub.Topic != "a" || !pub.Duplicate || pub.PacketID != 7 || len(pub.Properties.User) != 2 || pub.Properties.User[1].Value != "b" {
					t.Fatal("standard client lost publication identity/properties")
				}
			}
		})
	}
}

func TestEncodeEnforcesPeerLimitsAndValidWire(t *testing.T) {
	for _, p := range []mqtt.Packet{
		nil, (*mqtt.Publish)(nil), &mqtt.Connect{},
		&mqtt.Connack{SessionPresent: true, Reason: 0x87},
		&mqtt.Puback{PacketID: 0}, &mqtt.Puback{PacketID: 1, Reason: 3},
		&mqtt.Suback{PacketID: 1}, &mqtt.Unsuback{PacketID: 1, Reasons: []byte{2}},
		&mqtt.Publish{Topic: "a", QoS: 0, PacketID: 1},
		&mqtt.Publish{Topic: "a", QoS: 1}, &mqtt.Publish{Topic: "a", QoS: 3, PacketID: 1},
		&mqtt.Publish{Topic: "a", QoS: 0, Dup: true}, &mqtt.Publish{Topic: "#"},
		&mqtt.Publish{Topic: "a\x00b"},
		&mqtt.Disconnect{Properties: []mqtt.Property{{ID: mqtt.SessionExpiryInterval}, {ID: mqtt.SessionExpiryInterval}}},
		&mqtt.Connack{Properties: []mqtt.Property{{ID: mqtt.ReceiveMaximum, Number: 65536}}},
		&mqtt.Connack{Properties: []mqtt.Property{{ID: mqtt.MaximumQoS, Number: 2}}},
		&mqtt.Publish{Topic: "a", Properties: []mqtt.Property{{ID: mqtt.SubscriptionIdentifier, Number: 268435456}}},
		&mqtt.Publish{Topic: "a", Properties: []mqtt.Property{{ID: mqtt.UserProperty, Text: "x", Value: "\x00"}}},
	} {
		if b, err := mqtt.Encode(p, mqtt.Limits{}); b != nil || err == nil {
			t.Fatalf("invalid packet accepted: %T, %x, %v", p, b, err)
		}
	}
	p := &mqtt.Publish{Topic: "a", Payload: []byte("0123456789")}
	_, err := mqtt.Encode(p, mqtt.Limits{MaxPacketBytes: 15})
	var e *mqtt.Error
	if !errors.As(err, &e) || e.Reason != mqtt.PacketTooLarge {
		t.Fatalf("limit: %v", err)
	}
	b, err := mqtt.Encode(p, mqtt.Limits{MaxPacketBytes: 16})
	if err != nil || len(b) != 16 {
		t.Fatalf("exact limit: %d %v", len(b), err)
	}
	p.Payload[0] = 'x'
	if b[6] != '0' {
		t.Fatal("encoded packet aliases caller payload")
	}
}
