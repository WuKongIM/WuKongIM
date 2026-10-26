package frame

import (
	"encoding/hex"
	"testing"
)

func TestHandshakeFingerprintVectors(t *testing.T) {
	// Fixed vectors were independently calculated from the documented byte tuple.
	for _, test := range []struct {
		key   string
		stamp int64
		want  string
	}{
		{"", 1700000000123, "00000000000000000000000000000000"},
		{"abc", 0, "aad570da67a771828587616631133475"},
		{"abc", 1, "b99411b6eec26a46c70d59d89732409c"},
		{"abd", 0, "e293f9ec81b058f3772c324b4d682215"},
		{"opaque-client-key", 1700000000123, "382d6d5418bf374c708ca41aa9b855d0"},
		{"abc", -1, "b3825c75363678a78178ba856d6719c8"},
	} {
		got := HandshakeFingerprint(test.key, test.stamp)
		if hex.EncodeToString(got[:]) != test.want {
			t.Fatalf("fingerprint(%q,%d) = %x, want %s", test.key, test.stamp, got, test.want)
		}
	}
}
