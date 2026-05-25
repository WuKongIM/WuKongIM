package keycodec

import (
	"encoding/binary"
	"fmt"
)

const maxStringLen = 1<<16 - 1

// Domain identifies a top-level storage domain in encoded keys.
type Domain byte

const (
	// DomainMessage stores channel message data.
	DomainMessage Domain = 0x01
	// DomainMeta stores hash-slot metadata data.
	DomainMeta Domain = 0x02
)

// PartitionKind identifies the partition namespace after the domain byte.
type PartitionKind byte

const (
	// PartitionGlobal stores domain-global rows such as catalogs.
	PartitionGlobal PartitionKind = 0x00
	// PartitionChannel stores channel-scoped message rows.
	PartitionChannel PartitionKind = 0x01
	// PartitionHashSlot stores hash-slot-scoped metadata rows.
	PartitionHashSlot PartitionKind = 0x02
)

// Space identifies the logical table keyspace inside a partition.
type Space byte

const (
	// SpaceRow stores primary row families.
	SpaceRow Space = 0x10
	// SpaceIndex stores secondary indexes.
	SpaceIndex Space = 0x11
	// SpaceSystem stores domain system state.
	SpaceSystem Space = 0x12
	// SpaceCatalog stores domain catalogs.
	SpaceCatalog Space = 0x13
)

// AppendString appends a length-prefixed string key part.
func AppendString(dst []byte, value string) []byte {
	if len(value) > maxStringLen {
		panic(fmt.Sprintf("keycodec: string key part too long: %d", len(value)))
	}
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(value)))
	return append(dst, value...)
}

// ReadString reads a length-prefixed string key part.
func ReadString(src []byte) (string, []byte, error) {
	if len(src) < 2 {
		return "", nil, fmt.Errorf("keycodec: string length truncated")
	}
	n := int(binary.BigEndian.Uint16(src[:2]))
	src = src[2:]
	if len(src) < n {
		return "", nil, fmt.Errorf("keycodec: string payload truncated")
	}
	return string(src[:n]), src[n:], nil
}

// AppendUint64 appends a big-endian uint64 key part.
func AppendUint64(dst []byte, value uint64) []byte {
	return binary.BigEndian.AppendUint64(dst, value)
}

// AppendUint16 appends a big-endian uint16 key part.
func AppendUint16(dst []byte, value uint16) []byte {
	return binary.BigEndian.AppendUint16(dst, value)
}

// AppendUint32 appends a big-endian uint32 key part.
func AppendUint32(dst []byte, value uint32) []byte {
	return binary.BigEndian.AppendUint32(dst, value)
}

// AppendInt64Ordered appends an int64 key part that sorts by numeric order.
func AppendInt64Ordered(dst []byte, value int64) []byte {
	ordered := uint64(value) ^ (uint64(1) << 63)
	return binary.BigEndian.AppendUint64(dst, ordered)
}

// AppendInt64Desc appends an int64 key part that sorts in descending numeric order.
func AppendInt64Desc(dst []byte, value int64) []byte {
	ordered := uint64(value) ^ (uint64(1) << 63)
	return binary.BigEndian.AppendUint64(dst, ^ordered)
}
