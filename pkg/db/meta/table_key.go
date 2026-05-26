package meta

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// KeyPartKind identifies one encoded table key component.
type KeyPartKind uint8

const (
	KeyString KeyPartKind = iota + 1
	KeyInt64Ordered
	KeyInt64Desc
	KeyUint64
	KeyUint8
)

// KeyLayout describes how to decode ordered key bytes back into KeyParts.
type KeyLayout []KeyPartKind

// KeyParts is the logical key used by meta table primary keys and indexes.
type KeyParts []KeyPart

// KeyPart stores one typed ordered key component.
type KeyPart struct {
	Kind KeyPartKind
	S    string
	I64  int64
	U64  uint64
	U8   uint8
}

// String creates a string key part.
func String(value string) KeyPart { return KeyPart{Kind: KeyString, S: value} }

// Int64Ordered creates an int64 key part sorted by ascending numeric order.
func Int64Ordered(value int64) KeyPart { return KeyPart{Kind: KeyInt64Ordered, I64: value} }

// Int64Desc creates an int64 key part sorted by descending numeric order.
func Int64Desc(value int64) KeyPart { return KeyPart{Kind: KeyInt64Desc, I64: value} }

// Uint64 creates a uint64 key part.
func Uint64(value uint64) KeyPart { return KeyPart{Kind: KeyUint64, U64: value} }

// Uint8 creates a uint8 key part.
func Uint8(value uint8) KeyPart { return KeyPart{Kind: KeyUint8, U8: value} }

// Equal reports whether two logical keys have identical parts.
func (parts KeyParts) Equal(other KeyParts) bool {
	if len(parts) != len(other) {
		return false
	}
	for i := range parts {
		if parts[i] != other[i] {
			return false
		}
	}
	return true
}

func encodeKeyParts(dst []byte, parts KeyParts) ([]byte, error) {
	for _, part := range parts {
		switch part.Kind {
		case KeyString:
			if len(part.S) > maxKeyStringLen {
				return nil, dberrors.ErrInvalidArgument
			}
			dst = keycodec.AppendString(dst, part.S)
		case KeyInt64Ordered:
			dst = keycodec.AppendInt64Ordered(dst, part.I64)
		case KeyInt64Desc:
			dst = keycodec.AppendInt64Desc(dst, part.I64)
		case KeyUint64:
			dst = keycodec.AppendUint64(dst, part.U64)
		case KeyUint8:
			dst = append(dst, part.U8)
		default:
			return nil, fmt.Errorf("%w: unknown key part kind %d", dberrors.ErrInvalidArgument, part.Kind)
		}
	}
	return dst, nil
}

func decodeKeyParts(src []byte, layout KeyLayout) (KeyParts, []byte, error) {
	parts := make(KeyParts, 0, len(layout))
	rest := src
	for _, kind := range layout {
		switch kind {
		case KeyString:
			value, next, err := keycodec.ReadString(rest)
			if err != nil {
				return nil, nil, dberrors.ErrCorruptValue
			}
			parts = append(parts, String(value))
			rest = next
		case KeyInt64Ordered:
			if len(rest) < 8 {
				return nil, nil, dberrors.ErrCorruptValue
			}
			ordered := binary.BigEndian.Uint64(rest[:8])
			parts = append(parts, Int64Ordered(int64(ordered^(uint64(1)<<63))))
			rest = rest[8:]
		case KeyInt64Desc:
			if len(rest) < 8 {
				return nil, nil, dberrors.ErrCorruptValue
			}
			ordered := ^binary.BigEndian.Uint64(rest[:8])
			parts = append(parts, Int64Desc(int64(ordered^(uint64(1)<<63))))
			rest = rest[8:]
		case KeyUint64:
			if len(rest) < 8 {
				return nil, nil, dberrors.ErrCorruptValue
			}
			parts = append(parts, Uint64(binary.BigEndian.Uint64(rest[:8])))
			rest = rest[8:]
		case KeyUint8:
			if len(rest) < 1 {
				return nil, nil, dberrors.ErrCorruptValue
			}
			parts = append(parts, Uint8(rest[0]))
			rest = rest[1:]
		default:
			return nil, nil, fmt.Errorf("%w: unknown key layout kind %d", dberrors.ErrInvalidArgument, kind)
		}
	}
	return parts, rest, nil
}

func encodeTablePrimaryRowKey(hashSlot HashSlot, tableID uint32, primary KeyParts, familyID uint16) ([]byte, error) {
	key := encodeRowPrefix(hashSlot, tableID)
	var err error
	key, err = encodeKeyParts(key, primary)
	if err != nil {
		return nil, err
	}
	return keycodec.AppendUint16(key, familyID), nil
}

func decodeTablePrimaryRowKey(prefix []byte, key []byte, layout KeyLayout, familyID uint16) (KeyParts, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return nil, false
	}
	parts, rest, err := decodeKeyParts(key[len(prefix):], layout)
	if err != nil || len(rest) != 2 || binary.BigEndian.Uint16(rest) != familyID {
		return nil, false
	}
	return parts, true
}

func encodeTableIndexScanPrefix(hashSlot HashSlot, tableID uint32, indexID uint16, prefixParts KeyParts) ([]byte, error) {
	key := encodeIndexPrefix(hashSlot, tableID, indexID)
	return encodeKeyParts(key, prefixParts)
}

func encodeTableIndexKey(hashSlot HashSlot, tableID uint32, indexID uint16, indexParts KeyParts, primary KeyParts) ([]byte, error) {
	key, err := encodeTableIndexScanPrefix(hashSlot, tableID, indexID, indexParts)
	if err != nil {
		return nil, err
	}
	return encodeKeyParts(key, primary)
}
