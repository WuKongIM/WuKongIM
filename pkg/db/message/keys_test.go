package message

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestMessageRowKeyUsesChannelPartition(t *testing.T) {
	key := encodeMessageRowKey(ChannelKey("ch-1"), 7, messageHeaderFamilyID)
	prefix := []byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionChannel)}
	if !bytes.HasPrefix(key, prefix) {
		t.Fatalf("key %x missing message/channel prefix", key)
	}
	if !bytes.Contains(key, []byte("ch-1")) {
		t.Fatalf("key %x missing channel key", key)
	}
}

func TestMessageCatalogKeyUsesGlobalPartition(t *testing.T) {
	key := encodeCatalogKey(ChannelKey("ch-1"))
	prefix := []byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionGlobal), byte(keycodec.SpaceCatalog)}
	if !bytes.HasPrefix(key, prefix) {
		t.Fatalf("catalog key %x missing global catalog prefix %x", key, prefix)
	}
}

func TestMessageIndexPrefixIncludesIndexID(t *testing.T) {
	prefix := encodeMessageIndexPrefix(ChannelKey("ch-1"), messageIndexIDMessageID)
	wantPrefix := []byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionChannel)}
	if !bytes.HasPrefix(prefix, wantPrefix) {
		t.Fatalf("index prefix %x missing domain partition", prefix)
	}
	if !bytes.Contains(prefix, []byte{byte(messageIndexIDMessageID)}) {
		t.Fatalf("index prefix %x missing index id", prefix)
	}
}

func TestChannelLogAppendKeyCacheMatchesEncoders(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := store.db.Channel(ChannelKey("cached:1"), ChannelID{ID: "cached", Type: 1})
	cache := log.appendKeyCache

	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		{
			name: "row_header",
			got:  cache.messageRowKey(7, messageHeaderFamilyID),
			want: encodeMessageRowKey(log.key, 7, messageHeaderFamilyID),
		},
		{
			name: "row_header_to",
			got: func() []byte {
				key := make([]byte, cache.messageRowKeyLen())
				cache.writeMessageRowKey(key, 7, messageHeaderFamilyID)
				return key
			}(),
			want: encodeMessageRowKey(log.key, 7, messageHeaderFamilyID),
		},
		{
			name: "row_payload",
			got:  cache.messageRowKey(7, messagePayloadFamilyID),
			want: encodeMessageRowKey(log.key, 7, messagePayloadFamilyID),
		},
		{
			name: "message_id_index",
			got:  cache.messageIDIndexKey(99),
			want: encodeMessageIDIndexKey(log.key, 99),
		},
		{
			name: "message_id_index_to",
			got: func() []byte {
				key := make([]byte, cache.messageIDIndexKeyLen())
				cache.writeMessageIDIndexKey(key, 99)
				return key
			}(),
			want: encodeMessageIDIndexKey(log.key, 99),
		},
		{
			name: "client_msg_no_index",
			got:  cache.clientMsgNoIndexKey("client-1", 7),
			want: encodeMessageClientMsgNoIndexKey(log.key, "client-1", 7),
		},
		{
			name: "client_msg_no_index_to",
			got: func() []byte {
				key := make([]byte, cache.clientMsgNoIndexKeyLen("client-1"))
				cache.writeClientMsgNoIndexKey(key, "client-1", 7)
				return key
			}(),
			want: encodeMessageClientMsgNoIndexKey(log.key, "client-1", 7),
		},
		{
			name: "idempotency_index",
			got:  cache.idempotencyIndexKey("u1", "client-1"),
			want: encodeMessageIdempotencyIndexKey(log.key, "u1", "client-1"),
		},
		{
			name: "idempotency_index_to",
			got: func() []byte {
				key := make([]byte, cache.idempotencyIndexKeyLen("u1", "client-1"))
				cache.writeIdempotencyIndexKey(key, "u1", "client-1")
				return key
			}(),
			want: encodeMessageIdempotencyIndexKey(log.key, "u1", "client-1"),
		},
		{
			name: "catalog",
			got:  cache.catalogKey,
			want: encodeCatalogKey(log.key),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bytes.Equal(tt.got, tt.want) {
				t.Fatalf("cached key = %x, want %x", tt.got, tt.want)
			}
		})
	}
	if got, want := cache.catalogValue, encodeCatalogValue(log.id); !bytes.Equal(got, want) {
		t.Fatalf("cached catalog value = %x, want %x", got, want)
	}
}

func TestAppendKeyCacheReusesLookupScratch(t *testing.T) {
	cache := newAppendKeyCache(ChannelKey("cached:1"), ChannelID{ID: "cached", Type: 1})
	messageIDKey := make([]byte, 0, cache.messageIDIndexKeyLen())
	idempotencyKey := make([]byte, 0, cache.idempotencyIndexKeyLen("u1", "client-1"))

	allocs := testing.AllocsPerRun(100, func() {
		messageIDKey = cache.messageIDIndexKeyTo(messageIDKey, 99)
		idempotencyKey = cache.idempotencyIndexKeyTo(idempotencyKey, "u1", "client-1")
	})
	if allocs != 0 {
		t.Fatalf("lookup scratch allocations = %v, want 0", allocs)
	}
	if want := encodeMessageIDIndexKey(ChannelKey("cached:1"), 99); !bytes.Equal(messageIDKey, want) {
		t.Fatalf("message ID lookup scratch key = %x, want %x", messageIDKey, want)
	}
	if want := encodeMessageIdempotencyIndexKey(ChannelKey("cached:1"), "u1", "client-1"); !bytes.Equal(idempotencyKey, want) {
		t.Fatalf("idempotency lookup scratch key = %x, want %x", idempotencyKey, want)
	}
}
