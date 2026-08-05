package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	userCMDChannelMembershipColumnUID              uint16 = 1
	userCMDChannelMembershipColumnCommandChannelID uint16 = 2
	userCMDChannelMembershipColumnChannelType      uint16 = 3
	userCMDChannelMembershipColumnStartSeq         uint16 = 4
	userCMDChannelMembershipColumnAckSeq           uint16 = 5
	userCMDChannelMembershipColumnTombstone        uint16 = 6
	userCMDChannelMembershipColumnTombstoneAt      uint16 = 7
	userCMDChannelMembershipColumnUpdatedAt        uint16 = 8
)

// UserCMDChannelMembership stores UID-owned command-channel discovery and
// acknowledgement state separately from ordinary conversation membership.
type UserCMDChannelMembership struct {
	// UID owns this command-channel directory row and determines its hash slot.
	UID string
	// CommandChannelID is the durable command log identity, including the CMD suffix.
	CommandChannelID string
	// ChannelType separates command-channel namespaces for the same id.
	ChannelType int64
	// StartSeq is the first command sequence visible after the current binding.
	StartSeq uint64
	// AckSeq is the monotonically acknowledged command sequence.
	AckSeq uint64
	// Tombstone excludes this binding from sync while retaining deletion state.
	Tombstone bool
	// TombstoneAt records the latest unbind time in Unix nanoseconds.
	TombstoneAt int64
	// UpdatedAt orders last-writer-wins updates for the binding.
	UpdatedAt int64
}

// UserCMDChannelMembershipCursor identifies the last scanned CMD membership.
type UserCMDChannelMembershipCursor struct {
	// CommandChannelID is the last emitted command channel id.
	CommandChannelID string
	// ChannelType is the last emitted command channel type.
	ChannelType int64
}

var userCMDChannelMembershipTable = registerMetaTable(TableSpec[UserCMDChannelMembership]{
	ID:   TableIDUserCMDChannelMembership,
	Name: "user_cmd_channel_membership",
	Columns: []schema.Column{
		{ID: userCMDChannelMembershipColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: userCMDChannelMembershipColumnCommandChannelID, Name: "command_channel_id", Type: schema.TypeString, Required: true},
		{ID: userCMDChannelMembershipColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: userCMDChannelMembershipColumnStartSeq, Name: "start_seq", Type: schema.TypeUint64},
		{ID: userCMDChannelMembershipColumnAckSeq, Name: "ack_seq", Type: schema.TypeUint64},
		{ID: userCMDChannelMembershipColumnTombstone, Name: "tombstone", Type: schema.TypeBool},
		{ID: userCMDChannelMembershipColumnTombstoneAt, Name: "tombstone_at", Type: schema.TypeInt64},
		{ID: userCMDChannelMembershipColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: userCMDChannelMembershipPrimaryFamilyID, Name: "primary", Columns: []uint16{
		userCMDChannelMembershipColumnStartSeq,
		userCMDChannelMembershipColumnAckSeq,
		userCMDChannelMembershipColumnTombstone,
		userCMDChannelMembershipColumnTombstoneAt,
		userCMDChannelMembershipColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[UserCMDChannelMembership]{
		IndexID:  userCMDChannelMembershipPrimaryIndexID,
		FamilyID: userCMDChannelMembershipPrimaryFamilyID,
		Name:     "pk_user_cmd_channel_membership",
		Columns:  []uint16{userCMDChannelMembershipColumnUID, userCMDChannelMembershipColumnCommandChannelID, userCMDChannelMembershipColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyString, KeyInt64Ordered},
		Key: func(membership UserCMDChannelMembership) KeyParts {
			return userCMDChannelMembershipPrimaryKey(membership.UID, membership.CommandChannelID, membership.ChannelType)
		},
	},
	Validate: validateUserCMDChannelMembership,
	EncodeValue: func(membership UserCMDChannelMembership) ([]byte, error) {
		return encodeUserCMDChannelMembershipValue(membership), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (UserCMDChannelMembership, error) {
		return decodeUserCMDChannelMembershipValue(primary[0].S, primary[1].S, primary[2].I64, value)
	},
})

// UserCMDChannelMembershipTable describes the CMD membership table schema.
var UserCMDChannelMembershipTable = userCMDChannelMembershipTable.Schema()

// GetUserCMDChannelMembership returns one UID-owned CMD membership row.
func (s *Shard) GetUserCMDChannelMembership(ctx context.Context, uid, commandChannelID string, channelType int64) (UserCMDChannelMembership, bool, error) {
	if err := s.check(ctx); err != nil {
		return UserCMDChannelMembership{}, false, err
	}
	if err := validateUserCMDChannelMembershipIdentity(uid, commandChannelID, channelType); err != nil {
		return UserCMDChannelMembership{}, false, err
	}
	return userCMDChannelMembershipTable.Get(ctx, s, userCMDChannelMembershipPrimaryKey(uid, commandChannelID, channelType))
}

// UpsertUserCMDChannelMembership binds a command channel. Rebinding a
// tombstoned row resets its start and acknowledgement boundaries.
func (s *Shard) UpsertUserCMDChannelMembership(ctx context.Context, membership UserCMDChannelMembership) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateUserCMDChannelMembership(membership); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	pk := userCMDChannelMembershipPrimaryKey(membership.UID, membership.CommandChannelID, membership.ChannelType)
	primaryKey, err := userCMDChannelMembershipTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existing, exists, err := userCMDChannelMembershipTable.getByPrimaryKey(s.db, s.hashSlot, pk)
	if err != nil {
		return err
	}
	next := resolveUserCMDChannelMembership(existing, exists, membership)
	if exists && existing == next {
		return nil
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(primaryKey, encodeUserCMDChannelMembershipValue(next)); err != nil {
		return err
	}
	return batch.Commit(true)
}

// AdvanceUserCMDChannelMembershipAckSeq monotonically acknowledges CMD sync.
func (s *Shard) AdvanceUserCMDChannelMembershipAckSeq(ctx context.Context, uid, commandChannelID string, channelType int64, ackSeq uint64, updatedAt int64) error {
	return s.mutateUserCMDChannelMembership(ctx, uid, commandChannelID, channelType, func(row *UserCMDChannelMembership) {
		if ackSeq > row.AckSeq {
			row.AckSeq = ackSeq
		}
		if updatedAt > row.UpdatedAt {
			row.UpdatedAt = updatedAt
		}
	})
}

// TombstoneUserCMDChannelMembership unbinds one command channel.
func (s *Shard) TombstoneUserCMDChannelMembership(ctx context.Context, uid, commandChannelID string, channelType int64, tombstoneAt int64) error {
	return s.mutateUserCMDChannelMembership(ctx, uid, commandChannelID, channelType, func(row *UserCMDChannelMembership) {
		row.Tombstone = true
		if tombstoneAt > row.TombstoneAt {
			row.TombstoneAt = tombstoneAt
		}
		if tombstoneAt > row.UpdatedAt {
			row.UpdatedAt = tombstoneAt
		}
	})
}

func (s *Shard) mutateUserCMDChannelMembership(ctx context.Context, uid, commandChannelID string, channelType int64, mutate func(*UserCMDChannelMembership)) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateUserCMDChannelMembershipIdentity(uid, commandChannelID, channelType); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	pk := userCMDChannelMembershipPrimaryKey(uid, commandChannelID, channelType)
	primaryKey, err := userCMDChannelMembershipTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existing, exists, err := userCMDChannelMembershipTable.getByPrimaryKey(s.db, s.hashSlot, pk)
	if err != nil {
		return err
	}
	if !exists {
		return dberrors.ErrNotFound
	}
	if existing.Tombstone {
		return nil
	}
	next := existing
	mutate(&next)
	if next == existing {
		return nil
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	return commitSet(batch, primaryKey, encodeUserCMDChannelMembershipValue(next))
}

// ListUserCMDChannelMembershipPage returns CMD memberships in stable key order.
func (s *Shard) ListUserCMDChannelMembershipPage(ctx context.Context, uid string, cursor UserCMDChannelMembershipCursor, limit int) ([]UserCMDChannelMembership, UserCMDChannelMembershipCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	if err := validateUID(uid); err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	if cursor != (UserCMDChannelMembershipCursor{}) {
		if err := validateUserCMDChannelMembershipIdentity(uid, cursor.CommandChannelID, cursor.ChannelType); err != nil {
			return nil, UserCMDChannelMembershipCursor{}, false, err
		}
	}
	if err := validatePageLimit(limit); err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	var after KeyParts
	if cursor != (UserCMDChannelMembershipCursor{}) {
		after = userCMDChannelMembershipPrimaryKey(uid, cursor.CommandChannelID, cursor.ChannelType)
	}
	rows, next, done, err := userCMDChannelMembershipTable.scanPrimaryPrefixStrict(ctx, s, KeyParts{String(uid)}, after, limit)
	if err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 3 {
		nextCursor = UserCMDChannelMembershipCursor{CommandChannelID: next[1].S, ChannelType: next[2].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = UserCMDChannelMembershipCursor{CommandChannelID: last.CommandChannelID, ChannelType: last.ChannelType}
	}
	return rows, nextCursor, done, nil
}

// UpsertUserCMDChannelMembership stages one CMD discovery binding.
func (b *Batch) UpsertUserCMDChannelMembership(hashSlot HashSlot, membership UserCMDChannelMembership) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserCMDChannelMembership(membership); err != nil {
		return err
	}
	pk := userCMDChannelMembershipPrimaryKey(membership.UID, membership.CommandChannelID, membership.ChannelType)
	primaryKey, err := userCMDChannelMembershipTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := userCMDChannelMembershipTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		next := resolveUserCMDChannelMembership(existing, exists, membership)
		if exists && next == existing {
			return nil
		}
		value := encodeUserCMDChannelMembershipValue(next)
		if err := batch.Set(primaryKey, value); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

// AdvanceUserCMDChannelMembershipAckSeq stages one monotonic acknowledgement.
func (b *Batch) AdvanceUserCMDChannelMembershipAckSeq(hashSlot HashSlot, membership UserCMDChannelMembership) error {
	return b.mutateUserCMDChannelMembership(hashSlot, membership, func(row *UserCMDChannelMembership) {
		if membership.AckSeq > row.AckSeq {
			row.AckSeq = membership.AckSeq
			if membership.UpdatedAt > row.UpdatedAt {
				row.UpdatedAt = membership.UpdatedAt
			}
		}
	})
}

// TombstoneUserCMDChannelMembership stages one CMD discovery unbind.
func (b *Batch) TombstoneUserCMDChannelMembership(hashSlot HashSlot, membership UserCMDChannelMembership) error {
	return b.mutateUserCMDChannelMembership(hashSlot, membership, func(row *UserCMDChannelMembership) {
		if !row.Tombstone {
			row.Tombstone = true
		}
		if membership.TombstoneAt > row.TombstoneAt {
			row.TombstoneAt = membership.TombstoneAt
		}
		if membership.UpdatedAt > row.UpdatedAt {
			row.UpdatedAt = membership.UpdatedAt
		}
	})
}

func (b *Batch) mutateUserCMDChannelMembership(hashSlot HashSlot, membership UserCMDChannelMembership, mutate func(*UserCMDChannelMembership)) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserCMDChannelMembershipIdentity(membership.UID, membership.CommandChannelID, membership.ChannelType); err != nil {
		return err
	}
	pk := userCMDChannelMembershipPrimaryKey(membership.UID, membership.CommandChannelID, membership.ChannelType)
	primaryKey, err := userCMDChannelMembershipTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := userCMDChannelMembershipTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		if !exists {
			return dberrors.ErrNotFound
		}
		if existing.Tombstone {
			return nil
		}
		next := existing
		mutate(&next)
		if next == existing {
			return nil
		}
		value := encodeUserCMDChannelMembershipValue(next)
		if err := batch.Set(primaryKey, value); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func resolveUserCMDChannelMembership(existing UserCMDChannelMembership, exists bool, next UserCMDChannelMembership) UserCMDChannelMembership {
	if !exists || existing.Tombstone && !next.Tombstone {
		return next
	}
	if existing.Tombstone {
		return existing
	}
	if next.AckSeq > existing.AckSeq {
		existing.AckSeq = next.AckSeq
	}
	if next.UpdatedAt > existing.UpdatedAt {
		existing.UpdatedAt = next.UpdatedAt
	}
	return existing
}

func userCMDChannelMembershipPrimaryKey(uid, commandChannelID string, channelType int64) KeyParts {
	return KeyParts{String(uid), String(commandChannelID), Int64Ordered(channelType)}
}

func validateUserCMDChannelMembership(membership UserCMDChannelMembership) error {
	if err := validateUserCMDChannelMembershipIdentity(membership.UID, membership.CommandChannelID, membership.ChannelType); err != nil {
		return err
	}
	if membership.TombstoneAt < 0 || membership.UpdatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateUserCMDChannelMembershipIdentity(uid, commandChannelID string, channelType int64) error {
	if err := validateUID(uid); err != nil {
		return err
	}
	return validateChannelKey(ChannelKey{ChannelID: commandChannelID, ChannelType: channelType})
}

func encodeUserCMDChannelMembershipValue(membership UserCMDChannelMembership) []byte {
	value := appendValueUint64(nil, membership.StartSeq)
	value = appendValueUint64(value, membership.AckSeq)
	if membership.Tombstone {
		value = append(value, 1)
	} else {
		value = append(value, 0)
	}
	value = appendValueInt64(value, membership.TombstoneAt)
	return appendValueInt64(value, membership.UpdatedAt)
}

func decodeUserCMDChannelMembershipValue(uid, commandChannelID string, channelType int64, value []byte) (UserCMDChannelMembership, error) {
	startSeq, rest, err := readValueUint64(value)
	if err != nil {
		return UserCMDChannelMembership{}, err
	}
	ackSeq, rest, err := readValueUint64(rest)
	if err != nil || len(rest) < 1 || rest[0] > 1 {
		return UserCMDChannelMembership{}, dberrors.ErrCorruptValue
	}
	tombstone := rest[0] == 1
	rest = rest[1:]
	tombstoneAt, rest, err := readValueInt64(rest)
	if err != nil {
		return UserCMDChannelMembership{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil || len(rest) != 0 {
		return UserCMDChannelMembership{}, dberrors.ErrCorruptValue
	}
	return UserCMDChannelMembership{
		UID: uid, CommandChannelID: commandChannelID, ChannelType: channelType,
		StartSeq: startSeq, AckSeq: ackSeq, Tombstone: tombstone,
		TombstoneAt: tombstoneAt, UpdatedAt: updatedAt,
	}, nil
}
