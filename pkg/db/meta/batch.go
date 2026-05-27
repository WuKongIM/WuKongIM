package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// ChannelRuntimeMetaGuard fences a batch on an observed runtime metadata value.
type ChannelRuntimeMetaGuard struct {
	// ChannelID identifies the guarded channel.
	ChannelID string
	// ChannelType identifies the guarded channel namespace.
	ChannelType int64
	// ExpectedChannelEpoch is the channel epoch that must still be current.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch is the leader epoch that must still be current.
	ExpectedLeaderEpoch uint64
	// ExpectedLeader is the leader that must still be current.
	ExpectedLeader uint64
	// ExpectedRouteGeneration is the route generation that must still be current.
	ExpectedRouteGeneration uint64
	// ExpectedWriteFenceToken is the write fence token that must still be current.
	ExpectedWriteFenceToken string
	// ExpectedWriteFenceVersion is the write fence version that must still be current.
	ExpectedWriteFenceVersion uint64
}

// Batch stages metadata mutations across one or more hash slots.
type Batch struct {
	db              *MetaDB
	ops             []metaBatchOp
	hashSlots       []HashSlot
	hashSlotSeen    map[HashSlot]struct{}
	channelOverlay  map[string]Channel
	migrationActive map[string]struct{}
	closed          bool
	lastLocked      []HashSlot
}

type metaBatchOp struct {
	hashSlot HashSlot
	apply    func(context.Context, *batchCommitState, *engine.Batch) error
}

type batchCommitState struct {
	db               *MetaDB
	tableRows        map[string]tableRowOverlay
	tableCreates     map[string]struct{}
	runtimeMeta      map[string]runtimeMetaOverlay
	migrationTasks   map[string]migrationTaskOverlay
	channelPublishes map[string]Channel
	channelDeletes   map[string]struct{}
}

type tableRowOverlay struct {
	value  []byte
	exists bool
}

type runtimeMetaOverlay struct {
	meta   ChannelRuntimeMeta
	exists bool
}

type migrationTaskOverlay struct {
	task   ChannelMigrationTask
	exists bool
}

// NewBatch creates an atomic metadata batch.
func (db *MetaDB) NewBatch() *Batch {
	return &Batch{db: db}
}

// Close marks the batch unusable and releases staged operations.
func (b *Batch) Close() error {
	if b == nil {
		return nil
	}
	b.closed = true
	b.ops = nil
	b.hashSlots = nil
	b.hashSlotSeen = nil
	b.channelOverlay = nil
	b.migrationActive = nil
	return nil
}

// CreateUser stages a unique user insert.
func (b *Batch) CreateUser(hashSlot HashSlot, user User) error {
	return userTable.StageCreate(b, hashSlot, user)
}

// UpsertUser stages a user upsert.
func (b *Batch) UpsertUser(hashSlot HashSlot, user User) error {
	return userTable.StageUpsert(b, hashSlot, user)
}

// UpsertChannel stages a channel upsert and publishes the channel cache after commit.
func (b *Batch) UpsertChannel(hashSlot HashSlot, channel Channel) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateKeyString(channel.ChannelID); err != nil {
		return err
	}
	primaryKey := encodeChannelRowKey(hashSlot, channel.ChannelID, channel.ChannelType, channelPrimaryFamilyID)
	if b.channelOverlay == nil {
		b.channelOverlay = make(map[string]Channel)
	}
	b.channelOverlay[string(primaryKey)] = channel
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: state.db, hashSlot: hashSlot}
		if err := shard.stageChannel(batch, primaryKey, channel); err != nil {
			return err
		}
		state.channelPublishes[string(primaryKey)] = channel
		delete(state.channelDeletes, string(primaryKey))
		return nil
	})
	return nil
}

// GetChannel reads a staged channel before falling back to committed storage.
func (b *Batch) GetChannel(ctx context.Context, hashSlot HashSlot, channelID string, channelType int64) (Channel, bool, error) {
	if err := b.ensureOpen(); err != nil {
		return Channel{}, false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return Channel{}, false, err
	}
	primaryKey := encodeChannelRowKey(hashSlot, channelID, channelType, channelPrimaryFamilyID)
	if channel, ok := b.channelOverlay[string(primaryKey)]; ok {
		return channel, true, nil
	}
	return b.db.HashSlot(hashSlot).GetChannel(ctx, channelID, channelType)
}

// GuardChannelRuntimeMeta stages a runtime metadata equality guard.
func (b *Batch) GuardChannelRuntimeMeta(hashSlot HashSlot, guard ChannelRuntimeMetaGuard) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateKeyString(guard.ChannelID); err != nil {
		return err
	}
	key := encodeChannelRuntimeMetaRowKey(hashSlot, guard.ChannelID, guard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		meta, exists, err := state.loadRuntimeMeta(ctx, hashSlot, key, guard.ChannelID, guard.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return dberrors.ErrNotFound
		}
		if !guard.matches(meta) {
			return dberrors.ErrConflict
		}
		return nil
	})
	return nil
}

// UpsertChannelRuntimeMeta stages a monotonic runtime metadata upsert.
func (b *Batch) UpsertChannelRuntimeMeta(hashSlot HashSlot, meta ChannelRuntimeMeta) (MonotonicResult, error) {
	if err := b.ensureOpen(); err != nil {
		return 0, err
	}
	if err := validateChannelRuntimeMeta(meta); err != nil {
		return 0, err
	}
	key := encodeChannelRuntimeMetaRowKey(hashSlot, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := state.loadRuntimeMeta(ctx, hashSlot, key, meta.ChannelID, meta.ChannelType)
		if err != nil {
			return err
		}
		next, result := resolveMonotonicChannelRuntimeMeta(existing, exists, meta)
		switch result {
		case MonotonicIgnoredStale:
			return nil
		case MonotonicConflict:
			return dberrors.ErrConflict
		}
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
	return MonotonicApplied, nil
}

// CreateChannelMigrationTask stages a migration task create with active uniqueness.
func (b *Batch) CreateChannelMigrationTask(hashSlot HashSlot, task ChannelMigrationTask) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}
	activeKey := encodeChannelMigrationActiveIndexKey(hashSlot, task.ChannelID, task.ChannelType)
	if task.IsActive() {
		if b.migrationActive == nil {
			b.migrationActive = make(map[string]struct{})
		}
		if _, ok := b.migrationActive[string(activeKey)]; ok {
			return dberrors.ErrAlreadyExists
		}
		b.migrationActive[string(activeKey)] = struct{}{}
	}
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		shard := &Shard{db: state.db, hashSlot: hashSlot}
		if err := shard.stageCreateChannelMigrationTask(ctx, batch, task); err != nil {
			return err
		}
		key, err := channelMigrationTaskRowKey(hashSlot, task.ChannelID, task.ChannelType, task.TaskID)
		if err != nil {
			return err
		}
		state.migrationTasks[string(key)] = migrationTaskOverlay{task: task, exists: true}
		return nil
	})
	return nil
}

// CreateChannelMigrationTaskWithRuntimeGuard stages a guarded migration task create.
func (b *Batch) CreateChannelMigrationTaskWithRuntimeGuard(hashSlot HashSlot, req ChannelMigrationTaskCreate) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(req.Task); err != nil {
		return err
	}
	if err := validateChannelMigrationRuntimeGuard(req.RuntimeGuard); err != nil {
		return err
	}
	key := encodeChannelRuntimeMetaRowKey(hashSlot, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		meta, exists, err := state.loadRuntimeMeta(ctx, hashSlot, key, req.RuntimeGuard.ChannelID, req.RuntimeGuard.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return dberrors.ErrNotFound
		}
		if !req.RuntimeGuard.matches(meta) {
			return dberrors.ErrConflict
		}
		return nil
	})
	return b.CreateChannelMigrationTask(hashSlot, req.Task)
}

// Commit validates and writes all staged operations atomically.
func (b *Batch) Commit(ctx context.Context) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.db == nil || b.db.engine == nil {
		return dberrors.ErrClosed
	}
	unlock := b.db.lockHashSlots(b.hashSlots)
	b.lastLocked = b.db.testLockedOrder()
	defer unlock()

	engineBatch := b.db.engine.NewBatch()
	defer engineBatch.Close()
	state := &batchCommitState{
		db:               b.db,
		tableRows:        make(map[string]tableRowOverlay),
		tableCreates:     make(map[string]struct{}),
		runtimeMeta:      make(map[string]runtimeMetaOverlay),
		migrationTasks:   make(map[string]migrationTaskOverlay),
		channelPublishes: make(map[string]Channel),
		channelDeletes:   make(map[string]struct{}),
	}
	for _, op := range b.ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := op.apply(ctx, state, engineBatch); err != nil {
			return err
		}
	}
	if err := engineBatch.Commit(true); err != nil {
		return err
	}
	for cacheKey, channel := range state.channelPublishes {
		b.db.rememberChannel([]byte(cacheKey), channel)
	}
	for cacheKey := range state.channelDeletes {
		b.db.forgetChannel([]byte(cacheKey))
	}
	b.closed = true
	return nil
}

func (b *Batch) ensureOpen() error {
	if b == nil || b.closed || b.db == nil {
		return dberrors.ErrClosed
	}
	return nil
}

func (b *Batch) addOp(hashSlot HashSlot, apply func(context.Context, *batchCommitState, *engine.Batch) error) {
	if b.hashSlotSeen == nil {
		b.hashSlotSeen = make(map[HashSlot]struct{})
	}
	if _, ok := b.hashSlotSeen[hashSlot]; !ok {
		b.hashSlotSeen[hashSlot] = struct{}{}
		b.hashSlots = append(b.hashSlots, hashSlot)
	}
	b.ops = append(b.ops, metaBatchOp{hashSlot: hashSlot, apply: apply})
}

func (b *Batch) lockedOrderForTest() []HashSlot {
	return append([]HashSlot(nil), b.lastLocked...)
}

func (state *batchCommitState) loadRuntimeMeta(ctx context.Context, hashSlot HashSlot, key []byte, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	if entry, ok := state.runtimeMeta[string(key)]; ok {
		return entry.meta, entry.exists, nil
	}
	shard := &Shard{db: state.db, hashSlot: hashSlot}
	meta, exists, err := shard.getChannelRuntimeMetaByKey(ctx, key, channelID, channelType)
	if err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	state.runtimeMeta[string(key)] = runtimeMetaOverlay{meta: meta, exists: exists}
	return meta, exists, nil
}

func (state *batchCommitState) loadChannelMigrationTask(ctx context.Context, hashSlot HashSlot, key []byte, channelID string, channelType int64, taskID string) (ChannelMigrationTask, bool, error) {
	if entry, ok := state.migrationTasks[string(key)]; ok {
		return entry.task, entry.exists, nil
	}
	shard := &Shard{db: state.db, hashSlot: hashSlot}
	task, exists, err := shard.getChannelMigrationTaskByKey(ctx, key, channelID, channelType, taskID)
	if err != nil {
		return ChannelMigrationTask{}, false, err
	}
	state.migrationTasks[string(key)] = migrationTaskOverlay{task: task, exists: exists}
	return task, exists, nil
}

func (guard ChannelRuntimeMetaGuard) matches(meta ChannelRuntimeMeta) bool {
	return meta.ChannelEpoch == guard.ExpectedChannelEpoch &&
		meta.LeaderEpoch == guard.ExpectedLeaderEpoch &&
		meta.Leader == guard.ExpectedLeader &&
		meta.RouteGeneration == guard.ExpectedRouteGeneration &&
		meta.WriteFenceToken == guard.ExpectedWriteFenceToken &&
		meta.WriteFenceVersion == guard.ExpectedWriteFenceVersion
}
