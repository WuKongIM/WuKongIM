package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

var (
	// ErrInvalidArgument reports invalid caller input.
	ErrInvalidArgument = dberrors.ErrInvalidArgument
	// ErrNotFound reports missing metadata rows.
	ErrNotFound = dberrors.ErrNotFound
	// ErrAlreadyExists reports create-only conflicts.
	ErrAlreadyExists = dberrors.ErrAlreadyExists
	// ErrChecksumMismatch reports snapshot or value checksum mismatches.
	ErrChecksumMismatch = dberrors.ErrChecksumMismatch
	// ErrCorruptValue reports malformed encoded metadata.
	ErrCorruptValue = dberrors.ErrCorruptValue
	// ErrStaleMeta reports stale metadata guards and monotonic conflicts.
	ErrStaleMeta = dberrors.ErrConflict
)

// UserCursor identifies the last emitted user in a shard page scan.
type UserCursor struct {
	UID string
}

// ChannelCursor identifies the last emitted channel in a shard page scan.
type ChannelCursor struct {
	ChannelID   string
	ChannelType int64
}

// Subscriber stores one durable channel subscriber.
type Subscriber struct {
	ChannelID   string
	ChannelType int64
	UID         string
}

// UserConversationActiveHint describes a hot conversation activity hint.
type UserConversationActiveHint struct {
	UID         string
	ChannelID   string
	ChannelType int64
	ActiveAt    int64
	MessageSeq  uint64
}

// UserConversationDeleteBarrier prevents stale hints from reactivating deletes.
type UserConversationDeleteBarrier struct {
	UID          string
	ChannelID    string
	ChannelType  int64
	DeletedToSeq uint64
}

// DB is the compatibility handle used by slot FSM and proxy callers.
type DB struct {
	meta   *MetaDB
	engine *engine.DB
}

// Open opens a metadata DB at path.
func Open(path string) (*DB, error) {
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		return nil, err
	}
	return &DB{meta: NewDB(eng), engine: eng}, nil
}

// Close closes the metadata DB.
func (db *DB) Close() error {
	if db == nil || db.engine == nil {
		return nil
	}
	eng := db.engine
	db.engine = nil
	db.meta = nil
	return eng.Close()
}

// MetaDB returns the typed metadata handle.
func (db *DB) MetaDB() *MetaDB {
	if db == nil {
		return nil
	}
	return db.meta
}

// ForSlot returns a shard handle for legacy single-slot callers.
func (db *DB) ForSlot(slot uint64) *ShardStore {
	if slot > math.MaxUint16 {
		return &ShardStore{err: ErrInvalidArgument}
	}
	return db.ForHashSlot(uint16(slot))
}

// ForHashSlot returns a shard handle for hashSlot.
func (db *DB) ForHashSlot(hashSlot uint16) *ShardStore {
	if db == nil || db.meta == nil {
		return &ShardStore{err: dberrors.ErrClosed}
	}
	return &ShardStore{db: db, shard: db.meta.HashSlot(HashSlot(hashSlot)), hashSlot: HashSlot(hashSlot)}
}

// ForHashSlots returns shard handles for hashSlots.
func (db *DB) ForHashSlots(hashSlots []uint16) []*ShardStore {
	shards := make([]*ShardStore, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		shards = append(shards, db.ForHashSlot(hashSlot))
	}
	return shards
}

// NewWriteBatch creates a compatibility write batch.
func (db *DB) NewWriteBatch() *WriteBatch {
	if db == nil || db.meta == nil {
		return &WriteBatch{err: dberrors.ErrClosed}
	}
	return &WriteBatch{db: db, batch: db.meta.NewBatch(), migrationCreates: make(map[string]ChannelMigrationTask)}
}

// DeleteSlotData removes all data for a legacy single-slot hash slot.
func (db *DB) DeleteSlotData(ctx context.Context, slotID uint64) error {
	if slotID > math.MaxUint16 {
		return ErrInvalidArgument
	}
	return db.DeleteHashSlotData(ctx, uint16(slotID))
}

// DeleteHashSlotData removes all data for hashSlot.
func (db *DB) DeleteHashSlotData(ctx context.Context, hashSlot uint16) error {
	if db == nil || db.meta == nil {
		return dberrors.ErrClosed
	}
	return db.meta.DeleteHashSlotData(ctx, hashSlot)
}

// ExportHashSlotSnapshot exports selected hash slots.
func (db *DB) ExportHashSlotSnapshot(ctx context.Context, hashSlots []uint16) (SlotSnapshot, error) {
	if db == nil || db.meta == nil {
		return SlotSnapshot{}, dberrors.ErrClosed
	}
	return db.meta.ExportHashSlotSnapshot(ctx, hashSlots)
}

// ImportHashSlotSnapshot imports selected hash slots.
func (db *DB) ImportHashSlotSnapshot(ctx context.Context, snap SlotSnapshot) error {
	if db == nil || db.meta == nil {
		return dberrors.ErrClosed
	}
	return db.meta.ImportHashSlotSnapshot(ctx, snap)
}

// ImportHashSlotSnapshotPreservingMigrationMeta imports while preserving local migration rows.
func (db *DB) ImportHashSlotSnapshotPreservingMigrationMeta(ctx context.Context, snap SlotSnapshot) error {
	if db == nil || db.meta == nil {
		return dberrors.ErrClosed
	}
	return db.meta.ImportHashSlotSnapshotPreservingMigrationMeta(ctx, snap)
}

// ShardStore is the compatibility hash-slot scoped API.
type ShardStore struct {
	db       *DB
	shard    *Shard
	hashSlot HashSlot
	err      error
}

func (s *ShardStore) validate() error {
	if s == nil {
		return dberrors.ErrClosed
	}
	if s.err != nil {
		return s.err
	}
	if s.shard == nil {
		return dberrors.ErrClosed
	}
	return nil
}

// HashSlot returns the shard hash slot.
func (s *ShardStore) HashSlot() uint16 {
	if s == nil {
		return 0
	}
	return uint16(s.hashSlot)
}

func (s *ShardStore) CreateUser(ctx context.Context, user User) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.CreateUser(ctx, user)
}

func (s *ShardStore) UpsertUser(ctx context.Context, user User) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertUser(ctx, user)
}

func (s *ShardStore) UpdateUser(ctx context.Context, user User) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpdateUser(ctx, user)
}

func (s *ShardStore) DeleteUser(ctx context.Context, uid string) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteUser(ctx, uid)
}

func (s *ShardStore) GetUser(ctx context.Context, uid string) (User, error) {
	if err := s.validate(); err != nil {
		return User{}, err
	}
	user, ok, err := s.shard.GetUser(ctx, uid)
	return user, foundError(ok, err)
}

func (s *ShardStore) ListUsersPage(ctx context.Context, after UserCursor, limit int) ([]User, UserCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, UserCursor{}, false, err
	}
	users, cursor, done, err := s.shard.ListUsersPage(ctx, after.UID, limit)
	return users, UserCursor{UID: cursor}, done, err
}

func (s *ShardStore) CreateChannel(ctx context.Context, channel Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.CreateChannel(ctx, channel)
}

func (s *ShardStore) UpsertChannel(ctx context.Context, channel Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertChannel(ctx, channel)
}

func (s *ShardStore) UpdateChannel(ctx context.Context, channel Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpdateChannel(ctx, channel)
}

func (s *ShardStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteChannel(ctx, channelID, channelType)
}

func (s *ShardStore) GetChannel(ctx context.Context, channelID string, channelType int64) (Channel, error) {
	if err := s.validate(); err != nil {
		return Channel{}, err
	}
	channel, ok, err := s.shard.GetChannel(ctx, channelID, channelType)
	return channel, foundError(ok, err)
}

func (s *ShardStore) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListChannelsByChannelID(ctx, channelID)
}

func (s *ShardStore) ListChannelsPage(ctx context.Context, after ChannelCursor, limit int) ([]Channel, ChannelCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	return s.shard.listChannelsPage(ctx, after, limit)
}

func (s *ShardStore) UpsertDevice(ctx context.Context, device Device) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertDevice(ctx, device)
}

func (s *ShardStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, error) {
	if err := s.validate(); err != nil {
		return Device{}, err
	}
	device, ok, err := s.shard.GetDevice(ctx, uid, deviceFlag)
	return device, foundError(ok, err)
}

func (s *ShardStore) UpsertChannelRuntimeMeta(ctx context.Context, meta ChannelRuntimeMeta) error {
	if err := s.validate(); err != nil {
		return err
	}
	_, err := s.shard.UpsertChannelRuntimeMeta(ctx, meta)
	return err
}

func (s *ShardStore) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (ChannelRuntimeMeta, error) {
	if err := s.validate(); err != nil {
		return ChannelRuntimeMeta{}, err
	}
	meta, ok, err := s.shard.GetChannelRuntimeMeta(ctx, channelID, channelType)
	return meta, foundError(ok, err)
}

func (s *ShardStore) DeleteChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteChannelRuntimeMeta(ctx, channelID, channelType)
}

func (s *ShardStore) AdvanceChannelRetentionThroughSeq(ctx context.Context, req ChannelRetentionAdvance) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.AdvanceChannelRetentionThroughSeq(ctx, req)
}

func (s *ShardStore) ListChannelRuntimeMetaPage(ctx context.Context, after ChannelRuntimeMetaCursor, limit int) ([]ChannelRuntimeMeta, ChannelRuntimeMetaCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	return s.shard.ListChannelRuntimeMetaPage(ctx, after, limit)
}

func (s *ShardStore) AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion ...uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	wb := s.db.NewWriteBatch()
	defer wb.Close()
	if err := wb.AddSubscribers(uint16(s.hashSlot), channelID, channelType, uids, mutationVersion...); err != nil {
		return err
	}
	return wb.Commit()
}

func (s *ShardStore) RemoveSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion ...uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	wb := s.db.NewWriteBatch()
	defer wb.Close()
	if err := wb.RemoveSubscribers(uint16(s.hashSlot), channelID, channelType, uids, mutationVersion...); err != nil {
		return err
	}
	return wb.Commit()
}

func (s *ShardStore) ListSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if err := s.validate(); err != nil {
		return nil, "", false, err
	}
	return s.shard.ListSubscribersPage(ctx, channelID, channelType, afterUID, limit)
}

func (s *ShardStore) ListSubscribersSnapshot(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.SnapshotSubscribers(ctx, channelID, channelType)
}

func (s *ShardStore) ContainsSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.shard.ContainsSubscriber(ctx, channelID, channelType, uid)
}

func (s *ShardStore) HasSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.shard.HasSubscribers(ctx, channelID, channelType)
}

func (s *ShardStore) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (UserConversationState, error) {
	if err := s.validate(); err != nil {
		return UserConversationState{}, err
	}
	state, ok, err := s.shard.GetUserConversationState(ctx, uid, channelID, channelType)
	return state, foundError(ok, err)
}

func (s *ShardStore) UpsertUserConversationState(ctx context.Context, state UserConversationState) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertUserConversationState(ctx, state)
}

func (s *ShardStore) TouchUserConversationActiveAt(ctx context.Context, patch UserConversationActivePatch) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.TouchUserConversationActiveAt(ctx, patch)
}

func (s *ShardStore) ClearUserConversationActiveAt(ctx context.Context, uid string, keys []ConversationKey) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.ClearUserConversationActiveAt(ctx, uid, keys)
}

func (s *ShardStore) HideUserConversation(ctx context.Context, req UserConversationDelete) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.HideUserConversation(ctx, req)
}

func (s *ShardStore) ListUserConversationActive(ctx context.Context, uid string, limit int) ([]UserConversationState, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListUserConversationActive(ctx, uid, limit)
}

func (s *ShardStore) ListUserConversationStatePage(ctx context.Context, uid string, after ConversationCursor, limit int) ([]UserConversationState, ConversationCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	return s.shard.ListUserConversationStatePage(ctx, uid, after, limit)
}

func (s *ShardStore) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (CMDConversationState, error) {
	if err := s.validate(); err != nil {
		return CMDConversationState{}, err
	}
	state, ok, err := s.shard.GetCMDConversationState(ctx, uid, channelID, channelType)
	return state, foundError(ok, err)
}

func (s *ShardStore) UpsertCMDConversationState(ctx context.Context, state CMDConversationState) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertCMDConversationState(ctx, state)
}

func (s *ShardStore) AdvanceCMDConversationReadSeq(ctx context.Context, patch CMDConversationReadPatch) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.AdvanceCMDConversationReadSeq(ctx, patch)
}

func (s *ShardStore) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]CMDConversationState, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListCMDConversationActive(ctx, uid, limit)
}

func (s *ShardStore) BindPluginUser(ctx context.Context, binding PluginUserBinding) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.BindPluginUser(ctx, binding)
}

func (s *ShardStore) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UnbindPluginUser(ctx, uid, pluginNo)
}

func (s *ShardStore) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginUserBinding, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListPluginBindingsByUID(ctx, uid)
}

func (s *ShardStore) ScanPluginBindingsByPluginNo(ctx context.Context, pluginNo string, after PluginUserBindingCursor, limit int) ([]PluginUserBinding, PluginUserBindingCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	bindings, cursor, done, err := s.shard.ScanPluginBindingsByPluginNo(ctx, pluginNo, after, limit)
	return bindings, cursor, !done, err
}

func (s *ShardStore) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.shard.ExistPluginBindingByUID(ctx, uid)
}

func foundError(ok bool, err error) error {
	if err != nil {
		return err
	}
	if !ok {
		return dberrors.ErrNotFound
	}
	return nil
}

func optionalVersion(values []uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

func (s *Shard) listChannelsPage(ctx context.Context, after ChannelCursor, limit int) ([]Channel, ChannelCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	if limit <= 0 {
		return nil, ChannelCursor{}, false, dberrors.ErrInvalidArgument
	}
	prefix := encodeRowPrefix(s.hashSlot, TableIDChannel)
	span := keycodec.NewPrefixSpan(prefix)
	if after.ChannelID != "" {
		span.Start = keycodec.PrefixEnd(encodeChannelRowKey(s.hashSlot, after.ChannelID, after.ChannelType, channelPrimaryFamilyID))
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, ChannelCursor{}, false, err
	}
	defer iter.Close()
	channels := make([]Channel, 0, limit)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, ChannelCursor{}, false, err
		}
		channelID, channelType, familyID, ok := decodeChannelRowKey(prefix, iter.Key())
		if !ok {
			return nil, ChannelCursor{}, false, dberrors.ErrCorruptValue
		}
		if familyID != channelPrimaryFamilyID {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, ChannelCursor{}, false, err
		}
		channel, err := decodeChannelValue(channelID, channelType, value)
		if err != nil {
			return nil, ChannelCursor{}, false, err
		}
		channels = append(channels, channel)
		if len(channels) == limit {
			return channels, ChannelCursor{ChannelID: channel.ChannelID, ChannelType: channel.ChannelType}, false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	return channels, ChannelCursor{}, true, nil
}

func decodeChannelRowKey(prefix []byte, key []byte) (string, int64, uint16, bool) {
	if !bytesHasPrefix(key, prefix) {
		return "", 0, 0, false
	}
	channelID, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil {
		return "", 0, 0, false
	}
	channelType, rest, err := readKeyInt64Ordered(rest)
	if err != nil || len(rest) != 2 {
		return "", 0, 0, false
	}
	return channelID, channelType, binary.BigEndian.Uint16(rest), true
}

func isMetaRowKeyForTable(key []byte, tableID uint32) (HashSlot, bool) {
	if len(key) < 9 || key[0] != byte(keycodec.DomainMeta) || key[1] != byte(keycodec.PartitionHashSlot) || key[4] != byte(keycodec.SpaceRow) {
		return 0, false
	}
	if binary.BigEndian.Uint32(key[5:9]) != tableID {
		return 0, false
	}
	return HashSlot(binary.BigEndian.Uint16(key[2:4])), true
}

// ListChannelRuntimeMeta scans all runtime metadata rows.
func (db *DB) ListChannelRuntimeMeta(ctx context.Context) ([]ChannelRuntimeMeta, error) {
	if db == nil || db.meta == nil || db.meta.engine == nil {
		return nil, dberrors.ErrClosed
	}
	iter, err := db.meta.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []ChannelRuntimeMeta
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := iter.Key()
		hashSlot, ok := isMetaRowKeyForTable(key, TableIDChannelRuntimeMeta)
		if !ok {
			continue
		}
		prefix := encodeChannelRuntimeMetaRowPrefix(hashSlot)
		channelID, channelType, familyID, ok := decodeChannelRuntimeMetaRowKey(prefix, key)
		if !ok || familyID != channelRuntimeMetaPrimaryFamilyID {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		meta, err := channelRuntimeMetaTable.decodeValue(key, channelRuntimeMetaPrimaryKey(channelID, channelType), value)
		if err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, iter.Error()
}

// Hash-slot migration DB-level compatibility helpers.
func (db *DB) UpsertHashSlotMigrationState(ctx context.Context, state HashSlotMigrationState) error {
	return db.ForHashSlot(uint16(state.HashSlot)).UpsertHashSlotMigrationState(ctx, state)
}

func (db *DB) LoadHashSlotMigrationState(ctx context.Context, hashSlot uint16) (HashSlotMigrationState, error) {
	return db.ForHashSlot(hashSlot).LoadHashSlotMigrationState(ctx)
}

func (db *DB) DeleteHashSlotMigrationState(ctx context.Context, hashSlot uint16) error {
	return db.ForHashSlot(hashSlot).DeleteHashSlotMigrationState(ctx)
}

func (db *DB) MarkAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	return db.ForHashSlot(uint16(delta.HashSlot)).MarkAppliedHashSlotDelta(ctx, delta)
}

func (db *DB) HasAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) (bool, error) {
	return db.ForHashSlot(uint16(delta.HashSlot)).HasAppliedHashSlotDelta(ctx, delta)
}

func (db *DB) ListAppliedHashSlotDeltas(ctx context.Context, hashSlot uint16) ([]AppliedHashSlotDelta, error) {
	return db.ForHashSlot(hashSlot).ListAppliedHashSlotDeltas(ctx)
}

func (db *DB) DeleteAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	return db.ForHashSlot(uint16(delta.HashSlot)).DeleteAppliedHashSlotDelta(ctx, delta)
}

func (db *DB) UpsertHashSlotMigrationOutbox(ctx context.Context, row HashSlotMigrationOutboxRow) error {
	return db.ForHashSlot(uint16(row.HashSlot)).UpsertHashSlotMigrationOutbox(ctx, row)
}

func (db *DB) LoadHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) (HashSlotMigrationOutboxRow, error) {
	return db.ForHashSlot(hashSlot).LoadHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, sourceIndex)
}

func (db *DB) ListHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, afterSourceIndex uint64, limit int) ([]HashSlotMigrationOutboxRow, error) {
	return db.ForHashSlot(hashSlot).ListHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, afterSourceIndex, limit)
}

func (db *DB) DeleteHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	return db.ForHashSlot(hashSlot).DeleteHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, sourceIndex)
}

func (db *DB) DeleteAllHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16) error {
	return db.ForHashSlot(hashSlot).DeleteAllHashSlotMigrationOutbox(ctx)
}

func (db *DB) ListHashSlotMigrationStates(ctx context.Context) ([]HashSlotMigrationState, error) {
	if db == nil || db.meta == nil || db.meta.engine == nil {
		return nil, dberrors.ErrClosed
	}
	iter, err := db.meta.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []HashSlotMigrationState
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := iter.Key()
		hashSlot, ok := isMetaRowKeyForTable(key, TableIDHashSlotMigration)
		if !ok || !bytesHasPrefix(key, encodeHashSlotMigrationStateKey(hashSlot)) {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := decodeHashSlotMigrationStateValue(hashSlot, value)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, iter.Error()
}

func (s *ShardStore) UpsertHashSlotMigrationState(ctx context.Context, state HashSlotMigrationState) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertHashSlotMigrationState(ctx, state)
}

func (s *ShardStore) LoadHashSlotMigrationState(ctx context.Context) (HashSlotMigrationState, error) {
	if err := s.validate(); err != nil {
		return HashSlotMigrationState{}, err
	}
	state, ok, err := s.shard.LoadHashSlotMigrationState(ctx)
	return state, foundError(ok, err)
}

func (s *ShardStore) DeleteHashSlotMigrationState(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteHashSlotMigrationState(ctx)
}

func (s *ShardStore) MarkAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.MarkAppliedHashSlotDelta(ctx, delta)
}

func (s *ShardStore) HasAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.shard.HasAppliedHashSlotDelta(ctx, delta)
}

func (s *ShardStore) ListAppliedHashSlotDeltas(ctx context.Context) ([]AppliedHashSlotDelta, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListAppliedHashSlotDeltas(ctx)
}

func (s *ShardStore) DeleteAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteAppliedHashSlotDelta(ctx, delta)
}

func (s *ShardStore) UpsertHashSlotMigrationOutbox(ctx context.Context, row HashSlotMigrationOutboxRow) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.UpsertHashSlotMigrationOutbox(ctx, row)
}

func (s *ShardStore) LoadHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) (HashSlotMigrationOutboxRow, error) {
	if err := s.validate(); err != nil {
		return HashSlotMigrationOutboxRow{}, err
	}
	row, ok, err := s.shard.LoadHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, sourceIndex)
	return row, foundError(ok, err)
}

func (s *ShardStore) ListHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, afterSourceIndex uint64, limit int) ([]HashSlotMigrationOutboxRow, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, afterSourceIndex, limit)
}

func (s *ShardStore) DeleteHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteHashSlotMigrationOutbox(ctx, sourceSlot, targetSlot, sourceIndex)
}

func (s *ShardStore) DeleteHashSlotMigrationOutboxThrough(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteHashSlotMigrationOutboxThrough(ctx, sourceSlot, targetSlot, sourceIndex)
}

func (s *ShardStore) DeleteAllHashSlotMigrationOutbox(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.DeleteAllHashSlotMigrationOutbox(ctx)
}

// WriteBatch accumulates compatibility mutations and commits them atomically.
type WriteBatch struct {
	db               *DB
	batch            *Batch
	err              error
	migrationCreates map[string]ChannelMigrationTask
}

func (b *WriteBatch) Close() error {
	if b == nil || b.batch == nil {
		return nil
	}
	return b.batch.Close()
}

func (b *WriteBatch) Commit() error {
	if b == nil {
		return dberrors.ErrClosed
	}
	if b.err != nil {
		return b.err
	}
	return b.batch.Commit(context.Background())
}

func (b *WriteBatch) ensure() error {
	if b == nil || b.batch == nil || b.db == nil || b.db.meta == nil {
		return dberrors.ErrClosed
	}
	if b.err != nil {
		return b.err
	}
	return nil
}

func (b *WriteBatch) CreateUser(hashSlot uint16, user User) error {
	if err := b.ensure(); err != nil {
		return err
	}
	key := encodeUserRowKey(HashSlot(hashSlot), user.UID, userPrimaryFamilyID)
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		if overlay, ok := state.tableRows[string(key)]; ok && overlay.exists {
			return nil
		}
		if _, ok, err := state.db.get(key); err != nil || ok {
			return err
		}
		value := encodeUserValue(user)
		if err := batch.Set(key, value); err != nil {
			return err
		}
		state.tableRows[string(key)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func (b *WriteBatch) UpsertUser(hashSlot uint16, user User) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return b.batch.UpsertUser(HashSlot(hashSlot), user)
}

func (b *WriteBatch) UpsertDevice(hashSlot uint16, device Device) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return deviceTable.StageUpsert(b.batch, HashSlot(hashSlot), device)
}

func (b *WriteBatch) UpsertChannel(hashSlot uint16, channel Channel) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return b.batch.UpsertChannel(HashSlot(hashSlot), channel)
}

func (b *WriteBatch) DeleteChannel(hashSlot uint16, channelID string, channelType int64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	primaryKey := encodeChannelRowKey(hs, channelID, channelType, channelPrimaryFamilyID)
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		if err := batch.Delete(primaryKey); err != nil {
			return err
		}
		if err := batch.Delete(encodeChannelIDIndexKey(hs, channelID, channelType)); err != nil {
			return err
		}
		delete(state.channelPublishes, string(primaryKey))
		state.channelDeletes[string(primaryKey)] = struct{}{}
		return batch.DeleteRange(engine.Span{Start: encodeSubscriberRowPrefix(hs, channelID, channelType), End: keycodec.PrefixEnd(encodeSubscriberRowPrefix(hs, channelID, channelType))})
	})
	return nil
}

func (b *WriteBatch) UpsertChannelRuntimeMeta(hashSlot uint16, meta ChannelRuntimeMeta) error {
	if err := b.ensure(); err != nil {
		return err
	}
	_, err := b.batch.UpsertChannelRuntimeMeta(HashSlot(hashSlot), meta)
	return err
}

func (b *WriteBatch) DeleteChannelRuntimeMeta(hashSlot uint16, channelID string, channelType int64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	key := encodeChannelRuntimeMetaRowKey(HashSlot(hashSlot), channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		state.runtimeMeta[string(key)] = runtimeMetaOverlay{exists: false}
		return batch.Delete(key)
	})
	return nil
}

func (b *WriteBatch) AdvanceChannelRetentionThroughSeq(hashSlot uint16, req ChannelRetentionAdvance) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		key := encodeChannelRuntimeMetaRowKey(HashSlot(hashSlot), req.ChannelID, req.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		existing, exists, err := state.loadRuntimeMeta(ctx, HashSlot(hashSlot), key, req.ChannelID, req.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return dberrors.ErrNotFound
		}
		if existing.ChannelEpoch != req.ExpectedChannelEpoch ||
			existing.LeaderEpoch != req.ExpectedLeaderEpoch ||
			existing.Leader != req.ExpectedLeader ||
			existing.LeaseUntilMS != req.ExpectedLeaseUntilMS {
			return dberrors.ErrConflict
		}
		if req.RetentionThroughSeq <= existing.RetentionThroughSeq {
			return nil
		}
		next := existing
		next.RetentionThroughSeq = req.RetentionThroughSeq
		next.RetentionUpdatedAtMS = req.RetentionUpdatedAtMS
		next.RouteGeneration = nextChannelRouteGeneration(existing.RouteGeneration)
		value, err := channelRuntimeMetaTable.encodeValue(key, next)
		if err != nil {
			return err
		}
		if err := batch.Set(key, value); err != nil {
			return err
		}
		state.runtimeMeta[string(key)] = runtimeMetaOverlay{meta: next, exists: true}
		return nil
	})
	return nil
}

func (b *WriteBatch) AddSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, mutationVersion ...uint64) error {
	return b.stageSubscribers(hashSlot, channelID, channelType, uids, optionalVersion(mutationVersion), true)
}

func (b *WriteBatch) RemoveSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, mutationVersion ...uint64) error {
	return b.stageSubscribers(hashSlot, channelID, channelType, uids, optionalVersion(mutationVersion), false)
}

func (b *WriteBatch) stageSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, mutationVersion uint64, add bool) error {
	if err := b.ensure(); err != nil {
		return err
	}
	normalized, err := normalizeSubscriberUIDs(uids)
	if err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		primaryKey := encodeChannelRowKey(hs, channelID, channelType, channelPrimaryFamilyID)
		value, ok, err := state.db.get(primaryKey)
		if err != nil {
			return err
		}
		channel := Channel{ChannelID: channelID, ChannelType: channelType}
		if ok {
			channel, err = decodeChannelValue(channelID, channelType, value)
			if err != nil {
				return err
			}
		}
		if mutationVersion > 0 {
			if ok && channel.SubscriberMutationVersion > mutationVersion {
				return dberrors.ErrConflict
			}
			channel.SubscriberMutationVersion = mutationVersion
		}
		for _, uid := range normalized {
			key, err := subscriberRowKey(hs, channelID, channelType, uid)
			if err != nil {
				return err
			}
			if add {
				if err := batch.Set(key, nil); err != nil {
					return err
				}
			} else if err := batch.Delete(key); err != nil {
				return err
			}
		}
		if mutationVersion > 0 {
			if err := (&Shard{db: state.db, hashSlot: hs}).stageChannel(batch, primaryKey, channel); err != nil {
				return err
			}
			state.channelPublishes[string(primaryKey)] = channel
			delete(state.channelDeletes, string(primaryKey))
		}
		return nil
	})
	return nil
}

func (b *WriteBatch) UpsertUserConversationState(hashSlot uint16, state UserConversationState) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
		key := encodeConversationRowKey(HashSlot(hashSlot), state.UID, state.ChannelID, state.ChannelType, conversationPrimaryFamilyID)
		existing, exists, err := shard.getUserConversationStateByKey(ctx, key, state.UID, state.ChannelID, state.ChannelType)
		if err != nil {
			return err
		}
		next := state
		if exists && next.ActiveAt < existing.ActiveAt {
			next.ActiveAt = existing.ActiveAt
		}
		return shard.stageUserConversationState(batch, key, existing, exists, next)
	})
	return nil
}

func (b *WriteBatch) TouchUserConversationActiveAt(hashSlot uint16, patches []UserConversationActivePatch) error {
	if err := b.ensure(); err != nil {
		return err
	}
	for _, patch := range patches {
		p := patch
		b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
			shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
			key := encodeConversationRowKey(HashSlot(hashSlot), p.UID, p.ChannelID, p.ChannelType, conversationPrimaryFamilyID)
			current, exists, err := shard.getUserConversationStateByKey(ctx, key, p.UID, p.ChannelID, p.ChannelType)
			if err != nil {
				return err
			}
			if !exists {
				current = UserConversationState{UID: p.UID, ChannelID: p.ChannelID, ChannelType: p.ChannelType}
			}
			if p.MessageSeq > 0 && p.MessageSeq <= current.DeletedToSeq || p.ActiveAt <= current.ActiveAt {
				return nil
			}
			next := current
			next.ActiveAt = p.ActiveAt
			return shard.stageUserConversationState(batch, key, current, exists, next)
		})
	}
	return nil
}

func (b *WriteBatch) ClearUserConversationActiveAt(hashSlot uint16, uid string, keys []ConversationKey) error {
	if err := b.ensure(); err != nil {
		return err
	}
	for _, key := range keys {
		k := key
		b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
			shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
			primaryKey := encodeConversationRowKey(HashSlot(hashSlot), uid, k.ChannelID, k.ChannelType, conversationPrimaryFamilyID)
			current, exists, err := shard.getUserConversationStateByKey(ctx, primaryKey, uid, k.ChannelID, k.ChannelType)
			if err != nil || !exists || current.ActiveAt == 0 {
				return err
			}
			next := current
			next.ActiveAt = 0
			return shard.stageUserConversationState(batch, primaryKey, current, true, next)
		})
	}
	return nil
}

func (b *WriteBatch) HideUserConversation(hashSlot uint16, req UserConversationDelete) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
		key := encodeConversationRowKey(HashSlot(hashSlot), req.UID, req.ChannelID, req.ChannelType, conversationPrimaryFamilyID)
		current, exists, err := shard.getUserConversationStateByKey(ctx, key, req.UID, req.ChannelID, req.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			if req.DeletedToSeq == 0 {
				return nil
			}
			current = UserConversationState{UID: req.UID, ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		}
		if req.DeletedToSeq <= current.DeletedToSeq {
			return nil
		}
		next := current
		next.DeletedToSeq = req.DeletedToSeq
		next.ActiveAt = 0
		if req.UpdatedAt > next.UpdatedAt {
			next.UpdatedAt = req.UpdatedAt
		}
		return shard.stageUserConversationState(batch, key, current, exists, next)
	})
	return nil
}

func (b *WriteBatch) UpsertCMDConversationState(hashSlot uint16, state CMDConversationState) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
		key := encodeCMDConversationRowKey(HashSlot(hashSlot), state.UID, state.ChannelID, state.ChannelType, cmdConversationPrimaryFamilyID)
		existing, exists, err := shard.getCMDConversationStateByKey(ctx, key, state.UID, state.ChannelID, state.ChannelType)
		if err != nil {
			return err
		}
		next := state
		if exists {
			next = mergeCMDConversationState(existing, next)
		}
		return shard.stageCMDConversationState(batch, key, existing, exists, next)
	})
	return nil
}

func (b *WriteBatch) AdvanceCMDConversationReadSeq(hashSlot uint16, patches []CMDConversationReadPatch) error {
	if err := b.ensure(); err != nil {
		return err
	}
	for _, patch := range patches {
		p := patch
		b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
			shard := &Shard{db: st.db, hashSlot: HashSlot(hashSlot)}
			key := encodeCMDConversationRowKey(HashSlot(hashSlot), p.UID, p.ChannelID, p.ChannelType, cmdConversationPrimaryFamilyID)
			current, exists, err := shard.getCMDConversationStateByKey(ctx, key, p.UID, p.ChannelID, p.ChannelType)
			if err != nil || !exists || p.ReadSeq <= current.ReadSeq {
				return err
			}
			current.ReadSeq = p.ReadSeq
			if p.UpdatedAt > current.UpdatedAt {
				current.UpdatedAt = p.UpdatedAt
			}
			return shard.stageCMDConversationState(batch, key, current, true, current)
		})
	}
	return nil
}

func (b *WriteBatch) BindPluginUser(hashSlot uint16, binding PluginUserBinding) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return pluginBindingTable.StageUpsert(b.batch, HashSlot(hashSlot), binding)
}

func (b *WriteBatch) UnbindPluginUser(hashSlot uint16, uid, pluginNo string) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return pluginBindingTable.StageDelete(b.batch, HashSlot(hashSlot), KeyParts{String(uid), String(pluginNo)})
}

func (b *WriteBatch) UpsertHashSlotMigrationState(state HashSlotMigrationState) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return hashSlotMigrationTable.StageUpsert(b.batch, state.HashSlot, state)
}

func (b *WriteBatch) DeleteHashSlotMigrationState(hashSlot uint16) error {
	if err := b.ensure(); err != nil {
		return err
	}
	return hashSlotMigrationTable.StageDelete(b.batch, HashSlot(hashSlot), hashSlotMigrationStatePrimaryKey())
}

func (b *WriteBatch) MarkAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(delta.HashSlot, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.Set(encodeAppliedHashSlotDeltaKey(delta), nil)
	})
	return nil
}

func (b *WriteBatch) DeleteAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(delta.HashSlot, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.Delete(encodeAppliedHashSlotDeltaKey(delta))
	})
	return nil
}

func (b *WriteBatch) UpsertHashSlotMigrationOutbox(row HashSlotMigrationOutboxRow) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(row.HashSlot, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.Set(encodeHashSlotMigrationOutboxKey(row.HashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex), encodeHashSlotMigrationOutboxValue(row))
	})
	return nil
}

func (b *WriteBatch) DeleteHashSlotMigrationOutbox(hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.Delete(encodeHashSlotMigrationOutboxKey(HashSlot(hashSlot), sourceSlot, targetSlot, sourceIndex))
	})
	return nil
}

func (b *WriteBatch) DeleteHashSlotMigrationOutboxForPair(hashSlot uint16, sourceSlot, targetSlot uint64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	prefix := encodeHashSlotMigrationOutboxPrefix(HashSlot(hashSlot), sourceSlot, targetSlot)
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.DeleteRange(engine.Span{Start: prefix, End: keycodec.PrefixEnd(prefix)})
	})
	return nil
}

func (b *WriteBatch) DeleteHashSlotMigrationOutboxThrough(hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := b.ensure(); err != nil {
		return err
	}
	start := encodeHashSlotMigrationOutboxPrefix(HashSlot(hashSlot), sourceSlot, targetSlot)
	end := keycodec.PrefixEnd(encodeHashSlotMigrationOutboxKey(HashSlot(hashSlot), sourceSlot, targetSlot, sourceIndex))
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.DeleteRange(engine.Span{Start: start, End: end})
	})
	return nil
}

func (b *WriteBatch) DeleteAllHashSlotMigrationOutbox(hashSlot uint16) error {
	if err := b.ensure(); err != nil {
		return err
	}
	prefix := encodeHashSlotMigrationOutboxHashSlotPrefix(HashSlot(hashSlot))
	b.batch.addOp(HashSlot(hashSlot), func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		return batch.DeleteRange(engine.Span{Start: prefix, End: keycodec.PrefixEnd(prefix)})
	})
	return nil
}

func (b *WriteBatch) CreateChannelMigrationTask(hashSlot uint16, task ChannelMigrationTask) error {
	if err := b.ensure(); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	primaryKey, err := channelMigrationTaskRowKey(hs, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		return err
	}
	if existing, ok := b.migrationCreates[string(primaryKey)]; ok {
		if existing == task {
			return nil
		}
		return dberrors.ErrAlreadyExists
	}
	if task.IsActive() {
		activeKey := encodeChannelMigrationActiveIndexKey(hs, task.ChannelID, task.ChannelType)
		if b.batch.migrationActive == nil {
			b.batch.migrationActive = make(map[string]struct{})
		}
		if _, ok := b.batch.migrationActive[string(activeKey)]; ok {
			return dberrors.ErrAlreadyExists
		}
		b.batch.migrationActive[string(activeKey)] = struct{}{}
	}
	b.migrationCreates[string(primaryKey)] = task
	b.batch.addOp(hs, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: hs}
		existing, exists, err := st.loadChannelMigrationTask(ctx, hs, primaryKey, task.ChannelID, task.ChannelType, task.TaskID)
		if err != nil {
			return err
		}
		if exists {
			if existing == task {
				return nil
			}
			return dberrors.ErrAlreadyExists
		}
		if err := shard.stageUpsertChannelMigrationTask(ctx, batch, task); err != nil {
			return err
		}
		st.migrationTasks[string(primaryKey)] = migrationTaskOverlay{task: task, exists: true}
		return nil
	})
	return nil
}

func (b *WriteBatch) CreateChannelMigrationTaskWithRuntimeGuard(hashSlot uint16, req ChannelMigrationTaskCreate) error {
	if err := b.ensure(); err != nil {
		return err
	}
	if err := validateChannelMigrationTaskCreate(req); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		primaryKey, err := channelMigrationTaskRowKey(hs, req.Task.ChannelID, req.Task.ChannelType, req.Task.TaskID)
		if err != nil {
			return err
		}
		existing, exists, err := st.loadChannelMigrationTask(ctx, hs, primaryKey, req.Task.ChannelID, req.Task.ChannelType, req.Task.TaskID)
		if err != nil {
			return err
		}
		if exists {
			if existing == req.Task {
				return nil
			}
			return dberrors.ErrAlreadyExists
		}
		metaKey := encodeChannelRuntimeMetaRowKey(hs, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		meta, ok, err := st.loadRuntimeMeta(ctx, hs, metaKey, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType)
		if err != nil {
			return err
		}
		if !ok {
			return dberrors.ErrNotFound
		}
		if !req.RuntimeGuard.matches(meta) {
			return dberrors.ErrConflict
		}
		return nil
	})
	return b.CreateChannelMigrationTask(hashSlot, req.Task)
}

func (b *WriteBatch) ClaimChannelMigrationTask(hashSlot uint16, req ChannelMigrationTaskClaim) error {
	return b.stageChannelMigrationTask(hashSlot, req.Guard, func(task ChannelMigrationTask) ChannelMigrationTask {
		task.Status = req.Status
		task.Phase = req.Phase
		task.OwnerNodeID = req.OwnerNodeID
		task.OwnerLeaseUntilMS = req.OwnerLeaseUntilMS
		task.UpdatedAtMS = req.UpdatedAtMS
		return task
	})
}

func (b *WriteBatch) AdvanceChannelMigrationTask(hashSlot uint16, req ChannelMigrationTaskAdvance) error {
	return b.stageChannelMigrationTask(hashSlot, req.Guard, func(task ChannelMigrationTask) ChannelMigrationTask {
		task.Status = req.Status
		task.Phase = req.Phase
		task.Attempt = req.Attempt
		task.NextRunAtMS = req.NextRunAtMS
		task.BlockerCode = req.BlockerCode
		task.BlockerMessage = req.BlockerMessage
		task.LastError = req.LastError
		task.UpdatedAtMS = req.UpdatedAtMS
		task.CompletedAtMS = req.CompletedAtMS
		task.Progress = req.Progress
		if req.CutoverProof != (ChannelMigrationCutoverProof{}) {
			task.CutoverLEO = req.CutoverProof.CutoverLEO
			task.CutoverHW = req.CutoverProof.CutoverHW
			task.DrainedLeaderNode = req.CutoverProof.DrainedLeaderNode
			task.DrainedRuntimeGeneration = req.CutoverProof.DrainedRuntimeGeneration
			task.DrainedChannelEpoch = req.CutoverProof.DrainedChannelEpoch
			task.DrainedLeaderEpoch = req.CutoverProof.DrainedLeaderEpoch
			task.DrainedFenceVersion = req.CutoverProof.DrainedFenceVersion
		}
		if req.EmbeddedDesiredLeader != 0 {
			task.EmbeddedLeaderTransfer = true
			task.EmbeddedDesiredLeader = req.EmbeddedDesiredLeader
		}
		return task
	})
}

func (b *WriteBatch) stageChannelMigrationTask(hashSlot uint16, guard ChannelMigrationTaskGuard, mutate func(ChannelMigrationTask) ChannelMigrationTask) error {
	if err := b.ensure(); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: hs}
		taskKey, err := channelMigrationTaskRowKey(hs, guard.ChannelID, guard.ChannelType, guard.TaskID)
		if err != nil {
			return err
		}
		task, ok, err := st.loadChannelMigrationTask(ctx, hs, taskKey, guard.ChannelID, guard.ChannelType, guard.TaskID)
		if err != nil {
			return err
		}
		if !ok {
			return dberrors.ErrNotFound
		}
		if !guard.matches(task) {
			return dberrors.ErrConflict
		}
		next := mutate(task)
		if err := shard.stageUpsertChannelMigrationTask(ctx, batch, next); err != nil {
			return err
		}
		st.migrationTasks[string(taskKey)] = migrationTaskOverlay{task: next, exists: true}
		return nil
	})
	return nil
}

func (b *WriteBatch) SetChannelWriteFence(hashSlot uint16, req ChannelMigrationFenceRequest) error {
	if err := validateChannelMigrationFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationSetFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireNoForeignChannelMigrationFence(task, meta); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.FenceToken = task.TaskID
		nextTask.FenceVersion = meta.WriteFenceVersion + 1
		nextTask.FenceUntilMS = req.FenceUntilMS
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.WriteFenceToken = task.TaskID
		nextMeta.WriteFenceVersion = meta.WriteFenceVersion + 1
		nextMeta.WriteFenceReason = req.FenceReason
		nextMeta.WriteFenceUntilMS = req.FenceUntilMS
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) ResetChannelWriteFenceToPreCutover(hashSlot uint16, req ChannelMigrationResetFenceRequest) error {
	if err := validateChannelMigrationResetFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationResetFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.NowMS <= meta.WriteFenceUntilMS {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := clearChannelRuntimeMetaFence(meta)
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) CommitChannelLeaderTransfer(hashSlot uint16, req ChannelMigrationLeaderTransferRequest) error {
	if err := validateChannelMigrationLeaderTransferRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationLeaderTransferTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, req.NowMS, false); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireChannelMigrationCutoverProof(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.DesiredLeader != channelMigrationTaskDesiredLeader(task) || !containsUint64(meta.ISR, req.DesiredLeader) || req.NextLeaderEpoch <= meta.LeaderEpoch {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.Leader = req.DesiredLeader
		nextMeta.LeaderEpoch = req.NextLeaderEpoch
		nextMeta.LeaseUntilMS = req.LeaseUntilMS
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) AddChannelLearner(hashSlot uint16, req ChannelMigrationAddLearnerRequest) error {
	if err := validateChannelMigrationAddLearnerRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationAddLearnerTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.TargetNode != task.TargetNode ||
			meta.Leader == task.SourceNode ||
			!containsUint64(meta.Replicas, task.SourceNode) ||
			!containsUint64(meta.ISR, task.SourceNode) ||
			containsUint64(meta.Replicas, req.TargetNode) ||
			containsUint64(meta.ISR, req.TargetNode) {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		if !containsUint64(nextMeta.Replicas, req.TargetNode) {
			nextMeta.Replicas = append(nextMeta.Replicas, req.TargetNode)
			nextMeta.ChannelEpoch++
		}
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) PromoteLearnerAndRemoveReplica(hashSlot uint16, req ChannelMigrationPromoteLearnerRequest) error {
	if err := validateChannelMigrationPromoteLearnerRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationPromoteLearnerTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, req.NowMS, false); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireChannelMigrationCutoverProof(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if req.SourceNode != task.SourceNode ||
			req.TargetNode != task.TargetNode ||
			meta.Leader == req.SourceNode ||
			!containsUint64(meta.Replicas, req.SourceNode) ||
			!containsUint64(meta.Replicas, req.TargetNode) ||
			!containsUint64(meta.ISR, req.SourceNode) ||
			containsUint64(meta.ISR, req.TargetNode) {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		nextTask := task
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS

		nextMeta := meta
		nextMeta.Replicas = replaceUint64Member(nextMeta.Replicas, req.SourceNode, req.TargetNode)
		nextMeta.ISR = replaceUint64Member(nextMeta.ISR, req.SourceNode, req.TargetNode)
		nextMeta.ChannelEpoch++
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) ClearChannelWriteFence(hashSlot uint16, req ChannelMigrationClearFenceRequest) error {
	if err := validateChannelMigrationClearFenceRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if err := requireChannelMigrationClearFenceTransition(task, req); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if isChannelMigrationClearFenceIdempotent(task, meta, req) {
			return task, meta, nil
		}
		if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS
		nextTask.CompletedAtMS = req.CompletedAtMS
		if task.Kind == ChannelMigrationKindReplicaReplace &&
			task.EmbeddedLeaderTransfer &&
			task.Phase == ChannelMigrationPhaseVerifyNewLeader &&
			req.Status == ChannelMigrationStatusRunning &&
			req.Phase == ChannelMigrationPhaseAddLearner {
			nextTask.EmbeddedLeaderTransfer = false
			nextTask.EmbeddedDesiredLeader = 0
		}

		nextMeta := clearChannelRuntimeMetaFence(meta)
		return nextTask, nextMeta, nil
	})
}

func (b *WriteBatch) AbortChannelMigration(hashSlot uint16, req ChannelMigrationAbortRequest) error {
	if err := validateChannelMigrationAbortRequest(req); err != nil {
		return err
	}
	return b.stageChannelMigrationTaskAndMeta(hashSlot, req.Guard, req.RuntimeGuard, func(task ChannelMigrationTask, meta ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error) {
		if task.IsTerminal() {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		if err := requireChannelMigrationAbortTransition(task); err != nil {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
		}
		nextTask := clearChannelMigrationTaskFenceAndProof(task)
		nextTask.Status = req.Status
		nextTask.Phase = req.Phase
		nextTask.UpdatedAtMS = req.UpdatedAtMS
		nextTask.CompletedAtMS = req.CompletedAtMS
		nextTask.LastError = req.LastError

		nextMeta := meta
		if meta.WriteFenceToken != "" {
			if err := requireActiveChannelMigrationTaskFence(task, meta, req.RuntimeGuard.ExpectedFenceVersion); err != nil {
				return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
			}
			if err := requireMatchingFence(meta, req.RuntimeGuard.ExpectedFenceToken, req.RuntimeGuard.ExpectedFenceVersion, 0, true); err != nil {
				return ChannelMigrationTask{}, ChannelRuntimeMeta{}, err
			}
			nextMeta = clearChannelRuntimeMetaFence(nextMeta)
		} else if task.FenceToken != "" || task.FenceVersion != 0 || task.FenceUntilMS != 0 {
			return ChannelMigrationTask{}, ChannelRuntimeMeta{}, dberrors.ErrConflict
		}
		if task.Kind == ChannelMigrationKindReplicaReplace &&
			canAbortRemoveUnpromotedChannelMigrationLearner(task) &&
			containsUint64(nextMeta.Replicas, task.TargetNode) &&
			!containsUint64(nextMeta.ISR, task.TargetNode) {
			nextMeta.Replicas = removeUint64Member(nextMeta.Replicas, task.TargetNode)
			nextMeta.ChannelEpoch++
		}
		return nextTask, nextMeta, nil
	})
}

type channelMigrationTaskMetaMutator func(ChannelMigrationTask, ChannelRuntimeMeta) (ChannelMigrationTask, ChannelRuntimeMeta, error)

func (b *WriteBatch) stageChannelMigrationTaskAndMeta(hashSlot uint16, guard ChannelMigrationTaskGuard, runtimeGuard ChannelMigrationRuntimeGuard, mutate channelMigrationTaskMetaMutator) error {
	if err := b.ensure(); err != nil {
		return err
	}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: hs}
		taskKey, err := channelMigrationTaskRowKey(hs, guard.ChannelID, guard.ChannelType, guard.TaskID)
		if err != nil {
			return err
		}
		task, ok, err := st.loadChannelMigrationTask(ctx, hs, taskKey, guard.ChannelID, guard.ChannelType, guard.TaskID)
		if err != nil {
			return err
		}
		if !ok {
			return dberrors.ErrNotFound
		}
		metaKey := encodeChannelRuntimeMetaRowKey(hs, runtimeGuard.ChannelID, runtimeGuard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		meta, exists, err := st.loadRuntimeMeta(ctx, hs, metaKey, runtimeGuard.ChannelID, runtimeGuard.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return dberrors.ErrNotFound
		}
		nextTask, nextMeta, err := mutate(task, meta)
		if err != nil {
			return err
		}
		nextMeta = normalizeChannelRuntimeMeta(nextMeta)
		if !guard.matches(task) || !runtimeGuard.matches(meta) {
			if task == nextTask && channelRuntimeMetaEqual(meta, nextMeta) {
				return nil
			}
			return dberrors.ErrConflict
		}
		if task.IsTerminal() && task != nextTask {
			return dberrors.ErrConflict
		}
		if err := validateChannelMigrationTask(nextTask); err != nil {
			return err
		}
		if err := validateChannelRuntimeMeta(nextMeta); err != nil {
			return err
		}
		if err := shard.stageUpsertChannelMigrationTask(ctx, batch, nextTask); err != nil {
			return err
		}
		value, err := channelRuntimeMetaTable.encodeValue(metaKey, nextMeta)
		if err != nil {
			return err
		}
		if err := batch.Set(metaKey, value); err != nil {
			return err
		}
		st.migrationTasks[string(taskKey)] = migrationTaskOverlay{task: nextTask, exists: true}
		st.runtimeMeta[string(metaKey)] = runtimeMetaOverlay{meta: nextMeta, exists: true}
		return nil
	})
	return nil
}

func clearRuntimeFence(meta ChannelRuntimeMeta) ChannelRuntimeMeta {
	meta.WriteFenceToken = ""
	meta.WriteFenceVersion++
	meta.WriteFenceReason = 0
	meta.WriteFenceUntilMS = 0
	return meta
}

func replaceUint64(values []uint64, oldValue, newValue uint64) []uint64 {
	out := append([]uint64(nil), values...)
	for i, value := range out {
		if value == oldValue {
			out[i] = newValue
			return out
		}
	}
	return out
}

func (b *WriteBatch) DeleteTerminalChannelMigrationTasksBefore(hashSlot uint16, req ChannelMigrationTaskGCRequest) (int, error) {
	if err := b.ensure(); err != nil {
		return 0, err
	}
	if err := validateChannelMigrationTaskGCRequest(req); err != nil {
		return 0, err
	}
	hs := HashSlot(hashSlot)
	planned, err := b.db.ForHashSlot(hashSlot).shard.CountTerminalChannelMigrationTasksBefore(context.Background(), req.BeforeMS, req.Limit)
	if err != nil {
		return 0, err
	}
	b.batch.addOp(hs, func(ctx context.Context, st *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: st.db, hashSlot: hs}
		tasks, err := shard.ListChannelMigrationTasks(ctx)
		if err != nil {
			return err
		}
		deleted := 0
		for _, task := range tasks {
			if deleted >= req.Limit {
				break
			}
			if !task.IsTerminal() || task.CompletedAtMS >= req.BeforeMS {
				continue
			}
			taskKey, err := channelMigrationTaskRowKey(hs, task.ChannelID, task.ChannelType, task.TaskID)
			if err != nil {
				return err
			}
			if err := batch.Delete(taskKey); err != nil {
				return err
			}
			if err := batch.Delete(encodeChannelMigrationTerminalIndexKey(hs, task.CompletedAtMS, task.ChannelID, task.ChannelType, task.TaskID)); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return planned, nil
}

func (s *ShardStore) PlanTerminalChannelMigrationTaskGC(ctx context.Context, beforeMS int64, limit int) (ChannelMigrationTaskGCPlan, error) {
	if err := s.validate(); err != nil {
		return ChannelMigrationTaskGCPlan{}, err
	}
	count, err := s.shard.CountTerminalChannelMigrationTasksBefore(ctx, beforeMS, limit)
	return ChannelMigrationTaskGCPlan{TaskCount: count, EntryCount: count}, err
}

func (s *ShardStore) DeleteTerminalChannelMigrationTasksBefore(ctx context.Context, beforeMS int64, limit int) (int, error) {
	wb := s.db.NewWriteBatch()
	defer wb.Close()
	deleted, err := wb.DeleteTerminalChannelMigrationTasksBefore(uint16(s.hashSlot), ChannelMigrationTaskGCRequest{BeforeMS: beforeMS, Limit: limit})
	if err != nil {
		return 0, err
	}
	if err := wb.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *ShardStore) CreateChannelMigrationTask(ctx context.Context, task ChannelMigrationTask) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.CreateChannelMigrationTask(ctx, task)
}

func (s *ShardStore) CreateChannelMigrationTaskWithRuntimeGuard(ctx context.Context, req ChannelMigrationTaskCreate) error {
	if err := s.validate(); err != nil {
		return err
	}
	batch := s.db.meta.NewBatch()
	if err := batch.CreateChannelMigrationTaskWithRuntimeGuard(s.hashSlot, req); err != nil {
		return err
	}
	return batch.Commit(ctx)
}

func (s *ShardStore) GetChannelMigrationTask(ctx context.Context, channelID string, channelType int64, taskID string) (ChannelMigrationTask, error) {
	if err := s.validate(); err != nil {
		return ChannelMigrationTask{}, err
	}
	task, ok, err := s.shard.GetChannelMigrationTask(ctx, channelID, channelType, taskID)
	return task, foundError(ok, err)
}

func (s *ShardStore) GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (ChannelMigrationTask, bool, error) {
	if err := s.validate(); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	return s.shard.GetActiveChannelMigrationTask(ctx, channelID, channelType)
}

func (s *ShardStore) ListChannelMigrationTasks(ctx context.Context) ([]ChannelMigrationTask, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.shard.ListChannelMigrationTasks(ctx)
}

func (s *ShardStore) ClaimChannelMigrationTask(ctx context.Context, req ChannelMigrationTaskClaim) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.ClaimChannelMigrationTask(ctx, req)
}

func (s *ShardStore) AdvanceChannelMigrationTask(ctx context.Context, req ChannelMigrationTaskAdvance) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.shard.AdvanceChannelMigrationTask(ctx, req)
}

func normalizeCompatError(err error) error {
	if errors.Is(err, dberrors.ErrConflict) {
		return ErrStaleMeta
	}
	return err
}
