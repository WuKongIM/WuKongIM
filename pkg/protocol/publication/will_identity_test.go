package publication_test

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

func TestWillIdentityCanonicalV2(t *testing.T) {
	m := fixture()
	m.Source, m.AcceptedAtMS = publication.SourceWill, 0
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
	// v1 body with source/time changed, then a 77-byte server key.
	body, _ := hex.DecodeString("020201000000000000000000016e00016300017400030600017800036f6e65020000003c06000178000374776f004d")
	want := append(body, m.ServerWillKey...)
	b, err := publication.Encode(m)
	if err != nil || !bytes.Equal(b, want) {
		t.Fatalf("canonical v2: %x %v", b, err)
	}
	got, err := publication.Decode(want)
	if err != nil || !reflect.DeepEqual(got, m) {
		t.Fatalf("decode v2: %+v %v", got, err)
	}
	clear(want)
	if got.ServerWillKey != m.ServerWillKey {
		t.Fatal("decoded identity aliases input")
	}
	for i := range b {
		if _, err := publication.Decode(b[:i]); err == nil {
			t.Fatalf("accepted prefix %d", i)
		}
	}
	for _, mutate := range []func([]byte) []byte{
		func(v []byte) []byte { return append(v, 0) },
		func(v []byte) []byte { v[0] = 1; return v },
		func(v []byte) []byte { v[0] = 3; return v },
		func(v []byte) []byte { v[len(v)-1] = 'G'; return v },
	} {
		if _, err := publication.Decode(mutate(bytes.Clone(b))); err == nil {
			t.Fatal("accepted noncanonical v2")
		}
	}
	for _, bad := range []string{"mqtt-will-v1:", "client-key", "mqtt-will-v1:" + strings.Repeat("A", 64), m.ServerWillKey + "a"} {
		m.ServerWillKey = bad
		if _, err := publication.Encode(m); err == nil {
			t.Fatalf("accepted invalid identity %q", bad)
		}
	}
	m = fixture()
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
	if _, err := publication.Encode(m); err == nil {
		t.Fatal("ordinary MQTT accepted server Will domain")
	}
}

func TestWillIdentityBoundsAndContent(t *testing.T) {
	m := fixture()
	m.Source, m.AcceptedAtMS = publication.SourceWill, 0
	legacy, _ := publication.Encode(m)
	if legacy[0] != 1 {
		t.Fatal("unkeyed Will template must retain v1")
	}
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("a", 64)
	a, _ := publication.Encode(m)
	m.ServerWillKey = "mqtt-will-v1:" + strings.Repeat("b", 64)
	b, _ := publication.Encode(m)
	same, err := publication.SameContent(a, b)
	fa, ea := publication.ContentFingerprint(a)
	fb, eb := publication.ContentFingerprint(b)
	if same || err != nil || ea != nil || eb != nil || fa == fb {
		t.Fatal("content identity omitted Will key")
	}
	m.Properties = []publication.Property{{Kind: publication.CorrelationData}}
	base, _ := publication.Encode(m)
	m.Properties[0].Binary = make([]byte, publication.MaxEncodedBytes-len(base))
	b, err = publication.Encode(m)
	if err != nil || len(b) != publication.MaxEncodedBytes {
		t.Fatalf("exact bound: %d %v", len(b), err)
	}
	if _, err := publication.Decode(b); err != nil {
		t.Fatal(err)
	}
	m.Properties[0].Binary = append(m.Properties[0].Binary, 0)
	if _, err := publication.Encode(m); err == nil {
		t.Fatal("identity tail omitted from size bound")
	}
}
