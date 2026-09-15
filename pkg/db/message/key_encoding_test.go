package message

import (
	"bytes"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestMessageReadKeysPreserveBuilderEncoding(t *testing.T) {
	for _, channelKey := range []ChannelKey{"", "a", "room:2", "频道\x00:2", ChannelKey(strings.Repeat("x", 65535))} {
		for _, n := range []uint64{0, 1, 255, 256, ^uint64(0)} {
			partition := keycodec.AppendString(nil, string(channelKey))
			var builder keycodec.Builder
			prefix := func() *keycodec.Builder {
				return builder.Reset().Domain(keycodec.DomainMessage).Partition(keycodec.PartitionChannel, partition)
			}
			checks := [][2][]byte{
				{encodeMessageChannelPartitionPrefix(channelKey), prefix().Key()},
				{encodeMessageRowPrefix(channelKey), prefix().Row(TableIDMessage).Key()},
				{encodeMessageRowKey(channelKey, n, uint16(n)), prefix().Row(TableIDMessage).Uint64(n).Family(uint16(n)).Key()},
				{encodeMessageIndexPrefix(channelKey, uint16(n)), prefix().Index(TableIDMessage, uint16(n)).Key()},
				{encodeMessageSystemPrefix(channelKey, uint16(n)), prefix().System(TableIDMessage, uint16(n)).Key()},
			}
			for i, pair := range checks {
				if !bytes.Equal(pair[0], pair[1]) {
					t.Fatalf("key kind%d channel length%d seq%d differs from durable builder", i, len(channelKey), n)
				}
			}
		}
	}
}

func TestMessageReadKeysOwnBackingArrays(t *testing.T) {
	first := encodeMessageIndexPrefix("room:2", messageIndexIDNonBusinessSeq)
	second := encodeMessageIndexPrefix("room:2", messageIndexIDNonBusinessSeq)
	before := append([]byte(nil), second...)
	first[0] ^= 255
	if !bytes.Equal(second, before) {
		t.Fatal("independent encoded keys share mutable bytes")
	}
}

func TestMessageReadKeysRejectOversizedPartition(t *testing.T) {
	key := ChannelKey(strings.Repeat("x", 65536))
	for name, encode := range map[string]func(){
		"partition": func() { encodeMessageChannelPartitionPrefix(key) },
		"row":       func() { encodeMessageRowKey(key, 1, 0) },
		"index":     func() { encodeMessageIndexPrefix(key, 1) },
		"system":    func() { encodeMessageSystemPrefix(key, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != "keycodec: string key part too long: 65536" {
					t.Fatalf("panic = %v, want shared codec rejection", got)
				}
			}()
			encode()
		})
	}
}
