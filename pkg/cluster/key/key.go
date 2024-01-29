package key

import (
	"encoding/binary"
	"hash/fnv"
)

const (
	logKeySize                  uint64 = 20
	maxIndexKeySize             uint64 = 12
	appliedIndexKeySize         uint64 = 12
	leaderTermStartIndexKeySize uint64 = 16
)

var (
	logKeyHeader                  = [2]byte{0x1, 0x1}
	appliedIndexKey               = [2]byte{0x2, 0x2}
	maxIndexKeyHeader             = [2]byte{0x3, 0x3}
	leaderTermStartIndexKeyHeader = [2]byte{0x4, 0x4}
)

func NewLogKey(shardNo string, index uint64) []byte {
	key := make([]byte, logKeySize)
	shardID := shardNoToShardID(shardNo)
	key[0] = logKeyHeader[0]
	key[1] = logKeyHeader[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], shardID)
	binary.BigEndian.PutUint64(key[12:], index)
	return key
}

func NewMaxIndexKey(shardNo string) []byte {
	key := make([]byte, maxIndexKeySize)
	shardID := shardNoToShardID(shardNo)
	key[0] = maxIndexKeyHeader[0]
	key[1] = maxIndexKeyHeader[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], shardID)
	return key
}

func NewLeaderTermStartIndexKey(shardNo string, term uint32) []byte {
	key := make([]byte, leaderTermStartIndexKeySize)
	shardID := shardNoToShardID(shardNo)
	key[0] = leaderTermStartIndexKeyHeader[0]
	key[1] = leaderTermStartIndexKeyHeader[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], shardID)
	binary.BigEndian.PutUint32(key[12:], term)
	return key
}

func GetTermFromLeaderTermStartIndexKey(key []byte) uint32 {
	return binary.BigEndian.Uint32(key[12:])
}

func NewAppliedIndexKey(shardNo string) []byte {
	key := make([]byte, appliedIndexKeySize)
	shardID := shardNoToShardID(shardNo)
	key[0] = appliedIndexKey[0]
	key[1] = appliedIndexKey[1]
	key[2] = 0
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], shardID)
	return key
}

func shardNoToShardID(shardNo string) uint64 {
	h := fnv.New64a()
	_, err := h.Write([]byte(shardNo))
	if err != nil {
		panic(err)
	}
	return h.Sum64()
}
