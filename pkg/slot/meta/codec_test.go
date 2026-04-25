package meta

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"reflect"
	"testing"
)

func TestForHashSlotReturnsShardStore(t *testing.T) {
	db := openTestDB(t)

	shard := db.ForHashSlot(7)
	if shard == nil || shard.db != db || shard.slot != 7 {
		t.Fatalf("ForHashSlot(7) = %#v", shard)
	}
}

func TestHashSlotAPIsUseUint16Signatures(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   any
	}{
		{name: "encodeStatePrefix", fn: encodeStatePrefix},
		{name: "encodeIndexPrefix", fn: encodeIndexPrefix},
		{name: "encodeMetaPrefix", fn: encodeMetaPrefix},
		{name: "encodeUserPrimaryKey", fn: encodeUserPrimaryKey},
		{name: "encodeChannelPrimaryKey", fn: encodeChannelPrimaryKey},
		{name: "encodeChannelIDIndexKey", fn: encodeChannelIDIndexKey},
		{name: "encodeChannelIDIndexPrefix", fn: encodeChannelIDIndexPrefix},
		{name: "encodeChannelRuntimeMetaPrimaryKey", fn: encodeChannelRuntimeMetaPrimaryKey},
		{name: "encodeDevicePrimaryKey", fn: encodeDevicePrimaryKey},
	} {
		if got := reflect.TypeOf(tc.fn).In(0).Kind(); got != reflect.Uint16 {
			t.Fatalf("%s first parameter kind = %s, want uint16", tc.name, got)
		}
	}

	dbType := reflect.TypeOf(&DB{})
	forHashSlots, ok := dbType.MethodByName("ForHashSlots")
	if !ok {
		t.Fatal("DB.ForHashSlots method is missing")
	}
	if got, want := forHashSlots.Type.In(1), reflect.TypeOf([]uint16(nil)); got != want {
		t.Fatalf("DB.ForHashSlots arg type = %v, want %v", got, want)
	}

	writeBatchType := reflect.TypeOf(&WriteBatch{})
	for _, name := range []string{
		"CreateUser",
		"UpsertUser",
		"UpsertDevice",
		"UpsertChannel",
		"DeleteChannel",
		"UpsertChannelRuntimeMeta",
		"UpsertUserConversationState",
		"TouchUserConversationActiveAt",
		"ClearUserConversationActiveAt",
		"UpsertChannelUpdateLog",
		"DeleteChannelUpdateLogs",
		"DeleteChannelRuntimeMeta",
		"AddSubscribers",
		"RemoveSubscribers",
	} {
		method, ok := writeBatchType.MethodByName(name)
		if !ok {
			t.Fatalf("WriteBatch.%s method is missing", name)
		}
		if got := method.Type.In(1).Kind(); got != reflect.Uint16 {
			t.Fatalf("WriteBatch.%s first arg kind = %s, want uint16", name, got)
		}
	}
}

func TestStateAndIndexPrefixesIncludeHashSlotAndSortStably(t *testing.T) {
	aState := encodeStatePrefix(1, TableIDUser)
	bState := encodeStatePrefix(2, TableIDUser)
	if bytes.Compare(aState, bState) >= 0 {
		t.Fatalf("state prefixes did not sort by hash slot: %x >= %x", aState, bState)
	}

	aIndex := encodeIndexPrefix(1, TableIDChannel, channelIndexIDChannelID)
	bIndex := encodeIndexPrefix(2, TableIDChannel, channelIndexIDChannelID)
	if bytes.Compare(aIndex, bIndex) >= 0 {
		t.Fatalf("index prefixes did not sort by hash slot: %x >= %x", aIndex, bIndex)
	}

	meta := encodeMetaPrefix(1)
	if got, want := len(meta), 3; got != want {
		t.Fatalf("len(encodeMetaPrefix(1)) = %d, want %d", got, want)
	}
}

func TestUserPrimaryKeyEncodingMatchesDoc(t *testing.T) {
	got := encodeUserPrimaryKey(7, "u1001", 0)
	want := mustHex(t, "10 00 07 00 00 00 01 00 05 75 31 30 30 31 00")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected key:\n got: %x\nwant: %x", got, want)
	}
}

func TestChannelPrimaryKeyEncodingMatchesDoc(t *testing.T) {
	got := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	want := mustHex(t, "10 00 07 00 00 00 02 00 09 67 72 6f 75 70 2d 30 30 31 80 00 00 00 00 00 00 01 00")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected key:\n got: %x\nwant: %x", got, want)
	}
}

func TestChannelIndexKeyEncodingMatchesDoc(t *testing.T) {
	got := encodeChannelIDIndexKey(7, "group-001", 1)
	want := mustHex(t, "11 00 07 00 00 00 02 00 02 00 09 67 72 6f 75 70 2d 30 30 31 80 00 00 00 00 00 00 01")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected key:\n got: %x\nwant: %x", got, want)
	}
}

func TestUserValueEncodingMatchesDoc(t *testing.T) {
	key := encodeUserPrimaryKey(7, "u1001", 0)
	got := encodeUserFamilyValue("tk_abc", 1, 2, key)
	want := mustHex(t, "01 ba c7 5d 0a 26 06 74 6b 5f 61 62 63 13 02 13 04")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected value:\n got: %x\nwant: %x", got, want)
	}
}

func TestChannelValueEncodingMatchesDoc(t *testing.T) {
	key := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	got := encodeChannelFamilyValue(0, key)
	want := mustHex(t, "6a f6 c5 9d 0a 33 00")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected value:\n got: %x\nwant: %x", got, want)
	}
}

func TestChannelIndexValueEncodingIsCompact(t *testing.T) {
	got := encodeChannelIndexValue(1)
	want := mustHex(t, "02")
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected index value:\n got: %x\nwant: %x", got, want)
	}

	decoded, err := decodeChannelIndexValue(nil, got)
	if err != nil {
		t.Fatalf("decodeChannelIndexValue(): %v", err)
	}
	if decoded != 1 {
		t.Fatalf("decoded = %d, want 1", decoded)
	}
}

func TestDecodeChannelIndexValueAcceptsLegacyWrappedValue(t *testing.T) {
	key := encodeChannelIDIndexKey(7, "group-001", 1)
	legacy := encodeChannelFamilyValue(9, key)

	decoded, err := decodeChannelIndexValue(key, legacy)
	if err != nil {
		t.Fatalf("decodeChannelIndexValue(legacy): %v", err)
	}
	if decoded != 9 {
		t.Fatalf("decoded = %d, want 9", decoded)
	}
}

func TestDecodeWrappedValueDetectsChecksumMismatch(t *testing.T) {
	key := encodeUserPrimaryKey(7, "u1001", 0)
	value := encodeUserFamilyValue("tk_abc", 1, 2, key)
	value[len(value)-1] ^= 0xff

	_, _, err := decodeWrappedValue(key, value)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

func TestDecodeUserFamilyValueRejectsUnexpectedTag(t *testing.T) {
	key := encodeUserPrimaryKey(7, "u1001", 0)
	value := encodeUserFamilyValue("tk_abc", 1, 2, key)
	value[4] = 0x09
	binary.BigEndian.PutUint32(value[:4], crc32.ChecksumIEEE(append(append([]byte{}, key...), value[4:]...)))

	_, _, _, err := decodeUserFamilyValue(key, value)
	if !errors.Is(err, ErrCorruptValue) {
		t.Fatalf("expected ErrCorruptValue, got %v", err)
	}
}

func TestDecodeUserFamilyValueRejectsMissingColumns(t *testing.T) {
	key := encodeUserPrimaryKey(7, "u1001", 0)
	payload := appendBytesValue(nil, userColumnIDToken, 0, "tk_only")
	value := wrapFamilyValue(key, payload)

	_, _, _, err := decodeUserFamilyValue(key, value)
	if !errors.Is(err, ErrCorruptValue) {
		t.Fatalf("expected ErrCorruptValue, got %v", err)
	}
}

func TestDecodeChannelFamilyValueRejectsMissingBan(t *testing.T) {
	key := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	value := wrapFamilyValue(key, nil)

	_, err := decodeChannelFamilyValue(key, value)
	if !errors.Is(err, ErrCorruptValue) {
		t.Fatalf("expected ErrCorruptValue, got %v", err)
	}
}

func TestDecodeChannelFamilyValueRejectsWrongType(t *testing.T) {
	key := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	payload := appendBytesValue(nil, channelColumnIDBan, 0, "bad")
	value := wrapFamilyValue(key, payload)

	_, err := decodeChannelFamilyValue(key, value)
	if !errors.Is(err, ErrCorruptValue) {
		t.Fatalf("expected ErrCorruptValue, got %v", err)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(bytes.NewBufferString(s).String())
	if err == nil {
		return b
	}
	compact := bytes.ReplaceAll([]byte(s), []byte(" "), nil)
	b, err = hex.DecodeString(string(compact))
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return b
}
