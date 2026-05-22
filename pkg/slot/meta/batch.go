package meta

import (
	"encoding/binary"
	"errors"

	"github.com/cockroachdb/pebble/v2"
)

// WriteBatch accumulates multiple writes into a single pebble batch,
// committing them atomically with one fsync in Commit.
type WriteBatch struct {
	db                      *DB
	batch                   *pebble.Batch
	writtenUserKeys         map[string]struct{}
	channels                map[string]channelBatchEntry
	userConversationStates  map[string]userConversationStateBatchEntry
	cmdConversationStates   map[string]cmdConversationStateBatchEntry
	pluginUserBindings      map[string]pluginUserBindingBatchEntry
	channelRuntimeMetas     map[string]channelRuntimeMetaBatchEntry
	channelMigrationTasks   map[string]channelMigrationTaskBatchEntry
	channelMigrationActive  map[string]channelMigrationTaskActiveBatchEntry
	channelMigrationCreates map[string]struct{}
	channelMigrationGuards  map[string]channelMigrationTaskGuardBatchEntry
	channelMigrationRuntime map[string]channelMigrationRuntimeGuardBatchEntry
	channelMigrationGCKeys  map[string]struct{}
}

type channelBatchEntry struct {
	channel Channel
	exists  bool
}

type userConversationStateBatchEntry struct {
	state  UserConversationState
	exists bool
}

type cmdConversationStateBatchEntry struct {
	state  CMDConversationState
	exists bool
}

type pluginUserBindingBatchEntry struct {
	binding PluginUserBinding
	exists  bool
}

// channelRuntimeMetaBatchEntry records same-batch existence so deletes are
// visible to later reads before the Pebble batch is committed.
type channelRuntimeMetaBatchEntry struct {
	meta    ChannelRuntimeMeta
	exists  bool
	written bool
}

// channelMigrationTaskBatchEntry records same-batch migration task writes so
// create/upsert validation sees staged primary rows before commit.
type channelMigrationTaskBatchEntry struct {
	task    ChannelMigrationTask
	exists  bool
	written bool
}

// channelMigrationTaskActiveBatchEntry records same-batch active-index writes
// so active uniqueness checks include staged creates and terminal transitions.
type channelMigrationTaskActiveBatchEntry struct {
	taskID         string
	exists         bool
	deleteIfTaskID string
}

// channelMigrationTaskGuardBatchEntry records the first observed state for a
// guarded task mutation and the final idempotent target staged by the batch.
type channelMigrationTaskGuardBatchEntry struct {
	guard   ChannelMigrationTaskGuard
	desired ChannelMigrationTask
}

// channelMigrationRuntimeGuardBatchEntry records runtime metadata expected by
// a migration command and the idempotent target state it stages.
type channelMigrationRuntimeGuardBatchEntry struct {
	guard   ChannelMigrationRuntimeGuard
	desired ChannelRuntimeMeta
}

// NewWriteBatch creates a new WriteBatch. The caller must call Close
// when done, even if Commit is not called.
func (db *DB) NewWriteBatch() *WriteBatch {
	return &WriteBatch{
		db:    db,
		batch: db.db.NewBatch(),
	}
}

// CreateUser encodes and stages a create-only user write into the batch.
// If the user already exists in the database or earlier in the same indexed
// batch, the existing record is preserved and the operation becomes a no-op.
func (b *WriteBatch) CreateUser(hashSlot uint16, u User) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}

	key := encodeUserPrimaryKey(hashSlot, u.UID, userPrimaryFamilyID)
	if b.userKeyWritten(key) {
		return nil
	}
	exists, err := b.db.hasKey(key)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	value := encodeUserFamilyValue(u.Token, u.DeviceFlag, u.DeviceLevel, key)
	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.markUserKeyWritten(key)
	return nil
}

// UpsertUser encodes and stages a user write into the batch.
// No lock is held; the batch is assumed single-threaded.
func (b *WriteBatch) UpsertUser(hashSlot uint16, u User) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}

	key := encodeUserPrimaryKey(hashSlot, u.UID, userPrimaryFamilyID)
	value := encodeUserFamilyValue(u.Token, u.DeviceFlag, u.DeviceLevel, key)
	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.markUserKeyWritten(key)
	return nil
}

// UpsertDevice encodes and stages a device write into the batch.
// No lock is held; the batch is assumed single-threaded.
func (b *WriteBatch) UpsertDevice(hashSlot uint16, d Device) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateDevice(d); err != nil {
		return err
	}

	key := encodeDevicePrimaryKey(hashSlot, d.UID, d.DeviceFlag, devicePrimaryFamilyID)
	value := encodeDeviceFamilyValue(d.Token, d.DeviceLevel, key)
	return b.batch.Set(key, value, nil)
}

// UpsertChannel encodes and stages a channel write (primary + index)
// into the batch. No lock is held.
func (b *WriteBatch) UpsertChannel(hashSlot uint16, ch Channel) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannel(ch); err != nil {
		return err
	}

	primaryKey := encodeChannelPrimaryKey(hashSlot, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
	existing, exists, err := b.loadChannel(hashSlot, primaryKey, ch.ChannelID, ch.ChannelType)
	if err != nil {
		return err
	}
	if exists && ch.SubscriberMutationVersion == 0 {
		ch.SubscriberMutationVersion = existing.SubscriberMutationVersion
	}
	value := encodeChannelFamilyValue(ch.Ban, ch.Disband, ch.SendBan, ch.AllowStranger, ch.SubscriberMutationVersion, primaryKey)
	indexKey := encodeChannelIDIndexKey(hashSlot, ch.ChannelID, ch.ChannelType)
	indexValue := encodeChannelIndexValue(ch.Ban)

	if err := b.batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	b.rememberChannel(hashSlot, ch, true)
	return b.batch.Set(indexKey, indexValue, nil)
}

// DeleteChannel removes the primary record and ID index for a channel.
func (b *WriteBatch) DeleteChannel(hashSlot uint16, channelID string, channelType int64) error {
	primaryKey := encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
	if err := b.batch.Delete(primaryKey, nil); err != nil {
		return err
	}
	indexKey := encodeChannelIDIndexKey(hashSlot, channelID, channelType)
	if err := b.batch.Delete(indexKey, nil); err != nil {
		return err
	}
	subscriberPrefix := encodeSubscriberChannelPrefix(hashSlot, channelID, channelType)
	if err := b.batch.DeleteRange(subscriberPrefix, nextPrefix(subscriberPrefix), nil); err != nil {
		return err
	}
	b.rememberChannel(hashSlot, Channel{ChannelID: channelID, ChannelType: channelType}, false)
	return nil
}

// UpsertChannelRuntimeMeta encodes and stages a runtime metadata write into the batch.
func (b *WriteBatch) UpsertChannelRuntimeMeta(hashSlot uint16, meta ChannelRuntimeMeta) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelRuntimeMeta(meta); err != nil {
		return err
	}

	key := encodeChannelRuntimeMetaPrimaryKey(hashSlot, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	existing, exists, err := b.loadChannelRuntimeMeta(hashSlot, key, meta.ChannelID, meta.ChannelType)
	if err != nil {
		return err
	}
	meta, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, exists, meta)
	if !shouldWrite {
		return nil
	}
	value := encodeChannelRuntimeMetaFamilyValue(meta, key)
	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.rememberChannelRuntimeMeta(hashSlot, meta, true)
	return nil
}

// CreateChannelMigrationTask stages a create-only migration task write.
// The primary row, active index, and terminal index are committed atomically
// with other writes already staged in this batch.
func (b *WriteBatch) CreateChannelMigrationTask(hashSlot uint16, task ChannelMigrationTask) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationTaskPrimaryFamilyID)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		return err
	}
	if exists {
		if existing == task {
			return nil
		}
		return ErrAlreadyExists
	}
	if err := b.upsertChannelMigrationTask(hashSlot, task); err != nil {
		return err
	}
	b.rememberChannelMigrationTaskCreate(primaryKey)
	return nil
}

// CreateChannelMigrationTaskWithRuntimeGuard stages a create-only migration
// task write fenced to the observed channel runtime metadata.
func (b *WriteBatch) CreateChannelMigrationTaskWithRuntimeGuard(hashSlot uint16, req ChannelMigrationTaskCreate) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelMigrationTaskCreate(req); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, req.Task.ChannelID, req.Task.ChannelType, req.Task.TaskID, channelMigrationTaskPrimaryFamilyID)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, req.Task.ChannelID, req.Task.ChannelType, req.Task.TaskID)
	if err != nil {
		return err
	}
	if exists {
		if existing == req.Task {
			return nil
		}
		return ErrAlreadyExists
	}

	metaKey := encodeChannelRuntimeMetaPrimaryKey(hashSlot, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	runtimeMetaWritten := b.isChannelRuntimeMetaWritten(hashSlot, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType)
	meta, exists, err := b.loadChannelRuntimeMeta(hashSlot, metaKey, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if !req.RuntimeGuard.matches(meta) {
		return ErrStaleMeta
	}
	commitGuard := req.RuntimeGuard
	if runtimeMetaWritten {
		preBatchMeta, preBatchExists, err := b.loadChannelRuntimeMetaFromDBLocked(metaKey, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType)
		if err != nil {
			return err
		}
		if !preBatchExists {
			return ErrNotFound
		}
		commitGuard = channelMigrationRuntimeGuardFromMeta(preBatchMeta)
	}
	b.rememberChannelMigrationRuntimeGuard(metaKey, commitGuard, meta)
	return b.CreateChannelMigrationTask(hashSlot, req.Task)
}

func validateChannelMigrationTaskCreate(req ChannelMigrationTaskCreate) error {
	if err := validateChannelMigrationTask(req.Task); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(req.RuntimeGuard); err != nil {
		return err
	}
	if req.Task.ChannelID != req.RuntimeGuard.ChannelID || req.Task.ChannelType != req.RuntimeGuard.ChannelType {
		return ErrInvalidArgument
	}
	return nil
}

func channelMigrationRuntimeGuardFromMeta(meta ChannelRuntimeMeta) ChannelMigrationRuntimeGuard {
	return ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

// ClaimChannelMigrationTask stages a compare-and-set owner claim or renewal.
func (b *WriteBatch) ClaimChannelMigrationTask(hashSlot uint16, req ChannelMigrationTaskClaim) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelMigrationTaskClaim(req); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID, channelMigrationTaskPrimaryFamilyID)
	guardDBState := !b.isChannelMigrationTaskWritten(primaryKey)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	next := applyChannelMigrationTaskClaim(existing, req)
	if existing.IsTerminal() && existing != next {
		return ErrStaleMeta
	}
	if !req.Guard.matches(existing) {
		if existing == next {
			return nil
		}
		return ErrStaleMeta
	}
	if err := validateChannelMigrationTask(next); err != nil {
		return err
	}
	if guardDBState || b.hasChannelMigrationTaskGuard(primaryKey) {
		b.rememberChannelMigrationTaskGuard(primaryKey, req.Guard, next)
	}
	return b.upsertChannelMigrationTask(hashSlot, next)
}

// AdvanceChannelMigrationTask stages a guarded phase/progress update.
func (b *WriteBatch) AdvanceChannelMigrationTask(hashSlot uint16, req ChannelMigrationTaskAdvance) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelMigrationTaskAdvance(req); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID, channelMigrationTaskPrimaryFamilyID)
	guardDBState := !b.isChannelMigrationTaskWritten(primaryKey)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	next := applyChannelMigrationTaskAdvance(existing, req)
	if existing.IsTerminal() && existing != next {
		return ErrStaleMeta
	}
	if !req.Guard.matches(existing) {
		if existing == next {
			return nil
		}
		return ErrStaleMeta
	}
	if err := validateChannelMigrationTask(next); err != nil {
		return err
	}
	if guardDBState || b.hasChannelMigrationTaskGuard(primaryKey) {
		b.rememberChannelMigrationTaskGuard(primaryKey, req.Guard, next)
	}
	return b.upsertChannelMigrationTask(hashSlot, next)
}

// AdvanceChannelRetentionThroughSeq stages a fenced retention-only metadata update.
func (b *WriteBatch) AdvanceChannelRetentionThroughSeq(hashSlot uint16, req ChannelRetentionAdvance) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelRetentionAdvance(req); err != nil {
		return err
	}

	key := encodeChannelRuntimeMetaPrimaryKey(hashSlot, req.ChannelID, req.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	existing, exists, err := b.loadChannelRuntimeMeta(hashSlot, key, req.ChannelID, req.ChannelType)
	if err != nil {
		return err
	}
	next, shouldWrite, err := advanceChannelRetentionThroughSeq(existing, exists, req)
	if err != nil || !shouldWrite {
		return err
	}
	if err := b.batch.Set(key, encodeChannelRuntimeMetaFamilyValue(next, key), nil); err != nil {
		return err
	}
	b.rememberChannelRuntimeMeta(hashSlot, next, true)
	return nil
}

// UpsertUserConversationState encodes and stages a user conversation state write.
func (b *WriteBatch) UpsertUserConversationState(hashSlot uint16, state UserConversationState) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateUserConversationState(state); err != nil {
		return err
	}

	primaryKey := encodeUserConversationStatePrimaryKey(hashSlot, state.UID, state.ChannelType, state.ChannelID, userConversationStatePrimaryFamilyID)
	existing, exists, err := b.loadUserConversationState(hashSlot, primaryKey, state.UID, state.ChannelID, state.ChannelType)
	if err != nil {
		return err
	}
	if exists && state.ActiveAt < existing.ActiveAt {
		state.ActiveAt = existing.ActiveAt
	}
	value := encodeUserConversationStateFamilyValue(state, primaryKey)
	if exists && existing.ActiveAt > 0 && existing.ActiveAt != state.ActiveAt {
		oldIndexKey := encodeUserConversationActiveIndexKey(hashSlot, state.UID, existing.ActiveAt, state.ChannelType, state.ChannelID)
		if err := b.batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := b.batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if state.ActiveAt > 0 {
		indexKey := encodeUserConversationActiveIndexKey(hashSlot, state.UID, state.ActiveAt, state.ChannelType, state.ChannelID)
		if err := b.batch.Set(indexKey, []byte{}, nil); err != nil {
			return err
		}
	}
	b.rememberUserConversationState(primaryKey, state, true)
	return nil
}

// UpsertCMDConversationState encodes and stages a CMD conversation state write.
func (b *WriteBatch) UpsertCMDConversationState(hashSlot uint16, state CMDConversationState) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateCMDConversationState(state); err != nil {
		return err
	}

	primaryKey := encodeCMDConversationStatePrimaryKey(hashSlot, state.UID, state.ChannelType, state.ChannelID, cmdConversationStatePrimaryFamilyID)
	existing, exists, err := b.loadCMDConversationState(hashSlot, primaryKey, state.UID, state.ChannelID, state.ChannelType)
	if err != nil {
		return err
	}
	if exists {
		state = mergeCMDConversationState(existing, state)
	}
	value := encodeCMDConversationStateFamilyValue(state, primaryKey)
	if exists && existing.ActiveAt > 0 && existing.ActiveAt != state.ActiveAt {
		oldIndexKey := encodeCMDConversationActiveIndexKey(hashSlot, state.UID, existing.ActiveAt, state.ChannelType, state.ChannelID)
		if err := b.batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := b.batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if state.ActiveAt > 0 {
		indexKey := encodeCMDConversationActiveIndexKey(hashSlot, state.UID, state.ActiveAt, state.ChannelType, state.ChannelID)
		if err := b.batch.Set(indexKey, []byte{}, nil); err != nil {
			return err
		}
	}
	b.rememberCMDConversationState(primaryKey, state, true)
	return nil
}

// AdvanceCMDConversationReadSeq advances read_seq for each patch without
// creating missing CMD conversation rows.
func (b *WriteBatch) AdvanceCMDConversationReadSeq(hashSlot uint16, patches []CMDConversationReadPatch) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	for _, patch := range patches {
		if err := validateCMDConversationReadPatch(patch); err != nil {
			return err
		}

		primaryKey := encodeCMDConversationStatePrimaryKey(hashSlot, patch.UID, patch.ChannelType, patch.ChannelID, cmdConversationStatePrimaryFamilyID)
		current, exists, err := b.loadCMDConversationState(hashSlot, primaryKey, patch.UID, patch.ChannelID, patch.ChannelType)
		if err != nil {
			return err
		}
		if !exists || patch.ReadSeq <= current.ReadSeq {
			continue
		}
		current.ReadSeq = patch.ReadSeq
		if patch.UpdatedAt > current.UpdatedAt {
			current.UpdatedAt = patch.UpdatedAt
		}
		if err := b.batch.Set(primaryKey, encodeCMDConversationStateFamilyValue(current, primaryKey), nil); err != nil {
			return err
		}
		b.rememberCMDConversationState(primaryKey, current, true)
	}
	return nil
}

// TouchUserConversationActiveAt advances active_at for each patch without
// mutating updated_at or other persisted conversation fields.
func (b *WriteBatch) TouchUserConversationActiveAt(hashSlot uint16, patches []UserConversationActivePatch) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}

	for _, patch := range patches {
		if err := validateConversationUID(patch.UID); err != nil {
			return err
		}
		if err := validateConversationKey(ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}); err != nil {
			return err
		}

		primaryKey := encodeUserConversationStatePrimaryKey(hashSlot, patch.UID, patch.ChannelType, patch.ChannelID, userConversationStatePrimaryFamilyID)
		current, exists, err := b.loadUserConversationState(hashSlot, primaryKey, patch.UID, patch.ChannelID, patch.ChannelType)
		if err != nil {
			return err
		}
		if exists && patch.MessageSeq > 0 && patch.MessageSeq <= current.DeletedToSeq {
			continue
		}
		if exists && patch.ActiveAt <= current.ActiveAt {
			continue
		}

		next := current
		if !exists {
			next = UserConversationState{
				UID:         patch.UID,
				ChannelID:   patch.ChannelID,
				ChannelType: patch.ChannelType,
			}
		}
		next.ActiveAt = patch.ActiveAt

		if exists && current.ActiveAt > 0 {
			oldIndexKey := encodeUserConversationActiveIndexKey(hashSlot, patch.UID, current.ActiveAt, patch.ChannelType, patch.ChannelID)
			if err := b.batch.Delete(oldIndexKey, nil); err != nil {
				return err
			}
		}
		if err := b.batch.Set(primaryKey, encodeUserConversationStateFamilyValue(next, primaryKey), nil); err != nil {
			return err
		}
		if next.ActiveAt > 0 {
			indexKey := encodeUserConversationActiveIndexKey(hashSlot, patch.UID, next.ActiveAt, patch.ChannelType, patch.ChannelID)
			if err := b.batch.Set(indexKey, []byte{}, nil); err != nil {
				return err
			}
		}
		b.rememberUserConversationState(primaryKey, next, true)
	}
	return nil
}

// ClearUserConversationActiveAt zeros active_at for the provided uid-scoped keys
// while preserving updated_at and other conversation fields.
func (b *WriteBatch) ClearUserConversationActiveAt(hashSlot uint16, uid string, keys []ConversationKey) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateConversationUID(uid); err != nil {
		return err
	}

	normalized, err := normalizeConversationKeys(keys)
	if err != nil {
		return err
	}
	for _, key := range normalized {
		primaryKey := encodeUserConversationStatePrimaryKey(hashSlot, uid, key.ChannelType, key.ChannelID, userConversationStatePrimaryFamilyID)
		current, exists, err := b.loadUserConversationState(hashSlot, primaryKey, uid, key.ChannelID, key.ChannelType)
		if err != nil {
			return err
		}
		if !exists || current.ActiveAt <= 0 {
			continue
		}

		oldIndexKey := encodeUserConversationActiveIndexKey(hashSlot, uid, current.ActiveAt, key.ChannelType, key.ChannelID)
		if err := b.batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}

		current.ActiveAt = 0
		if err := b.batch.Set(primaryKey, encodeUserConversationStateFamilyValue(current, primaryKey), nil); err != nil {
			return err
		}
		b.rememberUserConversationState(primaryKey, current, true)
	}
	return nil
}

// HideUserConversation hides a user conversation through DeletedToSeq and
// clears active_at in the same write batch.
func (b *WriteBatch) HideUserConversation(hashSlot uint16, req UserConversationDelete) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateUserConversationDelete(req); err != nil {
		return err
	}

	primaryKey := encodeUserConversationStatePrimaryKey(hashSlot, req.UID, req.ChannelType, req.ChannelID, userConversationStatePrimaryFamilyID)
	current, exists, err := b.loadUserConversationState(hashSlot, primaryKey, req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		return err
	}
	if !exists {
		if req.DeletedToSeq == 0 {
			return nil
		}
		current = UserConversationState{
			UID:         req.UID,
			ChannelID:   req.ChannelID,
			ChannelType: req.ChannelType,
		}
	}
	if req.DeletedToSeq <= current.DeletedToSeq {
		return nil
	}

	next := hideUserConversationState(current, req)
	if exists && current.ActiveAt > 0 {
		oldIndexKey := encodeUserConversationActiveIndexKey(hashSlot, req.UID, current.ActiveAt, req.ChannelType, req.ChannelID)
		if err := b.batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := b.batch.Set(primaryKey, encodeUserConversationStateFamilyValue(next, primaryKey), nil); err != nil {
		return err
	}
	b.rememberUserConversationState(primaryKey, next, true)
	return nil
}

// DeleteChannelRuntimeMeta removes the runtime metadata record for a channel.
func (b *WriteBatch) DeleteChannelRuntimeMeta(hashSlot uint16, channelID string, channelType int64) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateChannelRuntimeMetaChannelID(channelID); err != nil {
		return err
	}
	key := encodeChannelRuntimeMetaPrimaryKey(hashSlot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	if err := b.batch.Delete(key, nil); err != nil {
		return err
	}
	b.rememberChannelRuntimeMeta(hashSlot, ChannelRuntimeMeta{ChannelID: channelID, ChannelType: channelType}, false)
	return nil
}

// AddSubscribers stages subscriber snapshot rows into the batch.
func (b *WriteBatch) AddSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return err
	}

	normalized, err := normalizeSubscriberUIDs(uids)
	if err != nil {
		return err
	}
	version := uint64(0)
	if len(subscriberMutationVersion) > 0 {
		version = subscriberMutationVersion[0]
	}
	var channel Channel
	var exists bool
	if version > 0 {
		channelKey := encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
		channel, exists, err = b.loadChannel(hashSlot, channelKey, channelID, channelType)
		if err != nil {
			return err
		}
		if exists && channel.SubscriberMutationVersion > version {
			return ErrStaleMeta
		}
		if !exists {
			channel = Channel{ChannelID: channelID, ChannelType: channelType}
		}
		channel.SubscriberMutationVersion = version
	}
	for _, uid := range normalized {
		key := encodeSubscriberPrimaryKey(hashSlot, channelID, channelType, uid, subscriberPrimaryFamilyID)
		if err := b.batch.Set(key, wrapFamilyValue(key, nil), nil); err != nil {
			return err
		}
	}
	if version > 0 {
		channelKey := encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
		value := encodeChannelFamilyValue(channel.Ban, channel.Disband, channel.SendBan, channel.AllowStranger, channel.SubscriberMutationVersion, channelKey)
		if err := b.batch.Set(channelKey, value, nil); err != nil {
			return err
		}
		b.rememberChannel(hashSlot, channel, true)
	}
	return nil
}

// RemoveSubscribers stages subscriber snapshot row deletions into the batch.
func (b *WriteBatch) RemoveSubscribers(hashSlot uint16, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return err
	}

	normalized, err := normalizeSubscriberUIDs(uids)
	if err != nil {
		return err
	}
	version := uint64(0)
	if len(subscriberMutationVersion) > 0 {
		version = subscriberMutationVersion[0]
	}
	var channel Channel
	var exists bool
	if version > 0 {
		channelKey := encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
		channel, exists, err = b.loadChannel(hashSlot, channelKey, channelID, channelType)
		if err != nil {
			return err
		}
		if exists && channel.SubscriberMutationVersion > version {
			return ErrStaleMeta
		}
		if !exists {
			channel = Channel{ChannelID: channelID, ChannelType: channelType}
		}
		channel.SubscriberMutationVersion = version
	}
	for _, uid := range normalized {
		key := encodeSubscriberPrimaryKey(hashSlot, channelID, channelType, uid, subscriberPrimaryFamilyID)
		if err := b.batch.Delete(key, nil); err != nil {
			return err
		}
	}
	if version > 0 {
		channelKey := encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
		value := encodeChannelFamilyValue(channel.Ban, channel.Disband, channel.SendBan, channel.AllowStranger, channel.SubscriberMutationVersion, channelKey)
		if err := b.batch.Set(channelKey, value, nil); err != nil {
			return err
		}
		b.rememberChannel(hashSlot, channel, true)
	}
	return nil
}

// BindPluginUser stages an idempotent plugin binding write.
func (b *WriteBatch) BindPluginUser(hashSlot uint16, binding PluginUserBinding) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validatePluginUserBinding(binding); err != nil {
		return err
	}

	primaryKey := encodePluginUserBindingPrimaryKey(hashSlot, binding.UID, binding.PluginNo, pluginUserBindingPrimaryFamilyID)
	existing, exists, err := b.loadPluginUserBinding(hashSlot, primaryKey, binding.UID, binding.PluginNo)
	if err != nil {
		return err
	}
	if exists {
		binding.CreatedAtMS = existing.CreatedAtMS
		if binding.UpdatedAtMS < existing.UpdatedAtMS {
			binding.UpdatedAtMS = existing.UpdatedAtMS
		}
	}
	if err := stageBindPluginUser(b.batch, hashSlot, binding); err != nil {
		return err
	}
	b.rememberPluginUserBinding(primaryKey, binding, true)
	return nil
}

// UnbindPluginUser stages an idempotent plugin binding deletion.
func (b *WriteBatch) UnbindPluginUser(hashSlot uint16, uid, pluginNo string) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
		return err
	}

	primaryKey := encodePluginUserBindingPrimaryKey(hashSlot, uid, pluginNo, pluginUserBindingPrimaryFamilyID)
	if err := stageUnbindPluginUser(b.batch, hashSlot, uid, pluginNo); err != nil {
		return err
	}
	b.rememberPluginUserBinding(primaryKey, PluginUserBinding{}, false)
	return nil
}

// Commit atomically writes all staged operations with a single fsync.
func (b *WriteBatch) Commit() error {
	b.db.mu.Lock()
	defer b.db.mu.Unlock()

	if err := b.enforceChannelRuntimeMetaMonotonicLocked(); err != nil {
		return err
	}
	if err := b.enforceChannelMigrationCreatePrimaryUniqueLocked(); err != nil {
		return err
	}
	if err := b.enforceChannelMigrationGuardedWritesLocked(); err != nil {
		return err
	}
	if err := b.enforceChannelMigrationRuntimeGuardsLocked(); err != nil {
		return err
	}
	if err := b.enforceChannelMigrationActiveUniqueLocked(); err != nil {
		return err
	}
	if err := b.applyChannelMigrationActiveIndexWritesLocked(); err != nil {
		return err
	}
	if err := b.batch.Commit(pebble.Sync); err != nil {
		return err
	}
	b.applyChannelCacheWritesLocked()
	return nil
}

// Close releases the batch resources. Safe to call after Commit.
func (b *WriteBatch) Close() {
	if b.batch != nil {
		_ = b.batch.Close()
	}
}

func (b *WriteBatch) markUserKeyWritten(key []byte) {
	if b.writtenUserKeys == nil {
		b.writtenUserKeys = make(map[string]struct{}, 1)
	}
	b.writtenUserKeys[string(key)] = struct{}{}
}

func (b *WriteBatch) userKeyWritten(key []byte) bool {
	if b.writtenUserKeys == nil {
		return false
	}
	_, ok := b.writtenUserKeys[string(key)]
	return ok
}

func (b *WriteBatch) loadChannelRuntimeMeta(hashSlot uint16, key []byte, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	batchKey := channelRuntimeMetaBatchKey(hashSlot, channelID, channelType)
	if b.channelRuntimeMetas != nil {
		if entry, ok := b.channelRuntimeMetas[batchKey]; ok {
			return entry.meta, entry.exists, nil
		}
	}
	value, err := b.db.getValue(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelRuntimeMeta{}, false, nil
		}
		return ChannelRuntimeMeta{}, false, err
	}
	meta, err := decodeChannelRuntimeMetaFamilyValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, true, nil
}

func (b *WriteBatch) rememberChannelRuntimeMeta(hashSlot uint16, meta ChannelRuntimeMeta, exists bool) {
	if b.channelRuntimeMetas == nil {
		b.channelRuntimeMetas = make(map[string]channelRuntimeMetaBatchEntry, 1)
	}
	b.channelRuntimeMetas[channelRuntimeMetaBatchKey(hashSlot, meta.ChannelID, meta.ChannelType)] = channelRuntimeMetaBatchEntry{
		meta:    meta,
		exists:  exists,
		written: true,
	}
}

func (b *WriteBatch) loadChannel(hashSlot uint16, key []byte, channelID string, channelType int64) (Channel, bool, error) {
	batchKey := channelBatchKey(hashSlot, channelID, channelType)
	if b.channels != nil {
		if entry, ok := b.channels[batchKey]; ok {
			return entry.channel, entry.exists, nil
		}
	}

	value, err := b.db.getValue(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Channel{}, false, nil
		}
		return Channel{}, false, err
	}

	ban, disband, sendBan, allowStranger, version, err := decodeChannelFamilyValue(key, value)
	if err != nil {
		return Channel{}, false, err
	}
	return Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		Ban:                       ban,
		Disband:                   disband,
		SendBan:                   sendBan,
		AllowStranger:             allowStranger,
		SubscriberMutationVersion: version,
	}, true, nil
}

func (b *WriteBatch) rememberChannel(hashSlot uint16, ch Channel, exists bool) {
	if b.channels == nil {
		b.channels = make(map[string]channelBatchEntry, 1)
	}
	b.channels[channelBatchKey(hashSlot, ch.ChannelID, ch.ChannelType)] = channelBatchEntry{
		channel: ch,
		exists:  exists,
	}
}

func (b *WriteBatch) applyChannelCacheWritesLocked() {
	for key, entry := range b.channels {
		if entry.exists {
			b.db.rememberChannelCacheKeyLocked(key, entry.channel)
			continue
		}
		b.db.forgetChannelCacheKeyLocked(key)
	}
}

func channelBatchKey(hashSlot uint16, channelID string, channelType int64) string {
	return string(encodeChannelPrimaryKey(hashSlot, channelID, channelType, channelPrimaryFamilyID))
}

func channelRuntimeMetaBatchKey(hashSlot uint16, channelID string, channelType int64) string {
	return string(encodeChannelRuntimeMetaPrimaryKey(hashSlot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID))
}

func (b *WriteBatch) upsertChannelMigrationTask(hashSlot uint16, task ChannelMigrationTask) error {
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationTaskPrimaryFamilyID)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		return err
	}

	activeIndexKey := encodeChannelMigrationTaskActiveIndexKey(hashSlot, task.ChannelID, task.ChannelType)
	if task.IsActive() {
		if err := b.ensureChannelMigrationTaskActiveSlotAvailable(hashSlot, task); err != nil {
			return err
		}
		b.rememberChannelMigrationActiveIndex(activeIndexKey, task.TaskID, true)
	} else if exists && existing.IsActive() {
		b.rememberChannelMigrationActiveIndexDelete(activeIndexKey, existing.TaskID)
	}

	if exists && existing.IsTerminal() {
		oldTerminalIndexKey := encodeChannelMigrationTaskTerminalIndexKey(hashSlot, existing.CompletedAtMS, existing.ChannelID, existing.ChannelType, existing.TaskID)
		if !task.IsTerminal() || existing.CompletedAtMS != task.CompletedAtMS {
			if err := b.batch.Delete(oldTerminalIndexKey, nil); err != nil {
				return err
			}
		}
	}
	if task.IsTerminal() {
		terminalIndexKey := encodeChannelMigrationTaskTerminalIndexKey(hashSlot, task.CompletedAtMS, task.ChannelID, task.ChannelType, task.TaskID)
		if err := b.batch.Set(terminalIndexKey, nil, nil); err != nil {
			return err
		}
	}

	if err := b.batch.Set(primaryKey, encodeChannelMigrationTaskFamilyValue(task, primaryKey), nil); err != nil {
		return err
	}
	b.rememberChannelMigrationTaskWrite(primaryKey, task)
	return nil
}

func (b *WriteBatch) ensureChannelMigrationTaskActiveSlotAvailable(hashSlot uint16, task ChannelMigrationTask) error {
	activeIndexKey := encodeChannelMigrationTaskActiveIndexKey(hashSlot, task.ChannelID, task.ChannelType)
	existingTaskID, exists, err := b.loadChannelMigrationActiveIndex(activeIndexKey)
	if err != nil || !exists {
		return err
	}
	if existingTaskID == task.TaskID {
		return nil
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, task.ChannelID, task.ChannelType, existingTaskID, channelMigrationTaskPrimaryFamilyID)
	existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, task.ChannelID, task.ChannelType, existingTaskID)
	if err != nil || !exists {
		return err
	}
	if existing.IsActive() {
		return ErrAlreadyExists
	}
	return nil
}

func (b *WriteBatch) loadChannelMigrationTask(hashSlot uint16, primaryKey []byte, channelID string, channelType int64, taskID string) (ChannelMigrationTask, bool, error) {
	if b.channelMigrationTasks != nil {
		if entry, ok := b.channelMigrationTasks[string(primaryKey)]; ok {
			return entry.task, entry.exists, nil
		}
	}

	task, exists, err := b.loadChannelMigrationTaskFromDB(primaryKey, channelID, channelType, taskID)
	if err != nil {
		return ChannelMigrationTask{}, false, err
	}
	b.rememberChannelMigrationTask(primaryKey, task, exists)
	return task, exists, nil
}

func (b *WriteBatch) loadChannelMigrationTaskFromDB(primaryKey []byte, channelID string, channelType int64, taskID string) (ChannelMigrationTask, bool, error) {
	value, err := b.db.getValue(primaryKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelMigrationTask{}, false, nil
		}
		return ChannelMigrationTask{}, false, err
	}
	task, err := decodeChannelMigrationTaskFamilyValue(primaryKey, value)
	if err != nil {
		return ChannelMigrationTask{}, false, err
	}
	task.ChannelID = channelID
	task.ChannelType = channelType
	task.TaskID = taskID
	if err := validateDecodedChannelMigrationTask(task); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	return task, true, nil
}

func (b *WriteBatch) rememberChannelMigrationTask(primaryKey []byte, task ChannelMigrationTask, exists bool) {
	if b.channelMigrationTasks == nil {
		b.channelMigrationTasks = make(map[string]channelMigrationTaskBatchEntry, 1)
	}
	b.channelMigrationTasks[string(primaryKey)] = channelMigrationTaskBatchEntry{
		task:    task,
		exists:  exists,
		written: false,
	}
}

func (b *WriteBatch) rememberChannelMigrationTaskWrite(primaryKey []byte, task ChannelMigrationTask) {
	if b.channelMigrationTasks == nil {
		b.channelMigrationTasks = make(map[string]channelMigrationTaskBatchEntry, 1)
	}
	b.channelMigrationTasks[string(primaryKey)] = channelMigrationTaskBatchEntry{
		task:    task,
		exists:  true,
		written: true,
	}
}

func (b *WriteBatch) isChannelMigrationTaskWritten(primaryKey []byte) bool {
	if b.channelMigrationTasks == nil {
		return false
	}
	entry, ok := b.channelMigrationTasks[string(primaryKey)]
	return ok && entry.written
}

func (b *WriteBatch) hasChannelMigrationTaskGuard(primaryKey []byte) bool {
	if b.channelMigrationGuards == nil {
		return false
	}
	_, ok := b.channelMigrationGuards[string(primaryKey)]
	return ok
}

func (b *WriteBatch) rememberChannelMigrationTaskGuard(primaryKey []byte, guard ChannelMigrationTaskGuard, desired ChannelMigrationTask) {
	if b.channelMigrationGuards == nil {
		b.channelMigrationGuards = make(map[string]channelMigrationTaskGuardBatchEntry, 1)
	}
	key := string(primaryKey)
	if existing, ok := b.channelMigrationGuards[key]; ok {
		existing.desired = desired
		b.channelMigrationGuards[key] = existing
		return
	}
	b.channelMigrationGuards[key] = channelMigrationTaskGuardBatchEntry{
		guard:   guard,
		desired: desired,
	}
}

func (b *WriteBatch) isChannelRuntimeMetaWritten(hashSlot uint16, channelID string, channelType int64) bool {
	if b.channelRuntimeMetas == nil {
		return false
	}
	entry, ok := b.channelRuntimeMetas[channelRuntimeMetaBatchKey(hashSlot, channelID, channelType)]
	return ok && entry.written
}

func (b *WriteBatch) hasChannelMigrationRuntimeGuard(primaryKey []byte) bool {
	if b.channelMigrationRuntime == nil {
		return false
	}
	_, ok := b.channelMigrationRuntime[string(primaryKey)]
	return ok
}

func (b *WriteBatch) rememberChannelMigrationRuntimeGuard(primaryKey []byte, guard ChannelMigrationRuntimeGuard, desired ChannelRuntimeMeta) {
	if b.channelMigrationRuntime == nil {
		b.channelMigrationRuntime = make(map[string]channelMigrationRuntimeGuardBatchEntry, 1)
	}
	key := string(primaryKey)
	if existing, ok := b.channelMigrationRuntime[key]; ok {
		existing.desired = normalizeChannelRuntimeMeta(desired)
		b.channelMigrationRuntime[key] = existing
		return
	}
	b.channelMigrationRuntime[key] = channelMigrationRuntimeGuardBatchEntry{
		guard:   guard,
		desired: normalizeChannelRuntimeMeta(desired),
	}
}

func (b *WriteBatch) rememberChannelMigrationTaskCreate(primaryKey []byte) {
	if b.channelMigrationCreates == nil {
		b.channelMigrationCreates = make(map[string]struct{}, 1)
	}
	b.channelMigrationCreates[string(primaryKey)] = struct{}{}
}

func (b *WriteBatch) loadChannelMigrationActiveIndex(activeIndexKey []byte) (string, bool, error) {
	if b.channelMigrationActive != nil {
		if entry, ok := b.channelMigrationActive[string(activeIndexKey)]; ok {
			return entry.taskID, entry.exists, nil
		}
	}

	taskID, err := decodeChannelMigrationTaskIDIndexValue(b.db.getValue(activeIndexKey))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			b.rememberChannelMigrationActiveIndex(activeIndexKey, "", false)
			return "", false, nil
		}
		return "", false, err
	}
	b.rememberChannelMigrationActiveIndex(activeIndexKey, taskID, true)
	return taskID, true, nil
}

func (b *WriteBatch) rememberChannelMigrationActiveIndex(activeIndexKey []byte, taskID string, exists bool) {
	if b.channelMigrationActive == nil {
		b.channelMigrationActive = make(map[string]channelMigrationTaskActiveBatchEntry, 1)
	}
	b.channelMigrationActive[string(activeIndexKey)] = channelMigrationTaskActiveBatchEntry{
		taskID: taskID,
		exists: exists,
	}
}

func (b *WriteBatch) rememberChannelMigrationActiveIndexDelete(activeIndexKey []byte, taskID string) {
	if b.channelMigrationActive == nil {
		b.channelMigrationActive = make(map[string]channelMigrationTaskActiveBatchEntry, 1)
	}
	b.channelMigrationActive[string(activeIndexKey)] = channelMigrationTaskActiveBatchEntry{
		exists:         false,
		deleteIfTaskID: taskID,
	}
}

// enforceChannelMigrationCreatePrimaryUniqueLocked rechecks create-only
// primary keys after this batch has acquired db.mu so concurrent batches cannot
// overwrite a task that was created after staging.
func (b *WriteBatch) enforceChannelMigrationCreatePrimaryUniqueLocked() error {
	if len(b.channelMigrationCreates) == 0 {
		return nil
	}
	for primaryKeyString := range b.channelMigrationCreates {
		exists, err := b.db.hasKey([]byte(primaryKeyString))
		if err != nil {
			return err
		}
		if exists {
			return ErrAlreadyExists
		}
	}
	return nil
}

// enforceChannelMigrationGuardedWritesLocked rechecks compare-and-set task
// mutations after db.mu is held. Duplicate commands whose desired task state
// is already durable are accepted as idempotent no-ops.
func (b *WriteBatch) enforceChannelMigrationGuardedWritesLocked() error {
	if len(b.channelMigrationGuards) == 0 {
		return nil
	}
	for primaryKeyString, entry := range b.channelMigrationGuards {
		primaryKey := []byte(primaryKeyString)
		current, exists, err := b.loadChannelMigrationTaskFromDB(primaryKey, entry.guard.ChannelID, entry.guard.ChannelType, entry.guard.TaskID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrStaleMeta
		}
		if entry.guard.matches(current) || current == entry.desired {
			continue
		}
		return ErrStaleMeta
	}
	return nil
}

// enforceChannelMigrationRuntimeGuardsLocked rechecks runtime metadata guards
// for migration commands after db.mu is held.
func (b *WriteBatch) enforceChannelMigrationRuntimeGuardsLocked() error {
	if len(b.channelMigrationRuntime) == 0 {
		return nil
	}
	for primaryKeyString, entry := range b.channelMigrationRuntime {
		primaryKey := []byte(primaryKeyString)
		current, exists, err := b.loadChannelRuntimeMetaFromDBLocked(primaryKey, entry.guard.ChannelID, entry.guard.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return ErrStaleMeta
		}
		if entry.guard.matches(current) || channelRuntimeMetaEqual(current, entry.desired) {
			continue
		}
		return ErrStaleMeta
	}
	return nil
}

// enforceChannelMigrationActiveUniqueLocked rechecks staged active writes after
// this batch has acquired db.mu so concurrent batches cannot race uniqueness.
func (b *WriteBatch) enforceChannelMigrationActiveUniqueLocked() error {
	if len(b.channelMigrationActive) == 0 {
		return nil
	}
	for activeIndexKeyString, entry := range b.channelMigrationActive {
		if !entry.exists {
			continue
		}
		activeIndexKey := []byte(activeIndexKeyString)
		hashSlot, channelID, channelType, err := decodeChannelMigrationTaskActiveIndexKey(activeIndexKey)
		if err != nil {
			return err
		}
		existingTaskID, err := decodeChannelMigrationTaskIDIndexValue(b.db.getValue(activeIndexKey))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		if existingTaskID == entry.taskID {
			continue
		}

		primaryKey := encodeChannelMigrationTaskPrimaryKey(hashSlot, channelID, channelType, existingTaskID, channelMigrationTaskPrimaryFamilyID)
		existing, exists, err := b.loadChannelMigrationTask(hashSlot, primaryKey, channelID, channelType, existingTaskID)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if existing.IsActive() {
			return ErrAlreadyExists
		}
	}
	return nil
}

// applyChannelMigrationActiveIndexWritesLocked applies active-index updates
// after commit-time revalidation. Terminal transitions delete the active index
// only when it still points at the terminal task staged by this batch.
func (b *WriteBatch) applyChannelMigrationActiveIndexWritesLocked() error {
	if len(b.channelMigrationActive) == 0 {
		return nil
	}
	for activeIndexKeyString, entry := range b.channelMigrationActive {
		activeIndexKey := []byte(activeIndexKeyString)
		if entry.exists {
			if err := b.batch.Set(activeIndexKey, []byte(entry.taskID), nil); err != nil {
				return err
			}
			continue
		}
		if entry.deleteIfTaskID == "" {
			continue
		}
		existingTaskID, err := decodeChannelMigrationTaskIDIndexValue(b.db.getValue(activeIndexKey))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		if existingTaskID != entry.deleteIfTaskID {
			continue
		}
		if err := b.batch.Delete(activeIndexKey, nil); err != nil {
			return err
		}
	}
	return nil
}

func decodeChannelMigrationTaskActiveIndexKey(key []byte) (uint16, string, int64, error) {
	prefixLen := 1 + 2 + 4 + 2
	if len(key) < prefixLen || key[0] != keyspaceIndex {
		return 0, "", 0, ErrCorruptValue
	}
	if binary.BigEndian.Uint32(key[1+2:1+2+4]) != TableIDChannelMigrationTask {
		return 0, "", 0, ErrCorruptValue
	}
	if binary.BigEndian.Uint16(key[1+2+4:prefixLen]) != channelMigrationTaskActiveIndexID {
		return 0, "", 0, ErrCorruptValue
	}
	hashSlot := binary.BigEndian.Uint16(key[1 : 1+2])
	rest := key[prefixLen:]
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return 0, "", 0, err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return 0, "", 0, err
	}
	if len(rest) != 0 {
		return 0, "", 0, ErrCorruptValue
	}
	return hashSlot, channelID, channelType, nil
}

func (b *WriteBatch) enforceChannelRuntimeMetaMonotonicLocked() error {
	if len(b.channelRuntimeMetas) == 0 {
		return nil
	}
	for keyString, entry := range b.channelRuntimeMetas {
		if !entry.exists {
			continue
		}
		candidate := entry.meta
		key := []byte(keyString)
		existing, exists, err := b.loadChannelRuntimeMetaFromDBLocked(key, candidate.ChannelID, candidate.ChannelType)
		if err != nil {
			return err
		}
		next, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, exists, candidate)
		if !shouldWrite && exists {
			next = existing
		}
		if !shouldWrite && !exists {
			continue
		}
		if err := b.batch.Set(key, encodeChannelRuntimeMetaFamilyValue(next, key), nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *WriteBatch) loadChannelRuntimeMetaFromDBLocked(key []byte, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	value, err := b.db.getValue(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelRuntimeMeta{}, false, nil
		}
		return ChannelRuntimeMeta{}, false, err
	}
	meta, err := decodeChannelRuntimeMetaFamilyValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, true, nil
}

func (b *WriteBatch) loadUserConversationState(hashSlot uint16, primaryKey []byte, uid, channelID string, channelType int64) (UserConversationState, bool, error) {
	if b.userConversationStates != nil {
		if entry, ok := b.userConversationStates[string(primaryKey)]; ok {
			return entry.state, entry.exists, nil
		}
	}

	shard := b.db.ForHashSlot(hashSlot)
	state, err := shard.getUserConversationStateLocked(uid, channelID, channelType)
	switch err {
	case nil:
		b.rememberUserConversationState(primaryKey, state, true)
		return state, true, nil
	case ErrNotFound:
		b.rememberUserConversationState(primaryKey, UserConversationState{}, false)
		return UserConversationState{}, false, nil
	default:
		return UserConversationState{}, false, err
	}
}

func (b *WriteBatch) rememberUserConversationState(primaryKey []byte, state UserConversationState, exists bool) {
	if b.userConversationStates == nil {
		b.userConversationStates = make(map[string]userConversationStateBatchEntry, 1)
	}
	b.userConversationStates[string(primaryKey)] = userConversationStateBatchEntry{
		state:  state,
		exists: exists,
	}
}

func (b *WriteBatch) loadPluginUserBinding(hashSlot uint16, primaryKey []byte, uid, pluginNo string) (PluginUserBinding, bool, error) {
	if b.pluginUserBindings != nil {
		if entry, ok := b.pluginUserBindings[string(primaryKey)]; ok {
			return entry.binding, entry.exists, nil
		}
	}

	shard := b.db.ForHashSlot(hashSlot)
	binding, err := shard.getPluginUserBindingLocked(uid, pluginNo)
	switch err {
	case nil:
		b.rememberPluginUserBinding(primaryKey, binding, true)
		return binding, true, nil
	case ErrNotFound:
		b.rememberPluginUserBinding(primaryKey, PluginUserBinding{}, false)
		return PluginUserBinding{}, false, nil
	default:
		return PluginUserBinding{}, false, err
	}
}

func (b *WriteBatch) rememberPluginUserBinding(primaryKey []byte, binding PluginUserBinding, exists bool) {
	if b.pluginUserBindings == nil {
		b.pluginUserBindings = make(map[string]pluginUserBindingBatchEntry, 1)
	}
	b.pluginUserBindings[string(primaryKey)] = pluginUserBindingBatchEntry{
		binding: binding,
		exists:  exists,
	}
}

func (b *WriteBatch) loadCMDConversationState(hashSlot uint16, primaryKey []byte, uid, channelID string, channelType int64) (CMDConversationState, bool, error) {
	if b.cmdConversationStates != nil {
		if entry, ok := b.cmdConversationStates[string(primaryKey)]; ok {
			return entry.state, entry.exists, nil
		}
	}

	shard := b.db.ForHashSlot(hashSlot)
	state, err := shard.getCMDConversationStateLocked(uid, channelID, channelType)
	switch err {
	case nil:
		b.rememberCMDConversationState(primaryKey, state, true)
		return state, true, nil
	case ErrNotFound:
		b.rememberCMDConversationState(primaryKey, CMDConversationState{}, false)
		return CMDConversationState{}, false, nil
	default:
		return CMDConversationState{}, false, err
	}
}

func (b *WriteBatch) rememberCMDConversationState(primaryKey []byte, state CMDConversationState, exists bool) {
	if b.cmdConversationStates == nil {
		b.cmdConversationStates = make(map[string]cmdConversationStateBatchEntry, 1)
	}
	b.cmdConversationStates[string(primaryKey)] = cmdConversationStateBatchEntry{
		state:  state,
		exists: exists,
	}
}
