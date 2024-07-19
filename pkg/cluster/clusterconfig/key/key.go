package key

import (
	"encoding/binary"
)

const (
	logKeySize                  uint64 = 12
	maxIndexKeySize             uint64 = 4
	appliedIndexKeySize         uint64 = 4
	leaderTermStartIndexKeySize uint64 = 12
)

var (
	logKeyHeader                  = [2]byte{0x1, 0x1}
	appliedIndexKey               = [2]byte{0x2, 0x2}
	maxIndexKeyHeader             = [2]byte{0x3, 0x3}
	leaderTermStartIndexKeyHeader = [2]byte{0x4, 0x4}
)

func NewLogKey(index uint64) []byte {
	key := make([]byte, logKeySize)
	key[0] = logKeyHeader[0]
	key[1] = logKeyHeader[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], index)
	return key
}

func NewMaxIndexKey() []byte {
	key := make([]byte, maxIndexKeySize)
	key[0] = maxIndexKeyHeader[0]
	key[1] = maxIndexKeyHeader[1]
	key[2] = 0
	key[3] = 0
	return key
}

func NewLeaderTermStartIndexKey(term uint32) []byte {
	key := make([]byte, leaderTermStartIndexKeySize)
	key[0] = leaderTermStartIndexKeyHeader[0]
	key[1] = leaderTermStartIndexKeyHeader[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint32(key[4:], term)
	return key
}

func GetTermFromLeaderTermStartIndexKey(key []byte) uint32 {
	return binary.BigEndian.Uint32(key[4:])
}

func NewAppliedIndexKey() []byte {
	key := make([]byte, appliedIndexKeySize)
	key[0] = appliedIndexKey[0]
	key[1] = appliedIndexKey[1]
	key[2] = 0
	key[3] = 0
	return key
}
