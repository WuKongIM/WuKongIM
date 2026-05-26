package meta

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

var slotSnapshotMagic = [4]byte{'W', 'K', 'D', 'B'}

const slotSnapshotVersion uint16 = 1

// SlotSnapshot is a portable hash-slot metadata snapshot.
type SlotSnapshot struct {
	// HashSlots lists the hash slots included in Data.
	HashSlots []uint16
	// Data is the encoded key/value payload.
	Data []byte
	// Stats summarizes the encoded payload.
	Stats SnapshotStats
}

// SnapshotStats summarizes encoded snapshot entries.
type SnapshotStats struct {
	// EntryCount is the number of key/value entries in the snapshot.
	EntryCount int
	// Bytes is the encoded snapshot size.
	Bytes int
}

type snapshotEntry struct {
	Key   []byte
	Value []byte
}

type decodedSlotSnapshot struct {
	HashSlots []uint16
	Entries   []snapshotEntry
	Stats     SnapshotStats
}

type slotSnapshotMeta struct {
	HashSlots []uint16
	Stats     SnapshotStats
}

// ExportHashSlotSnapshot exports all row, index, and system data for hashSlots.
func (db *MetaDB) ExportHashSlotSnapshot(ctx context.Context, hashSlots []uint16) (SlotSnapshot, error) {
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return SlotSnapshot{}, err
	}
	if err := checkSnapshotDB(ctx, db); err != nil {
		return SlotSnapshot{}, err
	}
	unlock := db.lockHashSlots(normalized)
	defer unlock()

	data, countPos := beginSlotSnapshotPayload(normalized)
	entryCount := 0
	for _, hashSlot := range normalized {
		for _, span := range hashSlotAllDataSpans(hashSlot) {
			iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
			if err != nil {
				return SlotSnapshot{}, err
			}
			for ok := iter.First(); ok; ok = iter.Next() {
				if err := contextErr(ctx); err != nil {
					_ = iter.Close()
					return SlotSnapshot{}, err
				}
				value, err := iter.Value()
				if err != nil {
					_ = iter.Close()
					return SlotSnapshot{}, err
				}
				data = appendSlotSnapshotEntry(data, iter.Key(), value)
				entryCount++
			}
			if err := iter.Error(); err != nil {
				_ = iter.Close()
				return SlotSnapshot{}, err
			}
			if err := iter.Close(); err != nil {
				return SlotSnapshot{}, err
			}
		}
	}
	data, stats := finishSlotSnapshotPayload(data, countPos, entryCount)
	return SlotSnapshot{HashSlots: uint16HashSlots(normalized), Data: data, Stats: stats}, nil
}

// ImportHashSlotSnapshot replaces hash-slot data with snap.
func (db *MetaDB) ImportHashSlotSnapshot(ctx context.Context, snap SlotSnapshot) error {
	return db.importHashSlotSnapshot(ctx, snap, false)
}

// ImportHashSlotSnapshotPreservingMigrationMeta imports while preserving local migration metadata.
func (db *MetaDB) ImportHashSlotSnapshotPreservingMigrationMeta(ctx context.Context, snap SlotSnapshot) error {
	return db.importHashSlotSnapshot(ctx, snap, true)
}

// DeleteHashSlotData removes all metadata for one hash slot.
func (db *MetaDB) DeleteHashSlotData(ctx context.Context, hashSlot uint16) error {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return err
	}
	slot := HashSlot(hashSlot)
	unlock := db.lockHashSlots([]HashSlot{slot})
	defer unlock()

	batch := db.engine.NewBatch()
	defer batch.Close()
	for _, span := range hashSlotAllDataSpans(slot) {
		if err := batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
			return err
		}
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	db.clearChannelCache()
	return nil
}

func (db *MetaDB) importHashSlotSnapshot(ctx context.Context, snap SlotSnapshot, preserveMigrationMeta bool) error {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return err
	}
	normalized, err := normalizeSnapshotHashSlots(snap.HashSlots)
	if err != nil {
		return err
	}
	decoded, err := decodeSlotSnapshotPayload(snap.Data)
	if err != nil {
		return err
	}
	if !equalUint16HashSlots(decoded.HashSlots, snap.HashSlots) || !equalUint16HashSlots(decoded.HashSlots, uint16HashSlots(normalized)) {
		return dberrors.ErrInvalidArgument
	}
	unlock := db.lockHashSlots(normalized)
	defer unlock()

	batch := db.engine.NewBatch()
	defer batch.Close()
	for _, hashSlot := range normalized {
		for _, span := range hashSlotSnapshotReplaceSpans(hashSlot, preserveMigrationMeta) {
			if err := batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
				return err
			}
		}
	}
	for _, entry := range decoded.Entries {
		if err := db.stageSlotSnapshotEntry(batch, entry, normalized, preserveMigrationMeta); err != nil {
			return err
		}
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	db.clearChannelCache()
	return nil
}

func checkSnapshotDB(ctx context.Context, db *MetaDB) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if db == nil || db.engine == nil {
		return dberrors.ErrClosed
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func normalizeSnapshotHashSlots(hashSlots []uint16) ([]HashSlot, error) {
	if len(hashSlots) == 0 {
		return nil, dberrors.ErrInvalidArgument
	}
	slots := make([]HashSlot, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		slots = append(slots, HashSlot(hashSlot))
	}
	return orderedHashSlots(slots), nil
}

func uint16HashSlots(hashSlots []HashSlot) []uint16 {
	out := make([]uint16, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		out = append(out, uint16(hashSlot))
	}
	return out
}

func equalUint16HashSlots(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hashSlotSnapshotReplaceSpans(hashSlot HashSlot, preserveMigrationMeta bool) []Span {
	if !preserveMigrationMeta {
		return hashSlotAllDataSpans(hashSlot)
	}
	tables := defaultMetaRegistry.rowTablesForSnapshot(true)
	spans := make([]Span, 0, len(tables)+2)
	for _, table := range tables {
		spans = append(spans, prefixSpan(encodeRowPrefix(hashSlot, table.ID)))
	}
	spans = append(spans, hashSlotIndexSpan(hashSlot), hashSlotSystemSpan(hashSlot))
	return spans
}

func hashSlotAllDataSpans(hashSlot HashSlot) []Span {
	return []Span{
		hashSlotRowSpan(hashSlot),
		hashSlotIndexSpan(hashSlot),
		hashSlotSystemSpan(hashSlot),
	}
}

func (db *MetaDB) stageSlotSnapshotEntry(batch *engine.Batch, entry snapshotEntry, hashSlots []HashSlot, preserveMigrationMeta bool) error {
	if !snapshotEntryInHashSlots(entry.Key, hashSlots) {
		return dberrors.ErrInvalidArgument
	}
	if preserveMigrationMeta && isHashSlotMigrationSnapshotKey(entry.Key, hashSlots) {
		if _, ok, err := db.get(entry.Key); err != nil || ok {
			return err
		}
	}
	return batch.Set(entry.Key, entry.Value)
}

func snapshotEntryInHashSlots(key []byte, hashSlots []HashSlot) bool {
	for _, hashSlot := range hashSlots {
		for _, span := range hashSlotAllDataSpans(hashSlot) {
			if bytesInSpan(key, span) {
				return true
			}
		}
	}
	return false
}

func isHashSlotMigrationSnapshotKey(key []byte, hashSlots []HashSlot) bool {
	for _, hashSlot := range hashSlots {
		if bytesHasPrefix(key, encodeHashSlotMigrationRowPrefix(hashSlot)) {
			return true
		}
	}
	return false
}

func bytesInSpan(key []byte, span Span) bool {
	return bytesCompare(key, span.Start) >= 0 && bytesCompare(key, span.End) < 0
}

func bytesHasPrefix(key, prefix []byte) bool {
	return len(key) >= len(prefix) && bytesCompare(key[:len(prefix)], prefix) == 0
}

func bytesCompare(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func beginSlotSnapshotPayload(hashSlots []HashSlot) ([]byte, int) {
	data := make([]byte, 0, 32)
	data = append(data, slotSnapshotMagic[:]...)
	data = binary.BigEndian.AppendUint16(data, slotSnapshotVersion)
	data = binary.BigEndian.AppendUint16(data, uint16(len(hashSlots)))
	for _, hashSlot := range hashSlots {
		data = binary.BigEndian.AppendUint16(data, uint16(hashSlot))
	}
	countPos := len(data)
	data = binary.BigEndian.AppendUint64(data, 0)
	return data, countPos
}

func appendSlotSnapshotEntry(data []byte, key, value []byte) []byte {
	data = binary.AppendUvarint(data, uint64(len(key)))
	data = binary.AppendUvarint(data, uint64(len(value)))
	data = append(data, key...)
	return append(data, value...)
}

func finishSlotSnapshotPayload(data []byte, countPos int, entryCount int) ([]byte, SnapshotStats) {
	binary.BigEndian.PutUint64(data[countPos:countPos+8], uint64(entryCount))
	sum := crc32.ChecksumIEEE(data)
	data = binary.BigEndian.AppendUint32(data, sum)
	return data, SnapshotStats{EntryCount: entryCount, Bytes: len(data)}
}

func decodeSlotSnapshotPayload(data []byte) (decodedSlotSnapshot, error) {
	meta, body, err := parseSlotSnapshotPayload(data)
	if err != nil {
		return decodedSlotSnapshot{}, err
	}
	entries := make([]snapshotEntry, 0, meta.Stats.EntryCount)
	if err := visitParsedSlotSnapshotPayload(meta, body, func(key, value []byte) error {
		entries = append(entries, snapshotEntry{Key: append([]byte(nil), key...), Value: append([]byte(nil), value...)})
		return nil
	}); err != nil {
		return decodedSlotSnapshot{}, err
	}
	return decodedSlotSnapshot{HashSlots: append([]uint16(nil), meta.HashSlots...), Entries: entries, Stats: meta.Stats}, nil
}

func parseSlotSnapshotPayload(data []byte) (slotSnapshotMeta, []byte, error) {
	const minHeaderSize = 4 + 2 + 2 + 8 + 4
	if len(data) < minHeaderSize {
		return slotSnapshotMeta{}, nil, dberrors.ErrCorruptValue
	}
	body := data[:len(data)-4]
	want := binary.BigEndian.Uint32(data[len(data)-4:])
	if got := crc32.ChecksumIEEE(body); got != want {
		return slotSnapshotMeta{}, nil, dberrors.ErrChecksumMismatch
	}
	if string(body[:len(slotSnapshotMagic)]) != string(slotSnapshotMagic[:]) {
		return slotSnapshotMeta{}, nil, dberrors.ErrCorruptValue
	}
	body = body[len(slotSnapshotMagic):]
	version := binary.BigEndian.Uint16(body[:2])
	if version != slotSnapshotVersion {
		return slotSnapshotMeta{}, nil, fmt.Errorf("%w: unknown snapshot version %d", dberrors.ErrCorruptValue, version)
	}
	body = body[2:]
	hashSlotCount := int(binary.BigEndian.Uint16(body[:2]))
	body = body[2:]
	if len(body) < hashSlotCount*2+8 {
		return slotSnapshotMeta{}, nil, dberrors.ErrCorruptValue
	}
	hashSlots := make([]uint16, hashSlotCount)
	for i := 0; i < hashSlotCount; i++ {
		hashSlots[i] = binary.BigEndian.Uint16(body[:2])
		body = body[2:]
	}
	entryCount := binary.BigEndian.Uint64(body[:8])
	body = body[8:]
	return slotSnapshotMeta{HashSlots: hashSlots, Stats: SnapshotStats{EntryCount: int(entryCount), Bytes: len(data)}}, body, nil
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
			return dberrors.ErrCorruptValue
		}
		body = body[n:]
		valueLen, n := binary.Uvarint(body)
		if n <= 0 {
			return dberrors.ErrCorruptValue
		}
		body = body[n:]
		if uint64(len(body)) < keyLen+valueLen {
			return dberrors.ErrCorruptValue
		}
		keyEnd := int(keyLen)
		valueEnd := keyEnd + int(valueLen)
		if err := fn(body[:keyEnd], body[keyEnd:valueEnd]); err != nil {
			return err
		}
		body = body[valueEnd:]
	}
	if len(body) != 0 {
		return dberrors.ErrCorruptValue
	}
	return nil
}

func encodeSlotSnapshotPayload(hashSlots []uint16, entries []snapshotEntry) ([]byte, SnapshotStats) {
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return nil, SnapshotStats{}
	}
	data, countPos := beginSlotSnapshotPayload(normalized)
	for _, entry := range entries {
		data = appendSlotSnapshotEntry(data, entry.Key, entry.Value)
	}
	return finishSlotSnapshotPayload(data, countPos, len(entries))
}
