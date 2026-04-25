package meta

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

var slotSnapshotMagic = [4]byte{'W', 'K', 'S', 'P'}

const slotSnapshotVersion uint16 = 2

type SlotSnapshot struct {
	// Deprecated: legacy single-slot compatibility during the transition.
	SlotID uint64

	HashSlots []uint16
	Data      []byte
	Stats     SnapshotStats
}

type SnapshotStats struct {
	EntryCount int
	Bytes      int
}

type snapshotEntry struct {
	Key   []byte
	Value []byte
}

type decodedSlotSnapshot struct {
	SlotID    uint64
	HashSlots []uint16
	Entries   []snapshotEntry
	Stats     SnapshotStats
}

type slotSnapshotMeta struct {
	SlotID    uint64
	HashSlots []uint16
	Stats     SnapshotStats
}

func encodeSlotSnapshotPayload(hashSlotsOrSlotID any, entries []snapshotEntry) ([]byte, SnapshotStats) {
	hashSlots, err := normalizeSnapshotHashSlots(hashSlotsOrSlotID)
	if err != nil {
		return nil, SnapshotStats{}
	}

	data := make([]byte, 0, 32)
	data = append(data, slotSnapshotMagic[:]...)
	data = binary.BigEndian.AppendUint16(data, slotSnapshotVersion)
	data = binary.BigEndian.AppendUint16(data, uint16(len(hashSlots)))
	for _, hashSlot := range hashSlots {
		data = binary.BigEndian.AppendUint16(data, hashSlot)
	}
	data = binary.BigEndian.AppendUint64(data, uint64(len(entries)))

	for _, entry := range entries {
		data = binary.AppendUvarint(data, uint64(len(entry.Key)))
		data = binary.AppendUvarint(data, uint64(len(entry.Value)))
		data = append(data, entry.Key...)
		data = append(data, entry.Value...)
	}

	sum := crc32.ChecksumIEEE(data)
	data = binary.BigEndian.AppendUint32(data, sum)
	return data, SnapshotStats{
		EntryCount: len(entries),
		Bytes:      len(data),
	}
}

func beginSlotSnapshotPayload(hashSlots []uint16) ([]byte, int) {
	data := make([]byte, 0, 32)
	data = append(data, slotSnapshotMagic[:]...)
	data = binary.BigEndian.AppendUint16(data, slotSnapshotVersion)
	data = binary.BigEndian.AppendUint16(data, uint16(len(hashSlots)))
	for _, hashSlot := range hashSlots {
		data = binary.BigEndian.AppendUint16(data, hashSlot)
	}
	countPos := len(data)
	data = binary.BigEndian.AppendUint64(data, 0)
	return data, countPos
}

func appendSlotSnapshotEntry(data []byte, key, value []byte) []byte {
	data = binary.AppendUvarint(data, uint64(len(key)))
	data = binary.AppendUvarint(data, uint64(len(value)))
	data = append(data, key...)
	data = append(data, value...)
	return data
}

func finishSlotSnapshotPayload(data []byte, countPos int, entryCount int) ([]byte, SnapshotStats) {
	binary.BigEndian.PutUint64(data[countPos:countPos+8], uint64(entryCount))
	sum := crc32.ChecksumIEEE(data)
	data = binary.BigEndian.AppendUint32(data, sum)
	return data, SnapshotStats{
		EntryCount: entryCount,
		Bytes:      len(data),
	}
}

func decodeSlotSnapshotPayload(data []byte) (decodedSlotSnapshot, error) {
	meta, body, err := parseSlotSnapshotPayload(data)
	if err != nil {
		return decodedSlotSnapshot{}, err
	}

	entries := make([]snapshotEntry, 0, meta.Stats.EntryCount)
	if err := visitParsedSlotSnapshotPayload(meta, body, func(key, value []byte) error {
		entries = append(entries, snapshotEntry{
			Key:   append([]byte(nil), key...),
			Value: append([]byte(nil), value...),
		})
		return nil
	}); err != nil {
		return decodedSlotSnapshot{}, err
	}

	return decodedSlotSnapshot{
		SlotID:    meta.SlotID,
		HashSlots: append([]uint16(nil), meta.HashSlots...),
		Entries:   entries,
		Stats:     meta.Stats,
	}, nil
}

func readSlotSnapshotMeta(data []byte) (slotSnapshotMeta, error) {
	meta, _, err := parseSlotSnapshotPayload(data)
	if err != nil {
		return slotSnapshotMeta{}, err
	}
	return meta, nil
}

func parseSlotSnapshotPayload(data []byte) (slotSnapshotMeta, []byte, error) {
	const minHeaderSize = 4 + 2 + 2 + 8 + 4
	if len(data) < minHeaderSize {
		return slotSnapshotMeta{}, nil, ErrCorruptValue
	}

	body := data[:len(data)-4]
	want := binary.BigEndian.Uint32(data[len(data)-4:])
	if got := crc32.ChecksumIEEE(body); got != want {
		return slotSnapshotMeta{}, nil, ErrChecksumMismatch
	}

	if string(body[:len(slotSnapshotMagic)]) != string(slotSnapshotMagic[:]) {
		return slotSnapshotMeta{}, nil, ErrCorruptValue
	}
	body = body[len(slotSnapshotMagic):]

	version := binary.BigEndian.Uint16(body[:2])
	if version != slotSnapshotVersion {
		return slotSnapshotMeta{}, nil, fmt.Errorf("%w: unknown snapshot version %d", ErrCorruptValue, version)
	}
	body = body[2:]

	hashSlotCount := int(binary.BigEndian.Uint16(body[:2]))
	body = body[2:]
	if len(body) < hashSlotCount*2+8 {
		return slotSnapshotMeta{}, nil, ErrCorruptValue
	}

	hashSlots := make([]uint16, hashSlotCount)
	for i := 0; i < hashSlotCount; i++ {
		hashSlots[i] = binary.BigEndian.Uint16(body[:2])
		body = body[2:]
	}

	entryCount := binary.BigEndian.Uint64(body[:8])
	body = body[8:]

	meta := slotSnapshotMeta{
		HashSlots: hashSlots,
		Stats: SnapshotStats{
			EntryCount: int(entryCount),
			Bytes:      len(data),
		},
	}
	if len(hashSlots) == 1 {
		meta.SlotID = uint64(hashSlots[0])
	}
	return meta, body, nil
}

func visitSlotSnapshotPayload(data []byte, fn func(key, value []byte) error) (slotSnapshotMeta, error) {
	meta, body, err := parseSlotSnapshotPayload(data)
	if err != nil {
		return slotSnapshotMeta{}, err
	}
	if err := visitParsedSlotSnapshotPayload(meta, body, fn); err != nil {
		return slotSnapshotMeta{}, err
	}
	return meta, nil
}

func visitParsedSlotSnapshotPayload(meta slotSnapshotMeta, body []byte, fn func(key, value []byte) error) error {
	for i := 0; i < meta.Stats.EntryCount; i++ {
		keyLen, n := binary.Uvarint(body)
		if n <= 0 {
			return ErrCorruptValue
		}
		body = body[n:]

		valueLen, n := binary.Uvarint(body)
		if n <= 0 {
			return ErrCorruptValue
		}
		body = body[n:]

		if uint64(len(body)) < keyLen+valueLen {
			return ErrCorruptValue
		}

		keyEnd := int(keyLen)
		valueEnd := keyEnd + int(valueLen)
		if err := fn(body[:keyEnd], body[keyEnd:valueEnd]); err != nil {
			return err
		}
		body = body[valueEnd:]
	}

	if len(body) != 0 {
		return ErrCorruptValue
	}
	return nil
}

func normalizeSnapshotHashSlots(v any) ([]uint16, error) {
	switch value := v.(type) {
	case []uint16:
		if len(value) == 0 {
			return nil, ErrInvalidArgument
		}
		out := make([]uint16, len(value))
		copy(out, value)
		return out, nil
	case uint16:
		return []uint16{value}, nil
	case uint64:
		if err := validateSlot(value); err != nil {
			return nil, err
		}
		return []uint16{uint16(value)}, nil
	case int:
		if value <= 0 || value > 1<<16-1 {
			return nil, ErrInvalidArgument
		}
		return []uint16{uint16(value)}, nil
	default:
		return nil, ErrInvalidArgument
	}
}
