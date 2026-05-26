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
