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
	userChannelMembershipColumnValue       uint16 = 4
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
	// UpdatedAt records the latest membership mutation timestamp.
	UpdatedAt int64
}

// UserChannelMembershipCursor identifies the last emitted user membership row.
type UserChannelMembershipCursor struct {
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
		{ID: userChannelMembershipColumnValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: userChannelMembershipPrimaryFamilyID, Name: "primary", Columns: []uint16{userChannelMembershipColumnValue}}},
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
	if err := stageUserChannelMembership(batch, primaryKey, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// DeleteUserChannelMembership removes one UID-owned channel membership row.
func (s *Shard) DeleteUserChannelMembership(ctx context.Context, uid string, key ConversationKey) error {
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	return userChannelMembershipTable.Delete(ctx, s, userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType))
}

// ListUserChannelMembershipPage returns UID memberships in stable channel-key order.
func (s *Shard) ListUserChannelMembershipPage(ctx context.Context, uid string, cursor UserChannelMembershipCursor, limit int) ([]UserChannelMembership, UserChannelMembershipCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validateUserChannelMembershipCursor(cursor); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	var after KeyParts
	if cursor != (UserChannelMembershipCursor{}) {
		after = userChannelMembershipPrimaryKey(uid, cursor.ChannelID, cursor.ChannelType)
	}
	rows, next, done, err := userChannelMembershipTable.scanPrimaryPrefixStrict(ctx, s, KeyParts{String(uid)}, after, limit)
	if err != nil {
		return nil, UserChannelMembershipCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 3 {
		nextCursor = UserChannelMembershipCursor{ChannelID: next[1].S, ChannelType: next[2].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = UserChannelMembershipCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
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
		if err := stageUserChannelMembership(batch, primaryKey, next); err != nil {
			return err
		}
		value := encodeUserChannelMembershipValue(next)
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func (b *Batch) DeleteUserChannelMembership(hashSlot HashSlot, uid string, key ConversationKey) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateUserChannelMembershipIdentity(uid, key.ChannelID, key.ChannelType); err != nil {
		return err
	}
	return userChannelMembershipTable.StageDelete(b, hashSlot, userChannelMembershipPrimaryKey(uid, key.ChannelID, key.ChannelType))
}

func stageUserChannelMembership(batch *engine.Batch, primaryKey []byte, membership UserChannelMembership) error {
	return batch.Set(primaryKey, encodeUserChannelMembershipValue(membership))
}

func resolveUserChannelMembership(existing UserChannelMembership, exists bool, next UserChannelMembership) UserChannelMembership {
	if !exists {
		return next
	}
	if next.JoinSeq < existing.JoinSeq {
		next.JoinSeq = existing.JoinSeq
	}
	if next.UpdatedAt < existing.UpdatedAt {
		next.UpdatedAt = existing.UpdatedAt
	}
	return next
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
	if err := validateConversationUID(uid); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType})
}

func validateUserChannelMembershipCursor(cursor UserChannelMembershipCursor) error {
	if cursor == (UserChannelMembershipCursor{}) {
		return nil
	}
	if cursor.ChannelID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validateConversationKey(ConversationKey{ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
}

func encodeUserChannelMembershipValue(membership UserChannelMembership) []byte {
	value := appendValueUint64(nil, membership.JoinSeq)
	return appendValueInt64(value, membership.UpdatedAt)
}

func decodeUserChannelMembershipValue(uid, channelID string, channelType int64, value []byte) (UserChannelMembership, error) {
	joinSeq, rest, err := readValueUint64(value)
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
		UID:         uid,
		ChannelID:   channelID,
		ChannelType: channelType,
		JoinSeq:     joinSeq,
		UpdatedAt:   updatedAt,
	}, nil
}
