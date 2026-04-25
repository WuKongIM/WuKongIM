package store

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const stateSnapshotVersion byte = 2

type stateSnapshotEntry struct {
	FromUID     string
	ClientMsgNo string
	Entry       channel.IdempotencyEntry
	PayloadHash uint64
}

func encodeCheckpoint(checkpoint channel.Checkpoint) []byte {
	value := make([]byte, 0, 24)
	value = binary.BigEndian.AppendUint64(value, checkpoint.Epoch)
	value = binary.BigEndian.AppendUint64(value, checkpoint.LogStartOffset)
	value = binary.BigEndian.AppendUint64(value, checkpoint.HW)
	return value
}

func decodeCheckpoint(value []byte) (channel.Checkpoint, error) {
	if len(value) != 24 {
		return channel.Checkpoint{}, channel.ErrCorruptValue
	}
	return channel.Checkpoint{
		Epoch:          binary.BigEndian.Uint64(value[0:8]),
		LogStartOffset: binary.BigEndian.Uint64(value[8:16]),
		HW:             binary.BigEndian.Uint64(value[16:24]),
	}, nil
}

func encodeEpochPoint(point channel.EpochPoint) []byte {
	value := make([]byte, 0, 16)
	value = binary.BigEndian.AppendUint64(value, point.Epoch)
	value = binary.BigEndian.AppendUint64(value, point.StartOffset)
	return value
}

func decodeEpochPoint(value []byte) (channel.EpochPoint, error) {
	if len(value) != 16 {
		return channel.EpochPoint{}, channel.ErrCorruptValue
	}
	return channel.EpochPoint{
		Epoch:       binary.BigEndian.Uint64(value[0:8]),
		StartOffset: binary.BigEndian.Uint64(value[8:16]),
	}, nil
}

func encodeIndexedIdempotencyEntryValue(entry channel.IdempotencyEntry, payloadHash uint64) []byte {
	value := make([]byte, 0, 24)
	value = binary.BigEndian.AppendUint64(value, entry.MessageSeq)
	value = binary.BigEndian.AppendUint64(value, entry.MessageID)
	value = binary.BigEndian.AppendUint64(value, payloadHash)
	return value
}

func decodeIndexedIdempotencyEntryValue(value []byte) (channel.IdempotencyEntry, uint64, error) {
	if len(value) != 24 {
		return channel.IdempotencyEntry{}, 0, channel.ErrCorruptValue
	}
	messageSeq := binary.BigEndian.Uint64(value[0:8])
	if messageSeq == 0 {
		return channel.IdempotencyEntry{}, 0, channel.ErrCorruptValue
	}
	return channel.IdempotencyEntry{
		MessageID:  binary.BigEndian.Uint64(value[8:16]),
		MessageSeq: messageSeq,
		Offset:     messageSeq - 1,
	}, binary.BigEndian.Uint64(value[16:24]), nil
}

func encodeStateSnapshot(entries []stateSnapshotEntry) []byte {
	payload := make([]byte, 0, 1+binary.MaxVarintLen64+len(entries)*64)
	payload = append(payload, stateSnapshotVersion)
	payload = binary.AppendUvarint(payload, uint64(len(entries)))
	for _, entry := range entries {
		payload = appendKeyString(payload, entry.FromUID)
		payload = appendKeyString(payload, entry.ClientMsgNo)
		payload = append(payload, encodeIndexedIdempotencyEntryValue(entry.Entry, entry.PayloadHash)...)
	}
	return payload
}

func decodeStateSnapshot(payload []byte) ([]stateSnapshotEntry, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if payload[0] != stateSnapshotVersion {
		return nil, channel.ErrCorruptValue
	}
	payload = payload[1:]

	count, n := binary.Uvarint(payload)
	if n <= 0 {
		return nil, channel.ErrCorruptValue
	}
	payload = payload[n:]

	entries := make([]stateSnapshotEntry, 0, int(count))
	for i := uint64(0); i < count; i++ {
		fromUID, rest, err := decodeKeyString(payload)
		if err != nil {
			return nil, err
		}
		clientMsgNo, rest, err := decodeKeyString(rest)
		if err != nil {
			return nil, err
		}
		if len(rest) < 24 {
			return nil, channel.ErrCorruptValue
		}
		entry, payloadHash, err := decodeIndexedIdempotencyEntryValue(rest[:24])
		if err != nil {
			return nil, err
		}
		entries = append(entries, stateSnapshotEntry{
			FromUID:     fromUID,
			ClientMsgNo: clientMsgNo,
			Entry:       entry,
			PayloadHash: payloadHash,
		})
		payload = rest[24:]
	}
	if len(payload) != 0 {
		return nil, channel.ErrCorruptValue
	}
	return entries, nil
}
