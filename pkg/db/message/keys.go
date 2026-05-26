package message

import (
	"bytes"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func channelPartitionID(key ChannelKey) []byte {
	return keycodec.AppendString(nil, string(key))
}

func encodeMessageRowPrefix(channelKey ChannelKey) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Row(TableIDMessage).
		Key()
}

func encodeMessageRowKey(channelKey ChannelKey, seq uint64, familyID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Row(TableIDMessage).
		Uint64(seq).
		Family(familyID).
		Key()
}

func decodeMessageRowKey(channelKey ChannelKey, key []byte) (seq uint64, familyID uint16, ok bool) {
	prefix := encodeMessageRowPrefix(channelKey)
	if !bytes.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	rest := key[len(prefix):]
	if len(rest) != 10 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint64(rest[:8]), binary.BigEndian.Uint16(rest[8:]), true
}

func encodeMessageIndexPrefix(channelKey ChannelKey, indexID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Index(TableIDMessage, indexID).
		Key()
}

func encodeMessageIDIndexKey(channelKey ChannelKey, messageID uint64) []byte {
	key := encodeMessageIndexPrefix(channelKey, messageIndexIDMessageID)
	return keycodec.AppendUint64(key, messageID)
}

func encodeMessageClientMsgNoIndexPrefix(channelKey ChannelKey, clientMsgNo string) []byte {
	key := encodeMessageIndexPrefix(channelKey, messageIndexIDClientMsgNo)
	return keycodec.AppendString(key, clientMsgNo)
}

func encodeMessageClientMsgNoIndexKey(channelKey ChannelKey, clientMsgNo string, seq uint64) []byte {
	key := encodeMessageClientMsgNoIndexPrefix(channelKey, clientMsgNo)
	return keycodec.AppendUint64(key, seq)
}

func decodeMessageClientMsgNoIndexSeq(channelKey ChannelKey, clientMsgNo string, key []byte) (uint64, bool) {
	prefix := encodeMessageClientMsgNoIndexPrefix(channelKey, clientMsgNo)
	if !bytes.HasPrefix(key, prefix) {
		return 0, false
	}
	rest := key[len(prefix):]
	if len(rest) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(rest), true
}

func encodeMessageIdempotencyIndexKey(channelKey ChannelKey, fromUID string, clientMsgNo string) []byte {
	key := encodeMessageIndexPrefix(channelKey, messageIndexIDFromUIDClientMsgNo)
	key = keycodec.AppendString(key, fromUID)
	return keycodec.AppendString(key, clientMsgNo)
}

func encodeMessageSystemPrefix(channelKey ChannelKey, systemID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		System(TableIDMessage, systemID).
		Key()
}

func encodeRetentionStateKey(channelKey ChannelKey) []byte {
	return encodeMessageSystemPrefix(channelKey, messageSystemIDRetention)
}

func encodeCommittedCursorKey(channelKey ChannelKey, name string) []byte {
	key := encodeMessageSystemPrefix(channelKey, messageSystemIDCursor)
	return keycodec.AppendString(key, name)
}

func encodeCheckpointKey(channelKey ChannelKey) []byte {
	return encodeMessageSystemPrefix(channelKey, messageSystemIDCheckpoint)
}

func encodeHistoryPrefix(channelKey ChannelKey) []byte {
	return encodeMessageSystemPrefix(channelKey, messageSystemIDHistory)
}

func encodeHistoryOffsetKey(channelKey ChannelKey, startOffset uint64) []byte {
	key := encodeHistoryPrefix(channelKey)
	return keycodec.AppendUint64(key, startOffset)
}

func encodeHistoryPointKey(channelKey ChannelKey, point EpochPoint) []byte {
	key := encodeHistoryOffsetKey(channelKey, point.StartOffset)
	return keycodec.AppendUint64(key, point.Epoch)
}

func encodeSnapshotKey(channelKey ChannelKey) []byte {
	return encodeMessageSystemPrefix(channelKey, messageSystemIDSnapshot)
}

func encodeCatalogPrefix() []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionGlobal, nil).
		Catalog().
		Key()
}

func encodeCatalogKey(channelKey ChannelKey) []byte {
	key := encodeCatalogPrefix()
	return keycodec.AppendString(key, string(channelKey))
}

func decodeCatalogKey(key []byte) (ChannelKey, bool) {
	prefix := encodeCatalogPrefix()
	if !bytes.HasPrefix(key, prefix) {
		return "", false
	}
	value, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 0 {
		return "", false
	}
	return ChannelKey(value), true
}
