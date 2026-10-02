package mqtt_test

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

func wire(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Fixtures use MQTT 5 section 3.1 field order and literal lengths, independently
// of the implementation's encoder. CONNECT: clean start, client "c", keepalive 60.
const basicConnect = "100e00044d5154540502003c00000163"

func TestConnectFramingOwnsOneCompletePacket(t *testing.T) {
	input := wire(t, basicConnect)
	for n := 0; n < len(input); n++ {
		p, used, err := mqtt.Decode(input[:n], mqtt.Limits{})
		if p != nil || used != 0 || err != nil {
			t.Fatalf("fragment %d: %T, %d, %v", n, p, used, err)
		}
	}
	p, used, err := mqtt.Decode(append(input, 0xc0, 0), mqtt.Limits{})
	if err != nil || used != 16 {
		t.Fatalf("decode: %d %v", used, err)
	}
	c, ok := p.(*mqtt.Connect)
	if !ok || c.ClientID != "c" || !c.CleanStart || c.KeepAlive != 60 {
		t.Fatalf("unexpected CONNECT: %#v", p)
	}
	input[15] = 'x'
	if c.ClientID != "c" {
		t.Fatal("decoded identity aliases transport buffer")
	}
}

func TestConnectRejectsInvalidWireBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		reason      byte
	}{
		{"reserved packet", "0000", 0x81},
		{"reserved fixed flag", "110e00044d5154540502003c00000163", 0x81},
		{"nonminimal length", "108e0000044d5154540502003c00000163", 0x81},
		{"overlong length", "10ffffffff", 0x81},
		{"version 311", "100d00044d5154540402003c000163", 0x84},
		{"wrong protocol", "100e00044d5154550502003c00000163", 0x84},
		{"reserved connect flag", "100e00044d5154540503003c00000163", 0x81},
		{"will qos without will", "100e00044d515454050a003c00000163", 0x81},
		{"will retain without will", "100e00044d5154540522003c00000163", 0x81},
		{"will qos three", "100e00044d515454051e003c00000163", 0x81},
		{"invalid utf8", "100e00044d5154540502003c000001ff", 0x81},
		{"nul utf8", "100e00044d5154540502003c00000100", 0x81},
		{"truncated string", "100e00044d5154540502003c00000263", 0x81},
		{"trailing byte", "100f00044d5154540502003c0000016300", 0x81},
		{"ping with body", "c00100", 0x81},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, n, err := mqtt.Decode(wire(t, tc.input), mqtt.Limits{})
			var protocolError *mqtt.Error
			if p != nil || n != 0 || !errors.As(err, &protocolError) || protocolError.Reason != tc.reason {
				t.Fatalf("got %T, %d, %v; want reason 0x%x", p, n, err, tc.reason)
			}
		})
	}
}

func TestPacketLimitRejectsAnnouncedSizeWithoutBody(t *testing.T) {
	_, _, err := mqtt.Decode(wire(t, "10ff7f"), mqtt.Limits{MaxPacketBytes: 64})
	var protocolError *mqtt.Error
	if !errors.As(err, &protocolError) || protocolError.Reason != 0x95 {
		t.Fatalf("want Packet Too Large, got %v", err)
	}
	if _, _, err := mqtt.Decode(wire(t, basicConnect), mqtt.Limits{MaxPacketBytes: 16}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectPreservesWillCredentialsAndOrderedProperties(t *testing.T) {
	input := wire(t, "103800044d51545405c6003c16110000003c210002260001780001612600017800016200016305180000000500016100026869000175000200ff")
	p, n, err := mqtt.Decode(input, mqtt.Limits{})
	if err != nil || n != len(input) {
		t.Fatalf("decode: %d %v", n, err)
	}
	c := p.(*mqtt.Connect)
	if !c.UsernameFlag || !c.PasswordFlag || c.Username != "u" || hex.EncodeToString(c.Password) != "00ff" {
		t.Fatal("credential flags/data changed")
	}
	if len(c.Properties) != 4 || c.Properties[0].ID != mqtt.SessionExpiryInterval || c.Properties[0].Number != 60 || c.Properties[1].Number != 2 || c.Properties[2].Text != "x" || c.Properties[2].Value != "a" || c.Properties[3].Value != "b" {
		t.Fatalf("properties: %#v", c.Properties)
	}
	if c.Will == nil || c.Will.Topic != "a" || string(c.Will.Payload) != "hi" || c.Will.Properties[0].Number != 5 {
		t.Fatal("Will changed")
	}
	for i := range input {
		input[i] = 0
	}
	if string(c.Will.Payload) != "hi" || hex.EncodeToString(c.Password) != "00ff" {
		t.Fatal("binary fields alias transport input")
	}
}

func connectProperties(t *testing.T, properties string) []byte {
	t.Helper()
	p := wire(t, properties)
	// All these literal property fixtures are shorter than 128 bytes.
	b := wire(t, "100000044d5154540502003c")
	b = append(b, byte(len(p)))
	b = append(b, p...)
	b = append(b, 0, 1, 'c')
	b[1] = byte(len(b) - 2)
	return b
}

func TestConnectPropertiesRejectInvalidOrAmplifyingInput(t *testing.T) {
	for _, tc := range []struct{ name, properties string }{
		{"unknown", "7f00"},
		{"not allowed on connect", "0100"},
		{"duplicate singleton", "210001210002"},
		{"zero receive maximum", "210000"},
		{"zero packet size", "2700000000"},
		{"invalid boolean", "1702"},
		{"truncated uint32", "110000"},
		{"truncated user value", "26000161000262"},
		{"nul user key", "26000100000162"},
		{"invalid utf8 user value", "260001610001ff"},
		{"nonminimal property identifier", "910000000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p, _, err := mqtt.Decode(connectProperties(t, tc.properties), mqtt.Limits{}); p != nil || err == nil {
				t.Fatalf("accepted malformed properties: %T %v", p, err)
			}
		})
	}
	input := connectProperties(t, "2600016100016226000161000162")
	for _, limits := range []mqtt.Limits{{MaxProperties: 1}, {MaxPropertyBytes: 13}} {
		if _, _, err := mqtt.Decode(input, limits); err == nil {
			t.Fatal("property budget not enforced")
		}
	}
}

func TestPublicationAndSubscriptionWireSemantics(t *testing.T) {
	input := wire(t, "3a140003612f6200070a020102030409000200ffdead")
	p, _, err := mqtt.Decode(input, mqtt.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	pub := p.(*mqtt.Publish)
	if pub.Topic != "a/b" || pub.PacketID != 7 || pub.QoS != 1 || !pub.Dup || pub.Retain || hex.EncodeToString(pub.Payload) != "dead" || pub.Properties[0].Number != 0x01020304 || hex.EncodeToString(pub.Properties[1].Data) != "00ff" {
		t.Fatalf("publication changed: %#v", pub)
	}
	for i := range input {
		input[i] = 0
	}
	if hex.EncodeToString(pub.Payload) != "dead" {
		t.Fatal("payload aliases transport buffer")
	}

	p, _, err = mqtt.Decode(wire(t, "820f0007020b030003612f622d00016302"), mqtt.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sub := p.(*mqtt.Subscribe)
	if sub.PacketID != 7 || sub.Properties[0].Number != 3 || len(sub.Subscriptions) != 2 || sub.Subscriptions[0].Filter != "a/b" || sub.Subscriptions[0].QoS != 1 || !sub.Subscriptions[0].NoLocal || !sub.Subscriptions[0].RetainAsPublished || sub.Subscriptions[0].RetainHandling != 2 {
		t.Fatal("subscription options changed")
	}
}

func TestSubscribeOptionsAndPacketIdentifiers(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"qos three", "3606000161000700"},
		{"dup qos zero", "380400016100"},
		{"zero publish id", "3206000161000000"},
		{"publish wildcard", "300400012b00"},
		{"missing alias and topic", "3003000000"},
		{"zero alias", "3006000003230000"},
		{"zero subscribe id", "820700000000016100"},
		{"empty subscriptions", "8203000100"},
		{"qos three subscription", "820700010000016103"},
		{"reserved option", "820700010000016140"},
		{"retain handling three", "820700010000016130"},
		{"empty filter", "8206000100000000"},
		{"invalid hash wildcard", "8209000100000361236200"},
		{"invalid plus wildcard", "82080001000002612b00"},
		{"shared no local", "8210000100000a2473686172652f672f6104"},
		{"zero subscription identifier", "82090001020b0000016100"},
		{"duplicate subscription identifier", "820b0001040b010b0200016100"},
		{"zero puback id", "40020000"},
		{"invalid puback reason", "4003000120"},
		{"empty unsubscribe", "a203000100"},
		{"bad unsubscribe flags", "a003000100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p, _, err := mqtt.Decode(wire(t, tc.input), mqtt.Limits{}); p != nil || err == nil {
				t.Fatalf("accepted: %T %v", p, err)
			}
		})
	}
	for _, input := range []string{
		"40020001",                           // compact successful PUBACK
		"4003000187",                         // reason without property length
		"e000",                               // compact normal DISCONNECT
		"e00104",                             // disconnect with Will
		"e009000726000178000179",             // ordered User Property on DISCONNECT
		"a206000100000161",                   // unsubscribe a
		"3006000003230001",                   // syntax-valid alias, admission policy negotiates maximum
		"82080001000002612f00",               // empty final topic level is valid
		"820f0007020b030003612f622d00016302", // QoS 1/no local/retain as published/handling 2; requested QoS 2
	} {
		if _, _, err := mqtt.Decode(wire(t, input), mqtt.Limits{}); err != nil {
			t.Fatalf("valid %s: %v", input, err)
		}
	}
	if _, _, err := mqtt.Decode(wire(t, "820b0001000001610000016200"), mqtt.Limits{MaxSubscriptions: 1}); err == nil {
		t.Fatal("subscription budget ignored")
	}
}

func TestPayloadFormatAndAuthenticationDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		reason      byte
	}{
		{"utf8 publication", "3007000161020101ff", 0x99},
		{"utf8 Will", "101700044d5154540506003c000001630201010001610001ff", 0x99},
		{"invalid topic encoding", "30040001ff00", 0x81},
		{"invalid filter encoding", "82070001000001ff00", 0x81},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := mqtt.Decode(wire(t, tc.input), mqtt.Limits{})
			var e *mqtt.Error
			if !errors.As(err, &e) || e.Reason != tc.reason {
				t.Fatalf("want 0x%x, got %v", tc.reason, err)
			}
		})
	}
	if _, _, err := mqtt.Decode(connectProperties(t, "16000161"), mqtt.Limits{}); err == nil {
		t.Fatal("authentication data without method accepted")
	}
	if _, err := mqtt.Encode(&mqtt.Publish{Topic: "a", Payload: []byte{255}, Properties: []mqtt.Property{{ID: mqtt.PayloadFormatIndicator, Number: 1}}}, mqtt.Limits{}); err == nil {
		t.Fatal("invalid UTF-8 payload encoded")
	}
	if _, _, err := mqtt.Decode(wire(t, "300700016102010100"), mqtt.Limits{}); err != nil {
		t.Fatalf("NUL is legal in UTF-8 payload: %v", err)
	}
}
