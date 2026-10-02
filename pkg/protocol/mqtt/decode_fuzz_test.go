package mqtt_test

import (
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

// FuzzDecodeBoundedPacket exercises hostile transport bytes at the public codec
// boundary. It cannot open a listener or create any persistent state.
func FuzzDecodeBoundedPacket(f *testing.F) {
	for _, fixture := range []string{basicConnect, "c000", "e000", "40020001", "820700010000016100", "300700016102010100", "30ffffffff", "10ff7f"} {
		b, err := hex.DecodeString(fixture)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		p, n, err := mqtt.Decode(input, mqtt.Limits{MaxPacketBytes: 4096, MaxPropertyBytes: 512, MaxProperties: 16, MaxSubscriptions: 16})
		if err != nil || p == nil {
			if p != nil || n != 0 {
				t.Fatal("failure/incomplete input consumed a packet")
			}
			return
		}
		if n <= 0 || n > len(input) || n > 4096 {
			t.Fatalf("invalid consumption: %d", n)
		}
		if p.Type() < mqtt.CONNECT || p.Type() > mqtt.DISCONNECT {
			t.Fatal("invalid packet type escaped")
		}
	})
}
