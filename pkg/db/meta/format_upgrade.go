package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// systemIDFormatUpgradeProgress is a node-global cursor outside Hash-Slot restore spans.
const systemIDFormatUpgradeProgress uint16 = 7

// format1Entry translates only exact legacy row shapes. The loader includes
// staged users so projecting a legacy UID ban never overwrites credentials.
func format1Entry(key, value []byte, load func([]byte) ([]byte, bool, error)) ([]byte, []byte, []byte, error) {
	if len(key) < 11 || key[0] != byte(keycodec.DomainMeta) || key[1] != byte(keycodec.PartitionHashSlot) || key[4] != byte(keycodec.SpaceRow) {
		return value, nil, nil, nil
	}
	slot := HashSlot(binary.BigEndian.Uint16(key[2:4]))
	table := binary.BigEndian.Uint32(key[5:9])
	switch table {
	case TableIDUser:
		pk, ok := decodeTablePrimaryRowKey(encodeRowPrefix(slot, TableIDUser), key, KeyLayout{KeyString}, userPrimaryFamilyID)
		if !ok {
			return nil, nil, nil, ErrCorruptValue
		}
		_, rest, err := readValueString(value)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(rest) == 16 {
			value = append(bytes.Clone(value), make([]byte, 16)...)
		}
		_, err = decodeUserValue(pk[0].S, value)
		return value, nil, nil, err
	case TableIDChannel:
		pk, ok := decodeTablePrimaryRowKey(encodeRowPrefix(slot, TableIDChannel), key, KeyLayout{KeyString, KeyInt64Ordered}, channelPrimaryFamilyID)
		if !ok {
			return nil, nil, nil, ErrCorruptValue
		}
		legacy := len(value) == 72
		if legacy {
			value = append(bytes.Clone(value), make([]byte, 8)...)
		}
		ch, err := decodeChannelValue(pk[0].S, pk[1].I64, value)
		if err != nil {
			return nil, nil, nil, err
		}
		if !legacy || ch.ChannelType != 1 || ch.SendBan == 0 {
			return value, nil, nil, nil
		}
		userKey := encodeUserRowKey(slot, ch.ChannelID, userPrimaryFamilyID)
		raw, found, err := load(userKey)
		if err != nil {
			return nil, nil, nil, err
		}
		u := User{UID: ch.ChannelID}
		if found {
			raw, _, _, err = format1Entry(userKey, raw, load)
			if err == nil {
				u, err = decodeUserValue(ch.ChannelID, raw)
			}
			if err != nil {
				return nil, nil, nil, err
			}
		}
		u.SendBan = 1
		u.SendBanVersion = 1
		ch.SendBan = 0
		return encodeChannelValue(ch), userKey, encodeUserValue(u), nil
	}
	return value, nil, nil, nil
}

// UpgradeFormat1 rewrites legacy rows in bounded synchronous batches. Its raw
// key cursor commits in the same batch as the converted rows; crashes can retry
// without skipping a row or applying a policy twice. The caller owns the cold
// data generation and a completed backup before invoking this method.
func (db *DB) UpgradeFormat1(ctx context.Context, progress func()) error {
	if db == nil || db.engine == nil || db.engine.IsClosed() {
		return dberrors.ErrClosed
	}
	if ctx == nil {
		return ErrInvalidArgument
	}
	cursorKey := recoverySystemKey(systemIDFormatUpgradeProgress)
	cursor, _, err := db.engine.Get(cursorKey)
	if err != nil {
		return err
	}
	it, err := db.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer it.Close()
	valid := it.First()
	if len(cursor) > 0 {
		valid = it.SeekGE(cursor)
		if valid && bytes.Equal(it.Key(), cursor) {
			valid = it.Next()
		}
	}
	batch := db.engine.NewBatch()
	defer func() { _ = batch.Close() }()
	users := map[string][]byte{}
	count, size := 0, 0
	var last []byte
	load := func(k []byte) ([]byte, bool, error) {
		if v, ok := users[string(k)]; ok {
			return v, true, nil
		}
		return db.engine.Get(k)
	}
	flush := func() error {
		if count == 0 {
			return nil
		}
		if err := batch.Set(cursorKey, last); err != nil {
			return err
		}
		if err := batch.Commit(true); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		batch = db.engine.NewBatch()
		users = map[string][]byte{}
		count, size = 0, 0
		// gofail: var formatUpgradeBatch struct{}
		if progress != nil {
			progress()
		}
		return nil
	}
	for ; valid; valid = it.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := it.Key()
		value, err := it.Value()
		if err != nil {
			return err
		}
		converted, uk, uv, err := format1Entry(key, value, load)
		if err != nil {
			return fmt.Errorf("format 1 metadata conversion: %w", err)
		}
		if !bytes.Equal(converted, value) {
			if err := batch.Set(key, converted); err != nil {
				return err
			}
		}
		if uk != nil {
			if err := batch.Set(uk, uv); err != nil {
				return err
			}
			users[string(uk)] = uv
		}
		// User rows precede Channel rows in each Hash Slot.
		if len(key) > 9 && key[4] == byte(keycodec.SpaceRow) && binary.BigEndian.Uint32(key[5:9]) == TableIDUser {
			users[string(key)] = converted
		}
		last = key
		count++
		size += len(key) + len(converted) + len(uk) + len(uv)
		if count >= 128 || size >= 1<<20 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := it.Error(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	// Independently verify the complete live keyspace, even after a resumed scan.
	verify, err := db.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer verify.Close()
	for valid := verify.First(); valid; valid = verify.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := verify.Key()
		value, err := verify.Value()
		if err != nil {
			return err
		}
		v, uk, _, err := format1Entry(key, value, db.engine.Get)
		if err != nil {
			return err
		}
		if uk != nil || !bytes.Equal(v, value) {
			return fmt.Errorf("incomplete format 1 metadata conversion")
		}
	}
	return verify.Error()
}

// snapshotFormat1 normalizes old snapshot rows before they become live. Each
// stream batch keeps only its own user overlay; earlier chunks are durable.
type snapshotFormat1 struct {
	users map[string][]byte
	load  func([]byte) ([]byte, bool, error)
}

func (s *snapshotFormat1) stage(batch *engine.Batch, key, value []byte) error {
	load := func(k []byte) ([]byte, bool, error) {
		if v, ok := s.users[string(k)]; ok {
			return v, true, nil
		}
		if s.load != nil {
			return s.load(k)
		}
		return nil, false, nil
	}
	v, uk, uv, err := format1Entry(key, value, load)
	if err != nil {
		return err
	}
	if !bytes.Equal(v, value) {
		if err := batch.Set(recoverySystemKey(systemIDFormatUpgradeProgress), nil); err != nil {
			return err
		}
	}
	if s.users == nil {
		s.users = map[string][]byte{}
	}
	if len(key) > 9 && key[4] == byte(keycodec.SpaceRow) && binary.BigEndian.Uint32(key[5:9]) == TableIDUser && !bytes.Equal(v, value) {
		// Current snapshots need no overlay; only old snapshots project UID bans.
		s.users[string(key)] = v
	}
	if uk != nil {
		if err := batch.Set(uk, uv); err != nil {
			return err
		}
		s.users[string(uk)] = uv
	}
	return batch.Set(key, v)
}

// ReplayFormat1ChannelPolicy retains beta.21 policy semantics when a migrated
// node replays pre-upgrade TLV commands. An old non-delete command also opts a
// fresh replica into legacy replay when its initial snapshot was empty.
// The conditional result is evaluated inside apply, after the original command.
func (b *WriteBatch) ReplayFormat1ChannelPolicy(hashSlot uint16, ch Channel, conditional *ChannelConditionalMutationResult, deleted bool) error {
	if err := b.ensure(); err != nil {
		return err
	}
	if err := validateKeyString(ch.ChannelID); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		if conditional != nil && !conditional.Applied {
			return nil
		}
		witness := recoverySystemKey(systemIDFormatUpgradeProgress)
		_, enabled, err := state.db.engine.Get(witness)
		if err != nil {
			return err
		}
		if staged, ok := state.tableRows[string(witness)]; ok {
			enabled = staged.exists
		}
		if !enabled {
			// An old non-delete Channel command is self-identifying. This also
			// covers a fresh replica replaying an old empty snapshot's suffix.
			// Untagged format-2 deletes alone cannot opt into legacy semantics.
			if deleted {
				return nil
			}
			if err := batch.Set(witness, nil); err != nil {
				return err
			}
			state.tableRows[string(witness)] = tableRowOverlay{exists: true}
		}
		if ch.ChannelType == 1 {
			key := encodeUserRowKey(hs, ch.ChannelID, userPrimaryFamilyID)
			u, found, err := state.loadUser(key, ch.ChannelID)
			if err != nil {
				return err
			}
			if !found {
				if ch.SendBan == 0 {
					return nil
				}
				u = User{UID: ch.ChannelID}
			}
			u.SendBan = ch.SendBan
			u.SendBanVersion = uint64(ch.SendBan)
			raw := encodeUserValue(u)
			if err := batch.Set(key, raw); err != nil {
				return err
			}
			state.tableRows[string(key)] = tableRowOverlay{value: raw, exists: true}
		}
		if deleted {
			return nil
		}
		key := encodeChannelRowKey(hs, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
		row, found, err := state.loadChannel(ctx, key, ch.ChannelID, ch.ChannelType)
		if err != nil || !found {
			return err
		}
		row.SendBan = ch.SendBan
		row.SendBanVersion = 0
		if ch.ChannelType == 1 {
			row.SendBan = 0
		}
		if err := (&Shard{db: state.db, hashSlot: hs}).stageChannel(batch, key, row); err != nil {
			return err
		}
		state.channelPublishes[string(key)] = row
		delete(state.channelDeletes, string(key))
		return nil
	})
	return nil
}
