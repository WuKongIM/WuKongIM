package meta

import (
	"context"
	"encoding/binary"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	conversationPrimaryFamilyID uint16 = 0

	conversationColumnUID          uint16 = 1
	conversationColumnChannelID    uint16 = 2
	conversationColumnChannelType  uint16 = 3
	conversationColumnValue        uint16 = 4
	conversationColumnActiveAt     uint16 = 5
	conversationColumnSparseActive uint16 = 6
	conversationColumnKind         uint16 = 7
)

// ConversationState stores one user's durable conversation cursors.
type ConversationState struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// Kind identifies the logical conversation projection view.
	Kind ConversationKind
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the highest message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest message sequence hidden from future sync.
	DeletedToSeq uint64
	// ActiveAt is the latest activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest state mutation timestamp.
	UpdatedAt int64
	// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
	// Message appends do not have to advance ActiveAt for every user in this conversation.
	SparseActive bool
}

// ConversationStateKey identifies one UID-owned conversation row.
type ConversationStateKey struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// Kind identifies the logical conversation projection view.
	Kind ConversationKind
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
}

var conversationTable = registerMetaTable(TableSpec[ConversationState]{
	ID:   TableIDConversation,
	Name: "conversation",
	Columns: []schema.Column{
		{ID: conversationColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: conversationColumnKind, Name: "kind", Type: schema.TypeUint64, Required: true},
		{ID: conversationColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: conversationColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: conversationColumnValue, Name: "value", Type: schema.TypeBytes},
		{ID: conversationColumnActiveAt, Name: "active_at", Type: schema.TypeInt64},
		{ID: conversationColumnSparseActive, Name: "sparse_active", Type: schema.TypeBool},
	},
	Families: []schema.Family{{ID: conversationPrimaryFamilyID, Name: "primary", Columns: []uint16{conversationColumnValue, conversationColumnActiveAt, conversationColumnSparseActive}}},
	Primary: PrimarySpec[ConversationState]{
		IndexID:  conversationPrimaryIndexID,
		FamilyID: conversationPrimaryFamilyID,
		Name:     "pk_conversation",
		Columns:  []uint16{conversationColumnUID, conversationColumnKind, conversationColumnChannelID, conversationColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyUint64, KeyString, KeyInt64Ordered},
		Key: func(state ConversationState) KeyParts {
			return KeyParts{String(state.UID), Uint64(uint64(state.Kind)), String(state.ChannelID), Int64Ordered(state.ChannelType)}
		},
	},
	Indexes: []IndexSpec[ConversationState]{
		{
			ID:      conversationActiveIndexID,
			Name:    "idx_conversation_active",
			Columns: []uint16{conversationColumnUID, conversationColumnKind, conversationColumnActiveAt, conversationColumnChannelID, conversationColumnChannelType},
			Layout:  KeyLayout{KeyString, KeyUint64, KeyInt64Desc, KeyString, KeyInt64Ordered},
			Key: func(state ConversationState) (KeyParts, bool) {
				if state.ActiveAt <= 0 {
					return nil, false
				}
				return KeyParts{String(state.UID), Uint64(uint64(state.Kind)), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)}, true
			},
			PrimaryKeyFromIndexParts: conversationPrimaryFromActiveIndexParts,
			CorruptIndexKeyIsError:   true,
		},
	},
	Validate: validateConversationState,
	EncodeValue: func(state ConversationState) ([]byte, error) {
		return encodeConversationStateValue(state), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (ConversationState, error) {
		return decodeConversationStateValue(primary[0].S, ConversationKind(primary[1].U64), primary[2].S, primary[3].I64, value)
	},
})

// ConversationTable describes the user conversation table schema.
var ConversationTable = conversationTable.Schema()

// ConversationKey identifies a user-owned conversation row.
type ConversationKey struct {
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
}

// ConversationCursor identifies the last emitted row in a conversation page.
type ConversationCursor struct {
	// ChannelID is the last emitted channel ID.
	ChannelID string
	// ChannelType is the last emitted channel type.
	ChannelType int64
}

// ConversationActiveCursor identifies the last emitted active-index row.
type ConversationActiveCursor struct {
	// ActiveAt is the active timestamp from the last emitted row.
	ActiveAt int64
	// ChannelID is the channel ID from the last emitted row.
	ChannelID string
	// ChannelType is the channel type from the last emitted row.
	ChannelType int64
}

// ConversationActivePatch advances a conversation active timestamp.
type ConversationActivePatch struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// Kind identifies the logical conversation projection view.
	Kind ConversationKind
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is a monotonic read floor merged with the durable row.
	ReadSeq uint64
	// DeletedToSeq is a monotonic visibility floor merged with the durable row.
	DeletedToSeq uint64
	// ActiveAt is the candidate activity timestamp.
	ActiveAt int64
	// UpdatedAt records the latest projection update timestamp.
	UpdatedAt int64
	// MessageSeq fences stale activity hints after a user delete barrier.
	MessageSeq uint64
	// SparseActive is the requested sparse-active mode when SparseActiveSet is true.
	SparseActive bool
	// SparseActiveSet reports that SparseActive should update the stored sparse mode.
	SparseActiveSet bool
}

// ConversationDelete hides a conversation through DeletedToSeq.
type ConversationDelete struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// Kind identifies the logical conversation projection view.
	Kind ConversationKind
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// DeletedToSeq is the highest sequence hidden by the delete.
	DeletedToSeq uint64
	// UpdatedAt records when the hide operation was requested.
	UpdatedAt int64
}

// GetConversationState returns one conversation state row.
func (s *Shard) GetConversationState(ctx context.Context, kind ConversationKind, uid, channelID string, channelType int64) (ConversationState, bool, error) {
	if err := s.check(ctx); err != nil {
		return ConversationState{}, false, err
	}
	if err := validateConversationKind(kind); err != nil {
		return ConversationState{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return ConversationState{}, false, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return ConversationState{}, false, err
	}
	state, ok, err := conversationTable.Get(ctx, s, conversationPrimaryKeyParts(kind, uid, channelID, channelType))
	return state, ok, err
}

// UpsertConversationState stores a conversation state.
func (s *Shard) UpsertConversationState(ctx context.Context, state ConversationState) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateConversationState(state); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeConversationRowKey(s.hashSlot, state.UID, state.Kind, state.ChannelID, state.ChannelType, conversationPrimaryFamilyID)
	existing, exists, err := conversationTable.Get(ctx, s, conversationPrimaryKeyParts(state.Kind, state.UID, state.ChannelID, state.ChannelType))
	if err != nil {
		return err
	}
	if exists {
		state = mergeConversationState(existing, state)
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageConversationState(batch, primaryKey, existing, exists, state); err != nil {
		return err
	}
	return batch.Commit(true)
}

// TouchConversationActiveAt advances active_at and monotonic visibility floors.
func (s *Shard) TouchConversationActiveAt(ctx context.Context, patch ConversationActivePatch) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateConversationActivePatch(patch); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeConversationRowKey(s.hashSlot, patch.UID, patch.Kind, patch.ChannelID, patch.ChannelType, conversationPrimaryFamilyID)
	current, exists, err := conversationTable.Get(ctx, s, conversationPrimaryKeyParts(patch.Kind, patch.UID, patch.ChannelID, patch.ChannelType))
	if err != nil {
		return err
	}
	if !exists {
		current = ConversationState{UID: patch.UID, Kind: patch.Kind, ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}
	}
	deleteBarrier := current.DeletedToSeq
	if patch.DeletedToSeq > deleteBarrier {
		deleteBarrier = patch.DeletedToSeq
	}
	activeBlocked := patch.MessageSeq > 0 && patch.MessageSeq <= deleteBarrier
	activeChanged := !activeBlocked && patch.ActiveAt > current.ActiveAt
	sparseChanged := patch.SparseActiveSet && patch.SparseActive != current.SparseActive
	floorsChanged := patch.ReadSeq > current.ReadSeq || patch.DeletedToSeq > current.DeletedToSeq || patch.UpdatedAt > current.UpdatedAt
	if !activeChanged && !sparseChanged && !floorsChanged {
		return nil
	}
	next := current
	if activeChanged {
		next.ActiveAt = patch.ActiveAt
	}
	if patch.SparseActiveSet {
		next.SparseActive = patch.SparseActive
	}
	if patch.ReadSeq > next.ReadSeq {
		next.ReadSeq = patch.ReadSeq
	}
	if patch.DeletedToSeq > next.DeletedToSeq {
		next.DeletedToSeq = patch.DeletedToSeq
	}
	if patch.UpdatedAt > next.UpdatedAt {
		next.UpdatedAt = patch.UpdatedAt
	}

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageConversationState(batch, primaryKey, current, exists, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ClearConversationActiveAt clears active_at for existing conversations.
func (s *Shard) ClearConversationActiveAt(ctx context.Context, kind ConversationKind, uid string, keys []ConversationKey) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateConversationKind(kind); err != nil {
		return err
	}
	if err := validateConversationUID(uid); err != nil {
		return err
	}
	normalized, err := normalizeConversationKeys(keys)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return nil
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	for _, key := range normalized {
		primaryKey := encodeConversationRowKey(s.hashSlot, uid, kind, key.ChannelID, key.ChannelType, conversationPrimaryFamilyID)
		current, exists, err := conversationTable.Get(ctx, s, conversationPrimaryKeyParts(kind, uid, key.ChannelID, key.ChannelType))
		if err != nil {
			return err
		}
		if !exists || current.ActiveAt <= 0 {
			continue
		}
		next := current
		next.ActiveAt = 0
		if err := s.stageConversationState(batch, primaryKey, current, true, next); err != nil {
			return err
		}
	}
	return batch.Commit(true)
}

// HideConversation advances the delete barrier and clears active_at.
func (s *Shard) HideConversation(ctx context.Context, req ConversationDelete) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateConversationDelete(req); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeConversationRowKey(s.hashSlot, req.UID, req.Kind, req.ChannelID, req.ChannelType, conversationPrimaryFamilyID)
	current, exists, err := conversationTable.Get(ctx, s, conversationPrimaryKeyParts(req.Kind, req.UID, req.ChannelID, req.ChannelType))
	if err != nil {
		return err
	}
	if !exists {
		if req.DeletedToSeq == 0 {
			return nil
		}
		current = ConversationState{UID: req.UID, Kind: req.Kind, ChannelID: req.ChannelID, ChannelType: req.ChannelType}
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

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageConversationState(batch, primaryKey, current, exists, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ListConversationActive returns active conversations in newest-first order.
func (s *Shard) ListConversationActive(ctx context.Context, kind ConversationKind, uid string, limit int) ([]ConversationState, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validateConversationKind(kind); err != nil {
		return nil, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, err
	}
	rows, err := conversationTable.scanIndexRows(ctx, s, conversationActiveIndexID, conversationActivePrefixParts(kind, uid), limit)
	return rows, err
}

// ListConversationActivePage returns active rows with an active-index cursor.
func (s *Shard) ListConversationActivePage(ctx context.Context, kind ConversationKind, uid string, cursor ConversationActiveCursor, limit int) ([]ConversationState, ConversationActiveCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	if err := validateConversationKind(kind); err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	if err := validateConversationActiveCursor(cursor); err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	var after KeyParts
	if cursor != (ConversationActiveCursor{}) {
		after = KeyParts{String(uid), Uint64(uint64(kind)), Int64Desc(cursor.ActiveAt), String(cursor.ChannelID), Int64Ordered(cursor.ChannelType)}
	}
	rows, next, done, err := conversationTable.ScanIndex(ctx, s, conversationActiveIndexID, conversationActivePrefixParts(kind, uid), after, limit)
	if err != nil {
		return nil, ConversationActiveCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 5 {
		nextCursor = ConversationActiveCursor{ActiveAt: next[2].I64, ChannelID: next[3].S, ChannelType: next[4].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, nextCursor, done, nil
}

// ListConversationStatePage returns primary rows in stable key order.
func (s *Shard) ListConversationStatePage(ctx context.Context, kind ConversationKind, uid string, cursor ConversationCursor, limit int) ([]ConversationState, ConversationCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationKind(kind); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationCursor(cursor); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	var after KeyParts
	if cursor != (ConversationCursor{}) {
		after = KeyParts{String(uid), Uint64(uint64(kind)), String(cursor.ChannelID), Int64Ordered(cursor.ChannelType)}
	}
	rows, next, done, err := conversationTable.scanPrimaryPrefixStrict(ctx, s, conversationPrimaryPrefixParts(kind, uid), after, limit)
	if err != nil {
		return nil, ConversationCursor{}, false, err
	}
	nextCursor := cursor
	if len(next) >= 4 {
		nextCursor = ConversationCursor{ChannelID: next[2].S, ChannelType: next[3].I64}
	} else if len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = ConversationCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, nextCursor, done, nil
}

func (s *Shard) getConversationStateByKey(ctx context.Context, key []byte, kind ConversationKind, uid, channelID string, channelType int64) (ConversationState, bool, error) {
	return conversationTable.Get(ctx, s, conversationPrimaryKeyParts(kind, uid, channelID, channelType))
}

func (s *Shard) stageConversationState(batch *engine.Batch, primaryKey []byte, existing ConversationState, exists bool, next ConversationState) error {
	pk := conversationPrimaryKeyParts(next.Kind, next.UID, next.ChannelID, next.ChannelType)
	if exists {
		if err := conversationTable.stageDeleteIndexEntries(batch, s.hashSlot, existing, pk); err != nil {
			return err
		}
	}
	value := encodeConversationStateValue(next)
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	return conversationTable.stagePutIndexEntries(batch, s.hashSlot, next, pk, value)
}

// stageConversationStateWithOverlay stages a user conversation row and exposes it to later batch operations.
func (s *Shard) stageConversationStateWithOverlay(state *batchCommitState, batch *engine.Batch, primaryKey []byte, existing ConversationState, exists bool, next ConversationState) error {
	if err := s.stageConversationState(batch, primaryKey, existing, exists, next); err != nil {
		return err
	}
	value := encodeConversationStateValue(next)
	state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
	return nil
}

func conversationPrimaryFromActiveIndexParts(parts KeyParts) (KeyParts, bool) {
	if len(parts) != 5 {
		return nil, false
	}
	return KeyParts{parts[0], parts[1], parts[3], parts[4]}, true
}

func validateConversationState(state ConversationState) error {
	if err := validateConversationKind(state.Kind); err != nil {
		return err
	}
	if err := validateConversationUID(state.UID); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType})
}

func validateConversationDelete(req ConversationDelete) error {
	if err := validateConversationKind(req.Kind); err != nil {
		return err
	}
	if err := validateConversationUID(req.UID); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType})
}

func validateConversationActivePatch(patch ConversationActivePatch) error {
	if err := validateConversationKind(patch.Kind); err != nil {
		return err
	}
	if err := validateConversationUID(patch.UID); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType})
}

func validateConversationKind(kind ConversationKind) error {
	if kind == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateConversationUID(uid string) error {
	return validateKeyString(uid)
}

func validateConversationKey(key ConversationKey) error {
	return validateKeyString(key.ChannelID)
}

func validateConversationCursor(cursor ConversationCursor) error {
	if cursor == (ConversationCursor{}) {
		return nil
	}
	if cursor.ChannelID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validateConversationKey(ConversationKey{ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
}

func validateConversationActiveCursor(cursor ConversationActiveCursor) error {
	if cursor == (ConversationActiveCursor{}) {
		return nil
	}
	if cursor.ActiveAt <= 0 || cursor.ChannelID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validateConversationKey(ConversationKey{ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
}

func validateConversationLimit(limit int) error {
	if limit <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func mergeConversationState(existing, next ConversationState) ConversationState {
	next.UID = existing.UID
	next.Kind = existing.Kind
	next.ChannelID = existing.ChannelID
	next.ChannelType = existing.ChannelType
	if next.ReadSeq < existing.ReadSeq {
		next.ReadSeq = existing.ReadSeq
	}
	if next.DeletedToSeq < existing.DeletedToSeq {
		next.DeletedToSeq = existing.DeletedToSeq
	}
	if next.ActiveAt < existing.ActiveAt {
		next.ActiveAt = existing.ActiveAt
	}
	if next.UpdatedAt < existing.UpdatedAt {
		next.UpdatedAt = existing.UpdatedAt
	}
	return next
}

func conversationPrimaryPrefixParts(kind ConversationKind, uid string) KeyParts {
	return KeyParts{String(uid), Uint64(uint64(kind))}
}

func conversationPrimaryKeyParts(kind ConversationKind, uid, channelID string, channelType int64) KeyParts {
	return KeyParts{String(uid), Uint64(uint64(kind)), String(channelID), Int64Ordered(channelType)}
}

func conversationActivePrefixParts(kind ConversationKind, uid string) KeyParts {
	return KeyParts{String(uid), Uint64(uint64(kind))}
}

func normalizeConversationKeys(keys []ConversationKey) ([]ConversationKey, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	seen := make(map[ConversationKey]struct{}, len(keys))
	out := make([]ConversationKey, 0, len(keys))
	for _, key := range keys {
		if err := validateConversationKey(key); err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChannelID != out[j].ChannelID {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].ChannelType < out[j].ChannelType
	})
	return out, nil
}

func encodeConversationValue(readSeq, deletedToSeq uint64, activeAt, updatedAt int64) []byte {
	value := appendValueUint64(nil, readSeq)
	value = appendValueUint64(value, deletedToSeq)
	value = appendValueInt64(value, activeAt)
	return appendValueInt64(value, updatedAt)
}

func encodeConversationStateValue(state ConversationState) []byte {
	value := encodeConversationValue(state.ReadSeq, state.DeletedToSeq, state.ActiveAt, state.UpdatedAt)
	if state.SparseActive {
		return append(value, 1)
	}
	return append(value, 0)
}

func decodeConversationStateValue(uid string, kind ConversationKind, channelID string, channelType int64, value []byte) (ConversationState, error) {
	readSeq, deletedToSeq, activeAt, updatedAt, sparseActive, err := decodeConversationStateValueFields(value)
	if err != nil {
		return ConversationState{}, err
	}
	return ConversationState{
		UID:          uid,
		Kind:         kind,
		ChannelID:    channelID,
		ChannelType:  channelType,
		ReadSeq:      readSeq,
		DeletedToSeq: deletedToSeq,
		ActiveAt:     activeAt,
		UpdatedAt:    updatedAt,
		SparseActive: sparseActive,
	}, nil
}

func decodeConversationStateValueFields(value []byte) (uint64, uint64, int64, int64, bool, error) {
	readSeq, deletedToSeq, activeAt, updatedAt, rest, err := decodeConversationValueFields(value)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	if len(rest) == 0 {
		return readSeq, deletedToSeq, activeAt, updatedAt, false, nil
	}
	if len(rest) != 1 {
		return 0, 0, 0, 0, false, dberrors.ErrCorruptValue
	}
	switch rest[0] {
	case 0:
		return readSeq, deletedToSeq, activeAt, updatedAt, false, nil
	case 1:
		return readSeq, deletedToSeq, activeAt, updatedAt, true, nil
	default:
		return 0, 0, 0, 0, false, dberrors.ErrCorruptValue
	}
}

func decodeConversationValue(value []byte) (uint64, uint64, int64, int64, error) {
	readSeq, deletedToSeq, activeAt, updatedAt, rest, err := decodeConversationValueFields(value)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(rest) != 0 {
		return 0, 0, 0, 0, dberrors.ErrCorruptValue
	}
	return readSeq, deletedToSeq, activeAt, updatedAt, nil
}

func decodeConversationValueFields(value []byte) (uint64, uint64, int64, int64, []byte, error) {
	readSeq, rest, err := readValueUint64(value)
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	deletedToSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	activeAt, rest, err := readValueInt64(rest)
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	return readSeq, deletedToSeq, activeAt, updatedAt, rest, nil
}

func readKeyInt64Ordered(src []byte) (int64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, dberrors.ErrCorruptValue
	}
	ordered := binary.BigEndian.Uint64(src[:8])
	return int64(ordered ^ (uint64(1) << 63)), src[8:], nil
}
