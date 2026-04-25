package meta

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	"github.com/cockroachdb/pebble/v2"
)

var snapshotMagic = [4]byte{'W', 'K', 'C', 'S'}

const snapshotVersion uint16 = 1

type snapshotEntry struct {
	Key   []byte
	Value []byte
}

func (s *Store) ExportSnapshot(ctx context.Context) ([]byte, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := s.checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ensureOpenLocked(); err != nil {
		return nil, err
	}

	entries, err := s.collectSnapshotEntriesLocked(ctx)
	if err != nil {
		return nil, err
	}
	return encodeSnapshot(entries), nil
}

func (s *Store) ImportSnapshot(ctx context.Context, data []byte) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	entries, err := decodeSnapshot(data)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.checkContext(ctx); err != nil {
			return err
		}
		if err := validateSnapshotValue(entry.Key, entry.Value); err != nil {
			return err
		}
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return err
	}

	batch := s.db.NewBatch()
	defer batch.Close()

	for _, prefix := range []byte{
		recordPrefixNode,
		recordPrefixMembership,
		recordPrefixHashSlot,
		recordPrefixAssignment,
		recordPrefixRuntimeView,
		recordPrefixTask,
	} {
		if err := s.checkContext(ctx); err != nil {
			return err
		}
		lowerBound, upperBound := prefixBounds(prefix)
		if err := batch.DeleteRange(lowerBound, upperBound, nil); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if err := s.checkContext(ctx); err != nil {
			return err
		}
		if err := batch.Set(entry.Key, entry.Value, nil); err != nil {
			return err
		}
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *Store) collectSnapshotEntriesLocked(ctx context.Context) ([]snapshotEntry, error) {
	var entries []snapshotEntry

	for _, prefix := range []byte{
		recordPrefixMembership,
		recordPrefixHashSlot,
		recordPrefixNode,
		recordPrefixAssignment,
		recordPrefixRuntimeView,
		recordPrefixTask,
	} {
		lowerBound, upperBound := prefixBounds(prefix)
		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: lowerBound,
			UpperBound: upperBound,
		})
		if err != nil {
			return nil, err
		}

		for ok := iter.First(); ok; ok = iter.Next() {
			if err := s.checkContext(ctx); err != nil {
				iter.Close()
				return nil, err
			}

			value, err := iter.ValueAndErr()
			if err != nil {
				iter.Close()
				return nil, err
			}
			if err := validateSnapshotKey(iter.Key()); err != nil {
				iter.Close()
				return nil, err
			}
			if err := validateSnapshotValue(iter.Key(), value); err != nil {
				iter.Close()
				return nil, err
			}
			entries = append(entries, snapshotEntry{
				Key:   append([]byte(nil), iter.Key()...),
				Value: append([]byte(nil), value...),
			})
		}
		if err := iter.Error(); err != nil {
			iter.Close()
			return nil, err
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	return entries, nil
}

func encodeSnapshot(entries []snapshotEntry) []byte {
	data := make([]byte, 0, 32)
	data = append(data, snapshotMagic[:]...)
	data = binary.BigEndian.AppendUint16(data, snapshotVersion)
	data = binary.BigEndian.AppendUint64(data, uint64(len(entries)))

	for _, entry := range entries {
		data = binary.AppendUvarint(data, uint64(len(entry.Key)))
		data = binary.AppendUvarint(data, uint64(len(entry.Value)))
		data = append(data, entry.Key...)
		data = append(data, entry.Value...)
	}

	sum := crc32.ChecksumIEEE(data)
	return binary.BigEndian.AppendUint32(data, sum)
}

func decodeSnapshot(data []byte) ([]snapshotEntry, error) {
	if len(data) < len(snapshotMagic)+2+8+4 {
		return nil, ErrCorruptValue
	}

	body := data[:len(data)-4]
	want := binary.BigEndian.Uint32(data[len(data)-4:])
	if got := crc32.ChecksumIEEE(body); got != want {
		return nil, ErrChecksumMismatch
	}

	if string(body[:len(snapshotMagic)]) != string(snapshotMagic[:]) {
		return nil, ErrCorruptValue
	}
	body = body[len(snapshotMagic):]

	version := binary.BigEndian.Uint16(body[:2])
	if version != snapshotVersion {
		return nil, fmt.Errorf("%w: unknown snapshot version %d", ErrCorruptValue, version)
	}
	body = body[2:]

	entryCount := binary.BigEndian.Uint64(body[:8])
	body = body[8:]
	if entryCount > uint64(len(body))/2 {
		return nil, ErrCorruptValue
	}

	var (
		entries []snapshotEntry
		seen    = make(map[string]struct{})
	)
	for i := uint64(0); i < entryCount; i++ {
		keyLen, n := binary.Uvarint(body)
		if n <= 0 {
			return nil, fmt.Errorf("decode snapshot key length %d: %w", i, ErrCorruptValue)
		}
		body = body[n:]

		valueLen, n := binary.Uvarint(body)
		if n <= 0 {
			return nil, fmt.Errorf("decode snapshot value length %d: %w", i, ErrCorruptValue)
		}
		body = body[n:]

		if keyLen > uint64(len(body)) {
			return nil, fmt.Errorf("decode snapshot key %d: %w", i, ErrCorruptValue)
		}
		key := append([]byte(nil), body[:int(keyLen)]...)
		body = body[int(keyLen):]

		if valueLen > uint64(len(body)) {
			return nil, fmt.Errorf("decode snapshot value %d: %w", i, ErrCorruptValue)
		}
		value := append([]byte(nil), body[:int(valueLen)]...)
		body = body[int(valueLen):]

		if err := validateSnapshotKey(key); err != nil {
			return nil, fmt.Errorf("validate snapshot key %d: %w", i, err)
		}
		keyID := string(key)
		if _, ok := seen[keyID]; ok {
			return nil, ErrCorruptValue
		}
		seen[keyID] = struct{}{}
		entries = append(entries, snapshotEntry{
			Key:   key,
			Value: value,
		})
	}

	if len(body) != 0 {
		return nil, ErrCorruptValue
	}
	return entries, nil
}

func validateSnapshotValue(key, value []byte) error {
	if len(key) == 0 {
		return ErrCorruptValue
	}
	switch key[0] {
	case recordPrefixNode:
		_, err := decodeClusterNode(key, value)
		return err
	case recordPrefixMembership:
		_, err := decodeControllerMembership(value)
		return err
	case recordPrefixAssignment:
		_, err := decodeGroupAssignment(key, value)
		return err
	case recordPrefixHashSlot:
		_, err := hashslot.DecodeHashSlotTable(value)
		return err
	case recordPrefixRuntimeView:
		_, err := decodeGroupRuntimeView(key, value)
		return err
	case recordPrefixTask:
		_, err := decodeReconcileTask(key, value)
		return err
	default:
		return ErrCorruptValue
	}
}
