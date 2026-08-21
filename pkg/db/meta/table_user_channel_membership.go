package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	userChannelMembershipColumnUID         uint16 = 1
	userChannelMembershipColumnChannelID   uint16 = 2
	userChannelMembershipColumnChannelType uint16 = 3
	userChannelMembershipColumnJoinSeq     uint16 = 4
	userChannelMembershipColumnReadSeq     uint16 = 5
	userChannelMembershipColumnDeletedSeq  uint16 = 6
	userChannelMembershipColumnActivatedAt uint16 = 7
	userChannelMembershipColumnTombstone   uint16 = 8
	userChannelMembershipColumnTombstoneAt uint16 = 9
	userChannelMembershipColumnSourceVer   uint16 = 10
	userChannelMembershipColumnUpdatedAt   uint16 = 11
)

// UserChannelMembership stores one UID-owned channel membership row.
type UserChannelMembership struct {
	// UID identifies the user that owns this membership row.
	UID string
	// ChannelID identifies the joined channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// JoinSeq is the first channel sequence visible to this membership.
	JoinSeq uint64
	// ReadSeq is a monotonic badge baseline, not a message-read receipt.
	ReadSeq uint64
	// DeletedToSeq hides ordinary messages through this sequence.
	DeletedToSeq uint64
	// ActivatedAt prioritizes directory synchronization after explicit user activity.
	ActivatedAt int64
	// Tombstone records that the user left or was removed from the channel.
	Tombstone bool
	// TombstoneAt records when the membership was removed.
	TombstoneAt int64
	// SourceVersion fences stale cross-Slot subscriber mutations.
	SourceVersion uint64
	// UpdatedAt records the latest membership mutation timestamp.
	UpdatedAt int64
}

// UserChannelMembershipCursor identifies the last emitted user membership row.
type UserChannelMembershipCursor struct {
	// ActivatedAt is the activation timestamp from the last scanned index row.
	ActivatedAt int64
	// ChannelID is the last emitted channel ID.
	ChannelID string
	// ChannelType is the last emitted channel type.
	ChannelType int64
}

var userChannelMembershipTable = registerMetaTable(TableSpec[UserChannelMembership]{
	ID:   TableIDUserChannelMembership,
	Name: "user_channel_membership",
	Columns: []schema.Column{
		{ID: userChannelMembershipColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: userChannelMembershipColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: userChannelMembershipColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: userChannelMembershipColumnJoinSeq, Name: "join_seq", Type: schema.TypeUint64},
		{ID: userChannelMembershipColumnReadSeq, Name: "read_seq", Type: schema.TypeUint64},
		{ID: userChannelMembershipColumnDeletedSeq, Name: "deleted_to_seq", Type: schema.TypeUint64},
		{ID: userChannelMembershipColumnActivatedAt, Name: "activated_at", Type: schema.TypeInt64},
		{ID: userChannelMembershipColumnTombstone, Name: "tombstone", Type: schema.TypeBool},
		{ID: userChannelMembershipColumnTombstoneAt, Name: "tombstone_at", Type: schema.TypeInt64},
		{ID: userChannelMembershipColumnSourceVer, Name: "source_version", Type: schema.TypeUint64},
		{ID: userChannelMembershipColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: userChannelMembershipPrimaryFamilyID, Name: "primary", Columns: []uint16{
		userChannelMembershipColumnJoinSeq,
		userChannelMembershipColumnReadSeq,
		userChannelMembershipColumnDeletedSeq,
		userChannelMembershipColumnActivatedAt,
		userChannelMembershipColumnTombstone,
		userChannelMembershipColumnTombstoneAt,
		userChannelMembershipColumnSourceVer,
		userChannelMembershipColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[UserChannelMembership]{
		IndexID:  userChannelMembershipPrimaryIndexID,
		FamilyID: userChannelMembershipPrimaryFamilyID,
		Name:     "pk_user_channel_membership",
		Columns:  []uint16{userChannelMembershipColumnUID, userChannelMembershipColumnChannelID, userChannelMembershipColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyString, KeyInt64Ordered},
		Key: func(membership UserChannelMembership) KeyParts {
			return userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType)
		},
	},
	Indexes: []IndexSpec[UserChannelMembership]{
		{
			ID:      userChannelMembershipActivationIndexID,
			Name:    "idx_user_channel_membership_activation",
			Columns: []uint16{userChannelMembershipColumnUID, userChannelMembershipColumnActivatedAt, userChannelMembershipColumnChannelID, userChannelMembershipColumnChannelType},
			Layout:  KeyLayout{KeyString, KeyInt64Desc, KeyString, KeyInt64Ordered},
			Key: func(membership UserChannelMembership) (KeyParts, bool) {
				return userChannelMembershipActivationKey(membership), true
			},
			PrimaryKeyFromIndexParts: userChannelMembershipPrimaryFromActivationIndex,
			CorruptIndexKeyIsError:   true,
		},
	},
	Validate: validateUserChannelMembership,
	EncodeValue: func(membership UserChannelMembership) ([]byte, error) {
		return encodeUserChannelMembershipValue(membership), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (UserChannelMembership, error) {
		return decodeUserChannelMembershipValue(primary[0].S, primary[1].S, primary[2].I64, value)
	},
})

// UserChannelMembershipTable describes the UID-owned channel membership table schema.
var UserChannelMembershipTable = userChannelMembershipTable.Schema()

// GetUserChannelMembership returns one UID-owned channel membership row.
func (s *Shard) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (UserChannelMembership, bool, error) {
	if err := s.check(ctx); err != nil {
		return UserChannelMembership{}, false, err
	}
	if err := validateUserChannelMembershipIdentity(uid, channelID, channelType); err != nil {
		return UserChannelMembership{}, false, err
	}
	return userChannelMembershipTable.Get(ctx, s, userChannelMembershipPrimaryKey(uid, channelID, channelType))
}

// UpsertUserChannelMembership stores one membership with monotonic join metadata.
func (s *Shard) UpsertUserChannelMembership(ctx context.Context, membership UserChannelMembership) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateUserChannelMembership(membership); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := userChannelMembershipRowKey(s.hashSlot, membership.UID, membership.ChannelID, membership.ChannelType)
	if err != nil {
		return err
	}
	existing, exists, err := userChannelMembershipTable.getByPrimaryKey(s.db, s.hashSlot, userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType))
	if err != nil {
		return err
	}
	next := resolveUserChannelMembership(existing, exists, membership)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if existing == next && exists {
		return nil
	}
	if err := stageUserChannelMembership(batch, s.hashSlot, primaryKey, existing, exists, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// EnsureUserChannelMembership creates one membership when absent and advances
// only its source-generation fence when a newer directory incarnation arrives.
// Structural and user-owned state, including tombstones, is never overwritten.
func (s *Shard) EnsureUserChannelMembership(ctx context.Context, membership UserChannelMembership) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateUserChannelMembership(membership); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := userChannelMembershipRowKey(s.hashSlot, membership.UID, membership.ChannelID, membership.ChannelType)
	if err != nil {
		return err
	}
	existing, exists, err := userChannelMembershipTable.getByPrimaryKey(s.db, s.hashSlot, userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType))
	if err != nil {
		return err
	}
	next := resolveEnsuredUserChannelMembership(existing, exists, membership)
	if exists && next == existing {
		return nil
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := stageUserChannelMembership(batch, s.hashSlot, primaryKey, existing, exists, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// SetUserChannelMembershipActivatedAt changes directory priority for a live
// membership. A tombstone ignores personal-state commands.
func (s *Shard) SetUserChannelMembershipActivatedAt(ctx context.Context, uid string, key ChannelKey, activatedAt, updatedAt int64) error {
	if activatedAt < 0 || updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return s.mutateUserChannelMembership(ctx, uid, key, func(row *UserChannelMembership) {
		if activatedAt > row.ActivatedAt {
			row.ActivatedAt = activatedAt
			if updatedAt > row.UpdatedAt {
				row.UpdatedAt = updatedAt
			}
		}
	})
}

// AdvanceUserChannelMembershipReadSeq monotonically advances the badge floor.
func (s *Shard) AdvanceUserChannelMembershipReadSeq(ctx context.Context, uid string, key ChannelKey, readSeq uint64, updatedAt int64) error {
	if updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return s.mutateUserChannelMembership(ctx, uid, key, func(row *UserChannelMembership) {
		if readSeq > row.ReadSeq {
			row.ReadSeq = readSeq
			if updatedAt > row.UpdatedAt {
				row.UpdatedAt = updatedAt
			}
		}
	})
}

// HideUserChannelMembership advances the local visibility floor and clears
// directory activation without removing channel membership.
func (s *Shard) HideUserChannelMembership(ctx context.Context, uid string, key ChannelKey, deletedToSeq uint64, updatedAt int64) error {
	if updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return s.mutateUserChannelMembership(ctx, uid, key, func(row *UserChannelMembership) {
		changed := false
		if deletedToSeq > row.DeletedToSeq {
			row.DeletedToSeq = deletedToSeq
			changed = true
		}
		if row.ActivatedAt != 0 {
			row.ActivatedAt = 0
			changed = true
		}
		if changed && updatedAt > row.UpdatedAt {
			row.UpdatedAt = updatedAt
		}
	})
}

func (s *Shard) mutateUserChannelMembership(ctx context.Context, uid string, key ChannelKey, mutate func(*UserChannelMembership)) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	pk := userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType)
	primaryKey, err := userChannelMembershipTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existing, exists, err := userChannelMembershipTable.getByPrimaryKey(s.db, s.hashSlot, pk)
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
	if err := stageUserChannelMembership(batch, s.hashSlot, primaryKey, existing, true, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// DeleteUserChannelMembership removes one UID-owned channel membership row.
func (s *Shard) DeleteUserChannelMembership(ctx context.Context, uid string, key ChannelKey) error {
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	return userChannelMembershipTable.Delete(ctx, s, userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType))
}

// ListUserChannelMembershipPage returns UID memberships in activation-priority order.
func (s *Shard) ListUserChannelMembershipPage(ctx context.Context, uid string, cursor UserChannelMembershipCursor, limit int) ([]UserChannelMembership, UserChannelMembershipCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validateUID(uid); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validateUserChannelMembershipCursor(cursor); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validatePageLimit(limit); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	var after KeyParts
	if cursor != (UserChannelMembershipCursor{}) {
		after = KeyParts{String(uid), Int64Desc(cursor.ActivatedAt), String(cursor.ChannelID), Int64Ordered(cursor.ChannelType)}
	}
	rows, next, done, err := userChannelMembershipTable.ScanIndex(ctx, s, userChannelMembershipActivationIndexID, KeyParts{String(uid)}, after, limit)
	if err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 4 {
		nextCursor = UserChannelMembershipCursor{ActivatedAt: next[1].I64, ChannelID: next[2].S, ChannelType: next[3].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = UserChannelMembershipCursor{ActivatedAt: last.ActivatedAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, nextCursor, done, nil
}

func (b *Batch) UpsertUserChannelMembership(hashSlot HashSlot, membership UserChannelMembership) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserChannelMembership(membership); err != nil {
		return err
	}
	pk := userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType)
	primaryKey, err := userChannelMembershipTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := userChannelMembershipTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		next := resolveUserChannelMembership(existing, exists, membership)
		if existing == next && exists {
			return nil
		}
		if err := stageUserChannelMembership(batch, hashSlot, primaryKey, existing, exists, next); err != nil {
			return err
		}
		value := encodeUserChannelMembershipValue(next)
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

// EnsureUserChannelMembership stages a create-or-fence-advance projection.
// A newer directory incarnation replaces all source-derived sequence floors
// while preserving activation and tombstone state owned by the user-facing
// directory. Exact replacement prevents a delayed older projector from
// poisoning a delete/recreate incarnation with its prior tail.
func (b *Batch) EnsureUserChannelMembership(hashSlot HashSlot, membership UserChannelMembership) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserChannelMembership(membership); err != nil {
		return err
	}
	pk := userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType)
	primaryKey, err := userChannelMembershipTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := userChannelMembershipTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		next := resolveEnsuredUserChannelMembership(existing, exists, membership)
		if exists && next == existing {
			return nil
		}
		if err := stageUserChannelMembership(batch, hashSlot, primaryKey, existing, exists, next); err != nil {
			return err
		}
		value := encodeUserChannelMembershipValue(next)
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func resolveEnsuredUserChannelMembership(existing UserChannelMembership, exists bool, incoming UserChannelMembership) UserChannelMembership {
	if !exists {
		return incoming
	}
	if incoming.SourceVersion <= existing.SourceVersion {
		return existing
	}
	existing.JoinSeq = incoming.JoinSeq
	if existing.SourceVersion == 0 {
		// An unfenced row may contain user-owned floors established before the
		// first source projection; importing generation one must not regress it.
		if incoming.ReadSeq > existing.ReadSeq {
			existing.ReadSeq = incoming.ReadSeq
		}
		if incoming.DeletedToSeq > existing.DeletedToSeq {
			existing.DeletedToSeq = incoming.DeletedToSeq
		}
	} else {
		// A later source generation is a true delete/recreate boundary.
		existing.ReadSeq = incoming.ReadSeq
		existing.DeletedToSeq = incoming.DeletedToSeq
	}
	existing.SourceVersion = incoming.SourceVersion
	if incoming.UpdatedAt > existing.UpdatedAt {
		existing.UpdatedAt = incoming.UpdatedAt
	}
	return existing
}

// AdvanceUserChannelMembershipReadSeq stages one monotonic badge-floor update.
func (b *Batch) AdvanceUserChannelMembershipReadSeq(hashSlot HashSlot, uid string, key ChannelKey, readSeq uint64, updatedAt int64) error {
	if updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return b.mutateUserChannelMembership(hashSlot, uid, key, func(row *UserChannelMembership) {
		if readSeq > row.ReadSeq {
			row.ReadSeq = readSeq
			if updatedAt > row.UpdatedAt {
				row.UpdatedAt = updatedAt
			}
		}
	})
}

// ActivateUserChannelMembership stages one monotonic directory-priority update.
func (b *Batch) ActivateUserChannelMembership(hashSlot HashSlot, uid string, key ChannelKey, activatedAt, updatedAt int64) error {
	if activatedAt <= 0 || updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return b.mutateUserChannelMembership(hashSlot, uid, key, func(row *UserChannelMembership) {
		if activatedAt > row.ActivatedAt {
			row.ActivatedAt = activatedAt
			if updatedAt > row.UpdatedAt {
				row.UpdatedAt = updatedAt
			}
		}
	})
}

// HideUserChannelMembership stages a monotonic visibility floor and clears
// activation without deleting channel membership.
func (b *Batch) HideUserChannelMembership(hashSlot HashSlot, uid string, key ChannelKey, deletedToSeq uint64, updatedAt int64) error {
	if updatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return b.mutateUserChannelMembership(hashSlot, uid, key, func(row *UserChannelMembership) {
		changed := false
		if deletedToSeq > row.DeletedToSeq {
			row.DeletedToSeq = deletedToSeq
			changed = true
		}
		if row.ActivatedAt != 0 {
			row.ActivatedAt = 0
			changed = true
		}
		if changed && updatedAt > row.UpdatedAt {
			row.UpdatedAt = updatedAt
		}
	})
}

func (b *Batch) mutateUserChannelMembership(hashSlot HashSlot, uid string, key ChannelKey, mutate func(*UserChannelMembership)) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	pk := userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType)
	primaryKey, err := userChannelMembershipTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := userChannelMembershipTable.loadBatchRow(state, hashSlot, pk, primaryKey)
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
		if err := stageUserChannelMembership(batch, hashSlot, primaryKey, existing, true, next); err != nil {
			return err
		}
		value := encodeUserChannelMembershipValue(next)
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func (b *Batch) DeleteUserChannelMembership(hashSlot HashSlot, uid string, key ChannelKey) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	return userChannelMembershipTable.StageDelete(b, hashSlot, userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType))
}

func stageUserChannelMembership(batch *engine.Batch, hashSlot HashSlot, primaryKey []byte, existing UserChannelMembership, exists bool, membership UserChannelMembership) error {
	pk := userChannelMembershipPrimaryKey(membership.UID, membership.ChannelID, membership.ChannelType)
	if exists {
		if err := userChannelMembershipTable.stageDeleteIndexEntries(batch, hashSlot, existing, pk); err != nil {
			return err
		}
	}
	value := encodeUserChannelMembershipValue(membership)
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	return userChannelMembershipTable.stagePutIndexEntries(batch, hashSlot, membership, pk, value)
}

func resolveUserChannelMembership(existing UserChannelMembership, exists bool, next UserChannelMembership) UserChannelMembership {
	if !exists {
		return next
	}
	if next.SourceVersion < existing.SourceVersion {
		return existing
	}
	if next.SourceVersion == existing.SourceVersion {
		if !existing.Tombstone && next.Tombstone {
			return existing
		}
		if existing.Tombstone && !next.Tombstone {
			existing.Tombstone = false
			existing.TombstoneAt = 0
			if next.UpdatedAt > existing.UpdatedAt {
				existing.UpdatedAt = next.UpdatedAt
			}
		}
		return existing
	}
	if next.Tombstone {
		existing.Tombstone = true
		existing.TombstoneAt = next.TombstoneAt
		existing.SourceVersion = next.SourceVersion
		if next.UpdatedAt > existing.UpdatedAt {
			existing.UpdatedAt = next.UpdatedAt
		}
		return existing
	}
	if existing.Tombstone {
		return next
	}
	existing.SourceVersion = next.SourceVersion
	if next.UpdatedAt > existing.UpdatedAt {
		existing.UpdatedAt = next.UpdatedAt
	}
	return existing
}

func userChannelMembershipActivationKey(membership UserChannelMembership) KeyParts {
	return KeyParts{String(membership.UID), Int64Desc(membership.ActivatedAt), String(membership.ChannelID), Int64Ordered(membership.ChannelType)}
}

func userChannelMembershipPrimaryFromActivationIndex(parts KeyParts) (KeyParts, bool) {
	if len(parts) != 4 {
		return nil, false
	}
	return KeyParts{parts[0], parts[2], parts[3]}, true
}

func userChannelMembershipPrimaryKey(uid, channelID string, channelType int64) KeyParts {
	return KeyParts{String(uid), String(channelID), Int64Ordered(channelType)}
}

func userChannelMembershipRowKey(hashSlot HashSlot, uid, channelID string, channelType int64) ([]byte, error) {
	return userChannelMembershipTable.primaryRowKey(hashSlot, userChannelMembershipPrimaryKey(uid, channelID, channelType))
}

func validateUserChannelMembership(membership UserChannelMembership) error {
	return validateUserChannelMembershipIdentity(membership.UID, membership.ChannelID, membership.ChannelType)
}

func validateUserChannelMembershipIdentity(uid, channelID string, channelType int64) error {
	if err := validateUID(uid); err != nil {
		return err
	}
	return validateChannelKey(ChannelKey{ChannelID: channelID, ChannelType: channelType})
}

func validateUserChannelMembershipCursor(cursor UserChannelMembershipCursor) error {
	if cursor == (UserChannelMembershipCursor{}) {
		return nil
	}
	if cursor.ChannelID == "" {
		return dberrors.ErrInvalidArgument
	}
	if cursor.ActivatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	return validateChannelKey(ChannelKey{ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
}

func encodeUserChannelMembershipValue(membership UserChannelMembership) []byte {
	value := appendValueUint64(nil, membership.JoinSeq)
	value = appendValueUint64(value, membership.ReadSeq)
	value = appendValueUint64(value, membership.DeletedToSeq)
	value = appendValueInt64(value, membership.ActivatedAt)
	if membership.Tombstone {
		value = append(value, 1)
	} else {
		value = append(value, 0)
	}
	value = appendValueInt64(value, membership.TombstoneAt)
	value = appendValueUint64(value, membership.SourceVersion)
	return appendValueInt64(value, membership.UpdatedAt)
}

func decodeUserChannelMembershipValue(uid, channelID string, channelType int64, value []byte) (UserChannelMembership, error) {
	joinSeq, rest, err := readValueUint64(value)
	if err != nil {
		return UserChannelMembership{}, err
	}
	readSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return UserChannelMembership{}, err
	}
	deletedToSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return UserChannelMembership{}, err
	}
	activatedAt, rest, err := readValueInt64(rest)
	if err != nil || len(rest) < 1 || rest[0] > 1 {
		return UserChannelMembership{}, dberrors.ErrCorruptValue
	}
	tombstone := rest[0] == 1
	rest = rest[1:]
	tombstoneAt, rest, err := readValueInt64(rest)
	if err != nil {
		return UserChannelMembership{}, err
	}
	sourceVersion, rest, err := readValueUint64(rest)
	if err != nil {
		return UserChannelMembership{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return UserChannelMembership{}, err
	}
	if len(rest) != 0 {
		return UserChannelMembership{}, dberrors.ErrCorruptValue
	}
	return UserChannelMembership{
		UID:           uid,
		ChannelID:     channelID,
		ChannelType:   channelType,
		JoinSeq:       joinSeq,
		ReadSeq:       readSeq,
		DeletedToSeq:  deletedToSeq,
		ActivatedAt:   activatedAt,
		Tombstone:     tombstone,
		TombstoneAt:   tombstoneAt,
		SourceVersion: sourceVersion,
		UpdatedAt:     updatedAt,
	}, nil
}
