package mqtt_test

import (
	"errors"
	"strings"
	"testing"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

func TestCanonicalApplicationTopics(t *testing.T) {
	for _, tc := range []struct {
		topic, id string
		kind      uint8
	}{
		{"wk/v1/users/Ym9i/messages", "bob", 1},
		{"wk/v1/groups/ZzE/messages", "g1", 2},
		{"wk/v1/groups/YS8jKw/messages", "a/#+", 2},
	} {
		target, err := access.ParseTopic(tc.topic)
		if err != nil || target.ChannelID != tc.id || target.ChannelType != tc.kind {
			t.Fatalf("parse: %#v %v", target, err)
		}
		encoded, err := access.FormatTopic(target)
		if err != nil || encoded != tc.topic {
			t.Fatalf("format: %s %v", encoded, err)
		}
	}
	for _, input := range []string{
		"wk/v1/users/bob/messages", // not canonical UTF-8 encoding of bob
		"wk/v1/users/Ym9i=/messages", "wk/v1/users/Ym9j\n/messages",
		"wk/v1/users//messages", "wk/v1/users/AA/messages", "wk/v1/users/_w/messages",
		"wk/v1/users/Yh/messages", // nonzero padding bits
		"wk/v1/users/Ym9i/messages/extra", "wk/v2/users/Ym9i/messages",
		"wk/v1/users/+/messages", "$share/g/wk/v1/users/Ym9i/messages",
		"wk/v1/groups/" + strings.Repeat("YQ", 1025) + "/messages",
	} {
		if _, err := access.ParseTopic(input); err == nil {
			t.Fatalf("ambiguous/invalid topic accepted: %q", input)
		}
	}
	for _, target := range []access.Target{{ChannelID: "", ChannelType: 1}, {ChannelID: "a", ChannelType: 99}, {ChannelID: "a\x00", ChannelType: 2}} {
		if _, err := access.FormatTopic(target); err == nil {
			t.Fatal("invalid target encoded")
		}
	}
}

func connectPacket() *mqtt.Connect {
	return &mqtt.Connect{ClientID: "client-1", UsernameFlag: true, Username: "alice", PasswordFlag: true, Password: []byte("secret"), Properties: []mqtt.Property{{ID: mqtt.UserProperty, Text: "wk.device_flag", Value: "1"}}}
}

func TestConnectIdentityCannotSelectDevicePrivileges(t *testing.T) {
	c := connectPacket()
	identity, err := access.Credentials(c)
	if err != nil || identity.UID != "alice" || identity.ClientID != "client-1" || identity.DeviceFlag != 1 || identity.Token != "secret" {
		t.Fatal("credential mapping failed")
	}
	for _, modify := range []func(*mqtt.Connect){
		func(c *mqtt.Connect) { c.ClientID = "" }, func(c *mqtt.Connect) { c.UsernameFlag = false },
		func(c *mqtt.Connect) { c.PasswordFlag = false }, func(c *mqtt.Connect) { c.Password = nil },
		func(c *mqtt.Connect) { c.Properties = nil },
		func(c *mqtt.Connect) { c.Properties[0].Value = "99" },
		func(c *mqtt.Connect) { c.Properties[0].Value = "01" },
		func(c *mqtt.Connect) { c.Properties = append(c.Properties, c.Properties[0]) },
		func(c *mqtt.Connect) {
			c.Properties = append(c.Properties, mqtt.Property{ID: mqtt.UserProperty, Text: "wk.device_level", Value: "1"})
		},
		func(c *mqtt.Connect) {
			c.Properties = append(c.Properties, mqtt.Property{ID: mqtt.AuthenticationMethod, Text: "enhanced"})
		},
	} {
		packet := connectPacket()
		modify(packet)
		_, err := access.Credentials(packet)
		if err == nil {
			t.Fatal("invalid identity accepted")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("credential leaked in error")
		}
	}
}

func TestPublicationMetadataReservedNamespaceAndIdempotency(t *testing.T) {
	properties := []mqtt.Property{
		{ID: mqtt.UserProperty, Text: "x", Value: "one"},
		{ID: mqtt.UserProperty, Text: "wk.client_msg_no", Value: "message-1"},
		{ID: mqtt.UserProperty, Text: "x", Value: "two"},
	}
	key, err := access.ClientMessageNumber(properties)
	if err != nil || key != "message-1" {
		t.Fatalf("key: %q %v", key, err)
	}
	for _, props := range [][]mqtt.Property{
		nil,
		{{ID: mqtt.UserProperty, Text: "wk.client_msg_no", Value: " "}},
		{{ID: mqtt.UserProperty, Text: "wk.client_msg_no", Value: strings.Repeat("x", 1025)}},
		append(append([]mqtt.Property(nil), properties...), properties[1]),
		append(append([]mqtt.Property(nil), properties...), mqtt.Property{ID: mqtt.UserProperty, Text: "wk.message_id", Value: "42"}),
	} {
		_, err := access.ClientMessageNumber(props)
		var e *mqtt.Error
		if !errors.As(err, &e) {
			t.Fatalf("expected MQTT rejection, got %v", err)
		}
	}
}
