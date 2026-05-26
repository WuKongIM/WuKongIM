package meta

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestMetaKeysUseHashSlotPartitions(t *testing.T) {
	key := encodeUserRowKey(7, "u1", userPrimaryFamilyID)
	wantPrefix := []byte{byte(keycodec.DomainMeta), byte(keycodec.PartitionHashSlot)}
	if !bytes.HasPrefix(key, wantPrefix) {
		t.Fatalf("user key %x missing meta/hash-slot prefix %x", key, wantPrefix)
	}
	if !bytes.Contains(key, []byte("u1")) {
		t.Fatalf("user key %x missing uid", key)
	}

	rowSpan := hashSlotRowSpan(7)
	if !bytes.HasPrefix(key, rowSpan.Start) {
		t.Fatalf("user key %x outside row span start %x", key, rowSpan.Start)
	}
	indexKey := encodeChannelIDIndexKey(7, "ch1", 2)
	indexSpan := hashSlotIndexSpan(7)
	if !bytes.HasPrefix(indexKey, indexSpan.Start) {
		t.Fatalf("index key %x outside index span start %x", indexKey, indexSpan.Start)
	}
	systemKey := encodeHashSlotSystemKey(7, systemIDSnapshot)
	systemSpan := hashSlotSystemSpan(7)
	if !bytes.HasPrefix(systemKey, systemSpan.Start) {
		t.Fatalf("system key %x outside system span start %x", systemKey, systemSpan.Start)
	}
}

func TestMetaActiveIndexSortsNewestFirst(t *testing.T) {
	newer := encodeActiveIndexKey(9, TableIDConversation, conversationActiveIndexID, 200, "u1", "ch")
	older := encodeActiveIndexKey(9, TableIDConversation, conversationActiveIndexID, 100, "u1", "ch")
	if bytes.Compare(newer, older) >= 0 {
		t.Fatalf("newer active index %x should sort before older %x", newer, older)
	}
}
