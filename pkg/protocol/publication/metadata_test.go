package publication_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

func fixture() publication.Metadata {
	return publication.Metadata{
		Source: publication.SourceMQTT, QoS: 1, AcceptedAtMS: 1000,
		PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t",
		Properties: []publication.Property{
			{Kind: publication.UserProperty, Text: "x", Value: "one"},
			{Kind: publication.MessageExpiry, Number: 60},
			{Kind: publication.UserProperty, Text: "x", Value: "two"},
		},
	}
}

// This literal is the frozen v1 layout, independently of encoder output:
// version/source/QoS, int64 acceptance time, three uint16-sized strings,
// uint16 property count, then kind-specific values in original order.
const fixtureHex = "01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f"

func TestMetadataV1PreservesPublisherQoSAndOrderedDuplicateProperties(t *testing.T) {
	want := fixture()
	literal, err := hex.DecodeString(fixtureHex)
	if err != nil {
		t.Fatal(err)
	}
	got, err := publication.Decode(literal)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decode: %#v, %v", got, err)
	}
	encoded, err := publication.Encode(want)
	if err != nil || !bytes.Equal(encoded, literal) {
		t.Fatalf("encode: %x, %v", encoded, err)
	}
}

func TestMetadataRejectsInvalidMeaningWithoutDroppingFields(t *testing.T) {
	for name, change := range map[string]func(*publication.Metadata){
		"source":              func(m *publication.Metadata) { m.Source = 99 },
		"qos":                 func(m *publication.Metadata) { m.QoS = 2 },
		"missing clock":       func(m *publication.Metadata) { m.AcceptedAtMS = 0 },
		"negative clock":      func(m *publication.Metadata) { m.AcceptedAtMS = -1 },
		"will ingress clock":  func(m *publication.Metadata) { m.Source = publication.SourceWill },
		"namespace empty":     func(m *publication.Metadata) { m.PublisherNamespace = "" },
		"client whitespace":   func(m *publication.Metadata) { m.PublisherClientID = "  " },
		"identity too long":   func(m *publication.Metadata) { m.PublisherClientID = strings.Repeat("a", 1025) },
		"identity NUL":        func(m *publication.Metadata) { m.PublisherNamespace = "a\x00" },
		"identity UTF8":       func(m *publication.Metadata) { m.PublisherClientID = "\xff" },
		"empty topic":         func(m *publication.Metadata) { m.OriginalTopic = "" },
		"wildcard topic":      func(m *publication.Metadata) { m.OriginalTopic = "a/+" },
		"topic limit":         func(m *publication.Metadata) { m.OriginalTopic = strings.Repeat("a", 2049) },
		"topic NUL":           func(m *publication.Metadata) { m.OriginalTopic = "a\x00" },
		"property UTF8":       func(m *publication.Metadata) { m.Properties[0].Text = "\xff" },
		"property NUL":        func(m *publication.Metadata) { m.Properties[0].Value = "a\x00" },
		"duplicate singleton": func(m *publication.Metadata) { m.Properties = append(m.Properties, m.Properties[1]) },
		"hidden number":       func(m *publication.Metadata) { m.Properties[0].Number = 1 },
		"hidden text":         func(m *publication.Metadata) { m.Properties[1].Text = "secret" },
		"hidden value":        func(m *publication.Metadata) { m.Properties[1].Value = "secret" },
		"hidden binary":       func(m *publication.Metadata) { m.Properties[0].Binary = []byte{9} },
		"format": func(m *publication.Metadata) {
			m.Properties = []publication.Property{{Kind: publication.PayloadFormat, Number: 256}}
		},
		"response empty": func(m *publication.Metadata) {
			m.Properties = []publication.Property{{Kind: publication.ResponseTopic}}
		},
		"response wildcard": func(m *publication.Metadata) {
			m.Properties = []publication.Property{{Kind: publication.ResponseTopic, Text: "a/#"}}
		},
		"content hidden value": func(m *publication.Metadata) {
			m.Properties = []publication.Property{{Kind: publication.ContentType, Text: "json", Value: "secret"}}
		},
		"binary hidden text": func(m *publication.Metadata) {
			m.Properties = []publication.Property{{Kind: publication.CorrelationData, Text: "secret"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := fixture()
			change(&m)
			if _, err := publication.Encode(m); !errors.Is(err, publication.ErrInvalid) {
				t.Fatalf("invalid metadata accepted or misclassified: %v", err)
			}
		})
	}
	base, _ := hex.DecodeString(fixtureHex)
	for name, mutate := range map[string]func([]byte) []byte{
		"source":              func(b []byte) []byte { b[1] = 99; return b },
		"qos":                 func(b []byte) []byte { b[2] = 2; return b },
		"clock":               func(b []byte) []byte { clear(b[3:11]); return b },
		"namespace UTF8":      func(b []byte) []byte { b[13] = 0xff; return b },
		"topic wildcard":      func(b []byte) []byte { b[19] = '#'; return b },
		"duplicate expiry":    func(b []byte) []byte { b[21] = 4; return append(b, 2, 0, 0, 0, 60) },
		"format out of range": func(b []byte) []byte { b[21] = 4; return append(b, 1, 2) },
	} {
		t.Run("decode/"+name, func(t *testing.T) {
			if _, err := publication.Decode(mutate(bytes.Clone(base))); !errors.Is(err, publication.ErrInvalid) {
				t.Fatalf("malformed meaning accepted: %v", err)
			}
		})
	}
}

func TestMetadataOwnsAllContentAndDistinguishesEmptyFromMissing(t *testing.T) {
	m := fixture()
	m.Properties = []publication.Property{
		{Kind: publication.PayloadFormat, Number: 1},
		{Kind: publication.MessageExpiry, Number: 0},
		{Kind: publication.ContentType, Text: ""},
		{Kind: publication.ResponseTopic, Text: "reply/用户"},
		{Kind: publication.CorrelationData, Binary: []byte{0, 255, 7}},
		{Kind: publication.UserProperty, Text: "", Value: ""},
	}
	encoded, err := publication.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Properties[4].Binary[0] = 99
	decoded, err := publication.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	m.Properties[4].Binary[0] = 0
	clear(encoded)
	if !reflect.DeepEqual(decoded, m) {
		t.Fatalf("owned content lost: %#v", decoded)
	}
	m.Source, m.AcceptedAtMS, m.Properties = publication.SourceWill, 0, nil
	encoded, err = publication.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = publication.Decode(encoded)
	if err != nil || !reflect.DeepEqual(decoded, m) {
		t.Fatalf("Will template: %#v %v", decoded, err)
	}
}

func TestMetadataFormatIsBoundedAndRejectsPartialOrFutureValues(t *testing.T) {
	literal, _ := hex.DecodeString(fixtureHex)
	for n := 0; n < len(literal); n++ {
		if _, err := publication.Decode(literal[:n]); err == nil {
			t.Fatalf("accepted prefix %d", n)
		}
	}
	for name, input := range map[string][]byte{
		"trailing":             append(bytes.Clone(literal), 0),
		"future version":       append([]byte{2}, literal[1:]...),
		"unknown kind":         append(append([]byte(nil), literal[:22]...), 99),
		"count amplification":  append(append([]byte(nil), literal[:20]...), 255, 255),
		"string amplification": append(append([]byte(nil), literal[:11]...), 255, 255),
		"oversize":             make([]byte, publication.MaxEncodedBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := publication.Decode(input); err == nil {
				t.Fatal("accepted malformed input")
			}
		})
	}
	m := fixture()
	m.Properties = []publication.Property{{Kind: publication.ContentType, Text: strings.Repeat("x", 32743)}}
	encoded, err := publication.Encode(m)
	if err != nil || len(encoded) != 32768 {
		t.Fatalf("exact bound: %d %v", len(encoded), err)
	}
	if _, err := publication.Decode(encoded); err != nil {
		t.Fatal(err)
	}
	m.Properties[0].Text += "x"
	if _, err := publication.Encode(m); !errors.Is(err, publication.ErrTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
	m.Properties = make([]publication.Property, 128)
	for i := range m.Properties {
		m.Properties[i].Kind = publication.UserProperty
	}
	if _, err := publication.Encode(m); err != nil {
		t.Fatal(err)
	}
	m.Properties = append(m.Properties, publication.Property{Kind: publication.UserProperty})
	if _, err := publication.Encode(m); !errors.Is(err, publication.ErrTooLarge) {
		t.Fatalf("count limit: %v", err)
	}
	m.Properties = []publication.Property{{Kind: 99}}
	if _, err := publication.Encode(m); !errors.Is(err, publication.ErrUnsupported) {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestPublicationExpiryUsesOriginalClockAndWillAppendClock(t *testing.T) {
	for _, tc := range []struct {
		name               string
		source             publication.Source
		accepted, appended int64
		properties         []publication.Property
		deadline           int64
		present, invalid   bool
	}{
		{"native interval absent", publication.SourceMQTT, 1000, 9000, nil, 0, false, false},
		{"ingress not replay", publication.SourceMQTT, 1000, 9000, []publication.Property{{Kind: publication.MessageExpiry, Number: 60}}, 61000, true, false},
		{"zero expires immediately", publication.SourceMQTT, 1000, 9000, []publication.Property{{Kind: publication.MessageExpiry}}, 1000, true, false},
		{"Will starts on publish", publication.SourceWill, 0, 9000, []publication.Property{{Kind: publication.MessageExpiry, Number: 60}}, 69000, true, false},
		{"Will zero interval", publication.SourceWill, 0, 9000, []publication.Property{{Kind: publication.MessageExpiry}}, 9000, true, false},
		{"Will missing timestamp", publication.SourceWill, 0, 0, []publication.Property{{Kind: publication.MessageExpiry, Number: 60}}, 0, false, true},
		{"Will absent interval", publication.SourceWill, 0, 0, nil, 0, false, false},
		{"largest interval", publication.SourceMQTT, 1000, 0, []publication.Property{{Kind: publication.MessageExpiry, Number: math.MaxUint32}}, 4294967296000, true, false},
		{"overflow", publication.SourceMQTT, math.MaxInt64 - 999, 0, []publication.Property{{Kind: publication.MessageExpiry, Number: 1}}, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			m.Source, m.AcceptedAtMS, m.Properties = tc.source, tc.accepted, tc.properties
			deadline, present, err := m.ExpiryDeadlineMS(tc.appended)
			if tc.invalid {
				if !errors.Is(err, publication.ErrInvalid) {
					t.Fatalf("missing rejection: %v", err)
				}
				return
			}
			if err != nil || deadline != tc.deadline || present != tc.present {
				t.Fatalf("expiry = %d, %v, %v", deadline, present, err)
			}
		})
	}
}
