package keycodec_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestOrderedInt64SortsNumerically(t *testing.T) {
	values := []int64{-2, -1, 0, 1, 2}
	keys := make([][]byte, 0, len(values))
	for _, v := range values {
		keys = append(keys, keycodec.AppendInt64Ordered(nil, v))
	}
	if !sort.SliceIsSorted(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 }) {
		t.Fatalf("ordered int64 keys are not sorted")
	}
}

func TestDescendingInt64SortsNewestFirst(t *testing.T) {
	newer := keycodec.AppendInt64Desc(nil, 20)
	older := keycodec.AppendInt64Desc(nil, 10)
	if bytes.Compare(newer, older) >= 0 {
		t.Fatalf("descending key for newer value should sort first: newer=%x older=%x", newer, older)
	}
}

func TestAppendAndReadString(t *testing.T) {
	key := keycodec.AppendString(nil, "channel-a")
	got, rest, err := keycodec.ReadString(key)
	if err != nil {
		t.Fatalf("ReadString(): %v", err)
	}
	if got != "channel-a" || len(rest) != 0 {
		t.Fatalf("ReadString() = %q rest=%x", got, rest)
	}
}

func TestPrefixEnd(t *testing.T) {
	got := keycodec.PrefixEnd([]byte{0x10, 0x20, 0xff})
	want := []byte{0x10, 0x21}
	if !bytes.Equal(got, want) {
		t.Fatalf("PrefixEnd() = %x, want %x", got, want)
	}
}

func TestBuilderCreatesMessageRowKey(t *testing.T) {
	var builder keycodec.Builder
	key := builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, []byte("ch-1")).
		Row(1).
		Uint64(7).
		Family(0).
		Key()

	prefix := []byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionChannel)}
	if !bytes.HasPrefix(key, prefix) {
		t.Fatalf("key %x missing domain/partition prefix %x", key, prefix)
	}
	if !bytes.Contains(key, []byte("ch-1")) {
		t.Fatalf("key %x missing partition id", key)
	}
}
