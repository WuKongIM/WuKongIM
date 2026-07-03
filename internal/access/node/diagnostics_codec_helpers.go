package node

import (
	"encoding/binary"
	"fmt"
)

func appendNodeInt(dst []byte, value int) []byte {
	return appendVarint(dst, int64(value))
}

func readNodeInt(body []byte, offset int, label string) (int, int, error) {
	value, next, err := readVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	if value > int64(^uint(0)>>1) || value < -int64(^uint(0)>>1)-1 {
		return 0, offset, fmt.Errorf("internal/access/node: %s overflows int", label)
	}
	return int(value), next, nil
}

func appendNodeVarint(dst []byte, value int64) []byte {
	return appendVarint(dst, value)
}

func readNodeVarint(body []byte, offset int) (int64, int, error) {
	return readVarint(body, offset)
}

func readNodeMarker(body []byte, offset int, label string) (byte, int, error) {
	return readByte(body, offset, label)
}

func readCollectionLen(count uint64, remaining int, label string) (int, error) {
	if count > uint64(remaining) {
		return 0, fmt.Errorf("internal/access/node: %s length exceeds payload", label)
	}
	if count > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("internal/access/node: %s length overflows int", label)
	}
	return int(count), nil
}

func binaryMaxVarintLen64() int {
	return binary.MaxVarintLen64
}
