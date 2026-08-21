package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	personDirectoryTaskColumnChannelID     uint16 = 1
	personDirectoryTaskColumnChannelType   uint16 = 2
	personDirectoryTaskColumnCommittedTail uint16 = 3
	personDirectoryTaskColumnCreatedAt     uint16 = 4
	personDirectoryTaskColumnGeneration    uint16 = 5

	personDirectoryTaskPrimaryFamilyID uint16 = 0
	personDirectoryTaskPrimaryIndexID  uint16 = 1
)

// PersonDirectoryTask is one durable pending UID-directory projection for a
// canonical person channel. The row has no running or retry state; current
// Slot leadership owns execution and retries are idempotent.
type PersonDirectoryTask struct {
	ChannelID     string
	ChannelType   int64
	CommittedTail uint64
	CreatedAt     int64
	// Generation is the durable source incarnation this projection belongs to.
	Generation uint64
}

// PersonDirectoryTaskCursor identifies the last emitted pending task.
type PersonDirectoryTaskCursor struct {
	ChannelID   string
	ChannelType int64
}

// PersonDirectoryTaskLocation identifies one task at its source hash slot.
// It contains no UID payload; completion derives all ownership from the
// canonical Channel key and current Slot routing.
type PersonDirectoryTaskLocation struct {
	HashSlot    HashSlot
	ChannelID   string
	ChannelType int64
	Generation  uint64
}

var personDirectoryTaskTable = registerMetaTable(TableSpec[PersonDirectoryTask]{
	ID:   TableIDPersonDirectoryTask,
	Name: "person_directory_task",
	Columns: []schema.Column{
		{ID: personDirectoryTaskColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: personDirectoryTaskColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: personDirectoryTaskColumnCommittedTail, Name: "committed_tail", Type: schema.TypeUint64},
		{ID: personDirectoryTaskColumnCreatedAt, Name: "created_at", Type: schema.TypeInt64},
		{ID: personDirectoryTaskColumnGeneration, Name: "generation", Type: schema.TypeUint64},
	},
	Families: []schema.Family{{ID: personDirectoryTaskPrimaryFamilyID, Name: "primary", Columns: []uint16{
		personDirectoryTaskColumnCommittedTail,
		personDirectoryTaskColumnCreatedAt,
		personDirectoryTaskColumnGeneration,
	}}},
	Primary: PrimarySpec[PersonDirectoryTask]{
		IndexID:  personDirectoryTaskPrimaryIndexID,
		FamilyID: personDirectoryTaskPrimaryFamilyID,
		Name:     "pk_person_directory_task",
		Columns:  []uint16{personDirectoryTaskColumnChannelID, personDirectoryTaskColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered},
		Key: func(task PersonDirectoryTask) KeyParts {
			return personDirectoryTaskPrimaryKey(task.ChannelID, task.ChannelType)
		},
	},
	Validate: func(task PersonDirectoryTask) error {
		if task.CreatedAt < 0 || task.Generation == 0 {
			return dberrors.ErrInvalidArgument
		}
		return validatePersonDirectoryKey(ChannelKey{ChannelID: task.ChannelID, ChannelType: task.ChannelType})
	},
	EncodeValue: func(task PersonDirectoryTask) ([]byte, error) {
		value := appendValueUint64(nil, task.CommittedTail)
		value = appendValueInt64(value, task.CreatedAt)
		return appendValueUint64(value, task.Generation), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (PersonDirectoryTask, error) {
		committedTail, rest, err := readValueUint64(value)
		if err != nil {
			return PersonDirectoryTask{}, dberrors.ErrCorruptValue
		}
		createdAt, rest, err := readValueInt64(rest)
		if err != nil {
			return PersonDirectoryTask{}, dberrors.ErrCorruptValue
		}
		generation, rest, err := readValueUint64(rest)
		if err != nil || len(rest) != 0 || createdAt < 0 || generation == 0 {
			return PersonDirectoryTask{}, dberrors.ErrCorruptValue
		}
		return PersonDirectoryTask{ChannelID: primary[0].S, ChannelType: primary[1].I64, CommittedTail: committedTail, CreatedAt: createdAt, Generation: generation}, nil
	},
})

// PersonDirectoryTaskTable describes the durable pending projection table.
var PersonDirectoryTaskTable = personDirectoryTaskTable.Schema()

// GetPersonDirectoryTask returns one pending projection task.
func (s *Shard) GetPersonDirectoryTask(ctx context.Context, channelID string, channelType int64) (PersonDirectoryTask, bool, error) {
	if err := s.check(ctx); err != nil {
		return PersonDirectoryTask{}, false, err
	}
	if err := validatePersonDirectoryKey(ChannelKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return PersonDirectoryTask{}, false, err
	}
	return personDirectoryTaskTable.Get(ctx, s, personDirectoryTaskPrimaryKey(channelID, channelType))
}

// ListPersonDirectoryTaskPage returns pending tasks in channel key order.
func (s *Shard) ListPersonDirectoryTaskPage(ctx context.Context, after PersonDirectoryTaskCursor, limit int) ([]PersonDirectoryTask, PersonDirectoryTaskCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, PersonDirectoryTaskCursor{}, false, err
	}
	if err := validatePersonDirectoryTaskCursor(after); err != nil {
		return nil, PersonDirectoryTaskCursor{}, false, err
	}
	if err := validatePageLimit(limit); err != nil {
		return nil, PersonDirectoryTaskCursor{}, false, err
	}
	var cursor KeyParts
	if after != (PersonDirectoryTaskCursor{}) {
		cursor = personDirectoryTaskPrimaryKey(after.ChannelID, after.ChannelType)
	}
	rows, next, done, err := personDirectoryTaskTable.ScanPrimary(ctx, s, cursor, limit)
	if err != nil {
		return nil, PersonDirectoryTaskCursor{}, false, err
	}
	nextCursor := after
	if len(next) >= 2 {
		nextCursor = PersonDirectoryTaskCursor{ChannelID: next[0].S, ChannelType: next[1].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = PersonDirectoryTaskCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, nextCursor, done, nil
}

// EnsurePersonDirectoryTask atomically advances Channel metadata to pending
// and creates the durable task when absent. Ready channels are immutable.
func (b *Batch) EnsurePersonDirectoryTask(hashSlot HashSlot, task PersonDirectoryTask) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	key := ChannelKey{ChannelID: task.ChannelID, ChannelType: task.ChannelType}
	if err := validatePersonDirectoryKey(key); err != nil {
		return err
	}
	if task.CreatedAt < 0 {
		return dberrors.ErrInvalidArgument
	}
	pk := personDirectoryTaskPrimaryKey(key.ChannelID, key.ChannelType)
	primaryKey, err := personDirectoryTaskTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	channelKey := encodeChannelRowKey(hashSlot, key.ChannelID, key.ChannelType, channelPrimaryFamilyID)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		channel, channelExists, err := state.loadChannel(ctx, channelKey, key.ChannelID, key.ChannelType)
		if err != nil {
			return err
		}
		if channelExists && channel.DirectoryProjectionState == DirectoryProjectionReady {
			return nil
		}
		runtimeKey := encodeChannelRuntimeMetaRowKey(hashSlot, key.ChannelID, key.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		runtimeMeta, runtimeExists, err := state.loadRuntimeMeta(ctx, hashSlot, runtimeKey, key.ChannelID, key.ChannelType)
		if err != nil {
			return err
		}
		generation := task.Generation
		if runtimeExists {
			generation = runtimeMeta.DirectoryGeneration
		}
		if generation == 0 || (task.Generation != 0 && task.Generation != generation) {
			return dberrors.ErrConflict
		}
		if !channelExists {
			channel = Channel{ChannelID: key.ChannelID, ChannelType: key.ChannelType}
		}
		channel.DirectoryProjectionState = DirectoryProjectionPending
		channel.DirectoryProjectionGeneration = generation
		shard := &Shard{db: state.db, hashSlot: hashSlot}
		if err := shard.stageChannel(batch, channelKey, channel); err != nil {
			return err
		}
		state.channelPublishes[string(channelKey)] = channel
		delete(state.channelDeletes, string(channelKey))

		existingTask, exists, err := personDirectoryTaskTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		if exists {
			if existingTask.Generation != generation {
				return dberrors.ErrConflict
			}
			return nil
		}
		task.Generation = generation
		value := appendValueUint64(nil, task.CommittedTail)
		value = appendValueInt64(value, task.CreatedAt)
		value = appendValueUint64(value, task.Generation)
		if err := batch.Set(primaryKey, value); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

// CompletePersonDirectoryTask atomically removes one pending task and advances
// its Channel metadata to ready.
func (b *Batch) CompletePersonDirectoryTask(hashSlot HashSlot, location PersonDirectoryTaskLocation) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	key := ChannelKey{ChannelID: location.ChannelID, ChannelType: location.ChannelType}
	if err := validatePersonDirectoryKey(key); err != nil || location.HashSlot != hashSlot || location.Generation == 0 {
		if err != nil {
			return err
		}
		return dberrors.ErrInvalidArgument
	}
	pk := personDirectoryTaskPrimaryKey(key.ChannelID, key.ChannelType)
	primaryKey, err := personDirectoryTaskTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	channelKey := encodeChannelRowKey(hashSlot, key.ChannelID, key.ChannelType, channelPrimaryFamilyID)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		channel, exists, err := state.loadChannel(ctx, channelKey, key.ChannelID, key.ChannelType)
		if err != nil {
			return err
		}
		if !exists || channel.DirectoryProjectionState == DirectoryProjectionNone {
			return dberrors.ErrNotFound
		}
		task, taskExists, err := personDirectoryTaskTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		if !taskExists {
			if channel.DirectoryProjectionState == DirectoryProjectionReady && channel.DirectoryProjectionGeneration == location.Generation {
				return nil
			}
			return dberrors.ErrNotFound
		}
		if task.Generation != location.Generation || channel.DirectoryProjectionGeneration != location.Generation {
			return dberrors.ErrConflict
		}
		if err := batch.Delete(primaryKey); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{exists: false}
		channel.DirectoryProjectionState = DirectoryProjectionReady
		shard := &Shard{db: state.db, hashSlot: hashSlot}
		if err := shard.stageChannel(batch, channelKey, channel); err != nil {
			return err
		}
		state.channelPublishes[string(channelKey)] = channel
		delete(state.channelDeletes, string(channelKey))
		return nil
	})
	return nil
}

func personDirectoryTaskPrimaryKey(channelID string, channelType int64) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType)}
}

func validatePersonDirectoryKey(key ChannelKey) error {
	if key.ChannelType != 1 {
		return dberrors.ErrInvalidArgument
	}
	return validateChannelKey(key)
}

func validatePersonDirectoryTaskCursor(cursor PersonDirectoryTaskCursor) error {
	if cursor == (PersonDirectoryTaskCursor{}) {
		return nil
	}
	return validatePersonDirectoryKey(ChannelKey{ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
}
