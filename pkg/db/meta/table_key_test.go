package meta

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestTableKeyPartsRoundTrip(t *testing.T) {
	parts := KeyParts{String("u1"), Int64Ordered(-7), Int64Desc(99), Uint64(42), Uint8(3)}
	layout := KeyLayout{KeyString, KeyInt64Ordered, KeyInt64Desc, KeyUint64, KeyUint8}

	encoded, err := encodeKeyParts(nil, parts)
	if err != nil {
		t.Fatalf("encodeKeyParts(): %v", err)
	}
	decoded, rest, err := decodeKeyParts(encoded, layout)
	if err != nil {
		t.Fatalf("decodeKeyParts(): %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("rest len = %d", len(rest))
	}
	if !decoded.Equal(parts) {
		t.Fatalf("decoded = %#v, want %#v", decoded, parts)
	}
}

func TestTableKeyPartOrdering(t *testing.T) {
	older, err := encodeKeyParts(nil, KeyParts{Int64Desc(100)})
	if err != nil {
		t.Fatalf("encode older: %v", err)
	}
	newer, err := encodeKeyParts(nil, KeyParts{Int64Desc(200)})
	if err != nil {
		t.Fatalf("encode newer: %v", err)
	}
	if bytes.Compare(newer, older) >= 0 {
		t.Fatalf("desc order failed: newer=%x older=%x", newer, older)
	}
}

func TestTablePrimaryAndIndexKeysKeepExistingPrefixes(t *testing.T) {
	primary, err := encodeTablePrimaryRowKey(7, 65010, KeyParts{String("u1")}, 0)
	if err != nil {
		t.Fatalf("primary key: %v", err)
	}
	if !bytes.HasPrefix(primary, encodeRowPrefix(7, 65010)) {
		t.Fatalf("primary key %x does not use row prefix %x", primary, encodeRowPrefix(7, 65010))
	}

	index, err := encodeTableIndexKey(7, 65010, 2, KeyParts{String("owner")}, KeyParts{String("u1")})
	if err != nil {
		t.Fatalf("index key: %v", err)
	}
	if !bytes.HasPrefix(index, encodeIndexPrefix(7, 65010, 2)) {
		t.Fatalf("index key %x does not use index prefix %x", index, encodeIndexPrefix(7, 65010, 2))
	}
}

func TestDecodeTablePrimaryKeyRejectsWrongFamily(t *testing.T) {
	key, err := encodeTablePrimaryRowKey(7, 65010, KeyParts{String("u1")}, 1)
	if err != nil {
		t.Fatalf("primary key: %v", err)
	}
	_, ok := decodeTablePrimaryRowKey(encodeRowPrefix(7, 65010), key, KeyLayout{KeyString}, 0)
	if ok {
		t.Fatal("decoded key with wrong family")
	}
}

func TestTableIndexPrefixSpan(t *testing.T) {
	prefix, err := encodeTableIndexScanPrefix(7, 65010, 2, KeyParts{String("owner")})
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	span := keycodec.NewPrefixSpan(prefix)
	key, err := encodeTableIndexKey(7, 65010, 2, KeyParts{String("owner"), String("u1")}, KeyParts{String("u1")})
	if err != nil {
		t.Fatalf("index key: %v", err)
	}
	if !bytesInSpan(key, Span{Start: span.Start, End: span.End}) {
		t.Fatalf("key %x not in prefix span %#v", key, span)
	}
}
