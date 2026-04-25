package store

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	channelSystemTableID      uint32 = TableIDMessage
	channelSystemIDCheckpoint uint16 = 1
	channelSystemIDHistory    uint16 = 2
	channelSystemIDSnapshot   uint16 = 3
)

func encodeCheckpointKey(channelKey channel.ChannelKey) []byte {
	return encodeTableSystemKey(channelKey, channelSystemTableID, channelSystemIDCheckpoint)
}

func encodeHistoryPrefix(channelKey channel.ChannelKey) []byte {
	return encodeTableSystemPrefix(channelKey, channelSystemTableID, channelSystemIDHistory)
}

func encodeHistoryKey(channelKey channel.ChannelKey, startOffset uint64) []byte {
	key := encodeHistoryPrefix(channelKey)
	return binary.BigEndian.AppendUint64(key, startOffset)
}

func encodeSnapshotKey(channelKey channel.ChannelKey) []byte {
	return encodeTableSystemKey(channelKey, channelSystemTableID, channelSystemIDSnapshot)
}

func encodeIdempotencyIndexPrefix(channelKey channel.ChannelKey) []byte {
	return encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDFromUIDClientMsgNo)
}

func encodeIdempotencyIndexKey(channelKey channel.ChannelKey, key channel.IdempotencyKey) []byte {
	return encodeMessageIdempotencyIndexKey(channelKey, key.FromUID, key.ClientMsgNo)
}

func decodeIdempotencyIndexKey(raw []byte, prefix []byte) (channel.IdempotencyKey, error) {
	if len(raw) < len(prefix) || string(raw[:len(prefix)]) != string(prefix) {
		return channel.IdempotencyKey{}, channel.ErrCorruptValue
	}

	rest := raw[len(prefix):]
	fromUID, rest, err := decodeKeyString(rest)
	if err != nil {
		return channel.IdempotencyKey{}, err
	}
	clientMsgNo, rest, err := decodeKeyString(rest)
	if err != nil {
		return channel.IdempotencyKey{}, err
	}
	if len(rest) != 0 {
		return channel.IdempotencyKey{}, channel.ErrCorruptValue
	}
	return channel.IdempotencyKey{FromUID: fromUID, ClientMsgNo: clientMsgNo}, nil
}
