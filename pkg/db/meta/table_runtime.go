package meta

import (
	"bytes"
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// TableSpec describes one runtime-backed metadata table.
type TableSpec[R any] struct {
	ID   uint32
	Name string

	Columns        []schema.Column
	Families       []schema.Family
	Primary        PrimarySpec[R]
	Indexes        []IndexSpec[R]
	SnapshotPolicy SnapshotPolicy

	Validate    func(R) error
	EncodeValue func(R) ([]byte, error)
	DecodeValue func(KeyParts, []byte) (R, error)
}

// PrimarySpec describes the primary key and row family for a table.
type PrimarySpec[R any] struct {
	IndexID  uint16
	FamilyID uint16
	Name     string
	Columns  []uint16
	Layout   KeyLayout
	Key      func(R) KeyParts
}

// IndexSpec describes one secondary index.
type IndexSpec[R any] struct {
	ID      uint16
	Name    string
	Unique  bool
	Columns []uint16
	Layout  KeyLayout
	Key     func(R) (KeyParts, bool)
}

// Table is a typed handle for common metadata table operations.
type Table[R any] struct {
	spec   TableSpec[R]
	schema schema.Table
}

func registerMetaTable[R any](spec TableSpec[R]) Table[R] {
	table, err := registerMetaTableInRegistry(defaultMetaRegistry, spec)
	if err != nil {
		panic(err)
	}
	return table
}

func registerMetaTableInRegistry[R any](registry *metaTableRegistry, spec TableSpec[R]) (Table[R], error) {
	normalized, tableSchema, err := normalizeTableSpec(spec)
	if err != nil {
		return Table[R]{}, err
	}
	table := Table[R]{spec: normalized, schema: tableSchema}
	if err := registry.register(metaTableDescriptor{Table: tableSchema, SnapshotPolicy: spec.SnapshotPolicy}); err != nil {
		return Table[R]{}, err
	}
	return table, nil
}

// Schema returns a copy of the table schema descriptor.
func (t Table[R]) Schema() schema.Table {
	return cloneSchemaTable(t.schema)
}

func normalizeTableSpec[R any](spec TableSpec[R]) (TableSpec[R], schema.Table, error) {
	if spec.ID == 0 || spec.Name == "" || spec.Primary.Key == nil || spec.EncodeValue == nil || spec.DecodeValue == nil {
		return spec, schema.Table{}, fmt.Errorf("%w: incomplete table spec", dberrors.ErrInvalidArgument)
	}
	if spec.Primary.IndexID == 0 || spec.Primary.Name == "" || len(spec.Primary.Layout) != len(spec.Primary.Columns) {
		return spec, schema.Table{}, fmt.Errorf("%w: invalid primary spec", dberrors.ErrInvalidArgument)
	}
	indexes := make([]schema.Index, 0, len(spec.Indexes))
	for _, index := range spec.Indexes {
		if index.ID == 0 || index.Name == "" || index.Key == nil || len(index.Layout) != len(index.Columns) {
			return spec, schema.Table{}, fmt.Errorf("%w: invalid index spec", dberrors.ErrInvalidArgument)
		}
		indexes = append(indexes, schema.Index{
			ID:      index.ID,
			Name:    index.Name,
			Unique:  index.Unique,
			Columns: append([]uint16(nil), index.Columns...),
		})
	}
	tableSchema := schema.Table{
		ID:       spec.ID,
		Name:     spec.Name,
		Columns:  append([]schema.Column(nil), spec.Columns...),
		Families: cloneFamilies(spec.Families),
		Primary: schema.Index{
			ID:      spec.Primary.IndexID,
			Name:    spec.Primary.Name,
			Unique:  true,
			Primary: true,
			Columns: append([]uint16(nil), spec.Primary.Columns...),
		},
		Indexes: indexes,
	}
	if err := schema.ValidateTable(tableSchema); err != nil {
		return spec, schema.Table{}, err
	}
	return spec, tableSchema, nil
}

func cloneFamilies(families []schema.Family) []schema.Family {
	out := append([]schema.Family(nil), families...)
	for i := range out {
		out[i].Columns = append([]uint16(nil), out[i].Columns...)
	}
	return out
}

type tableWriteMode uint8

const (
	tableWriteCreate tableWriteMode = iota + 1
	tableWriteUpdate
	tableWriteUpsert
)

// Get returns one row by primary key.
func (t Table[R]) Get(ctx context.Context, s *Shard, pk KeyParts) (R, bool, error) {
	var zero R
	if err := s.check(ctx); err != nil {
		return zero, false, err
	}
	row, exists, err := t.getByPrimaryKey(s.db, s.hashSlot, pk)
	if err != nil || !exists {
		return zero, exists, err
	}
	return row, true, nil
}

// Create inserts a row and rejects duplicate primary keys.
func (t Table[R]) Create(ctx context.Context, s *Shard, row R) error {
	return t.write(ctx, s, row, tableWriteCreate)
}

// Update replaces an existing row and rejects missing primary keys.
func (t Table[R]) Update(ctx context.Context, s *Shard, row R) error {
	return t.write(ctx, s, row, tableWriteUpdate)
}

// Upsert stores a row regardless of prior existence.
func (t Table[R]) Upsert(ctx context.Context, s *Shard, row R) error {
	return t.write(ctx, s, row, tableWriteUpsert)
}

// Delete removes a row by primary key. Missing rows are ignored.
func (t Table[R]) Delete(ctx context.Context, s *Shard, pk KeyParts) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := t.validatePrimaryKey(pk); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := t.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	old, exists, err := t.getByPrimaryKey(s.db, s.hashSlot, pk)
	if err != nil {
		return err
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if exists {
		if err := t.stageDeleteIndexEntries(batch, s.hashSlot, old, pk); err != nil {
			return err
		}
	}
	if err := batch.Delete(primaryKey); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ScanPrimary returns a primary-key ordered page after cursor.
func (t Table[R]) ScanPrimary(ctx context.Context, s *Shard, after KeyParts, limit int) ([]R, KeyParts, bool, error) {
	if limit <= 0 {
		return nil, nil, true, nil
	}
	return t.scanPrimary(ctx, s, nil, after, limit, false)
}

// ScanPrimaryPrefix returns primary-key ordered rows matching prefix.
func (t Table[R]) ScanPrimaryPrefix(ctx context.Context, s *Shard, prefix KeyParts, after KeyParts, limit int) ([]R, KeyParts, bool, error) {
	return t.scanPrimary(ctx, s, prefix, after, limit, limit <= 0)
}

// ScanIndex returns rows by secondary index prefix.
func (t Table[R]) ScanIndex(ctx context.Context, s *Shard, indexID uint16, prefix KeyParts, after KeyParts, limit int) ([]R, KeyParts, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, false, err
	}
	index, ok := t.indexByID(indexID)
	if !ok {
		return nil, nil, false, dberrors.ErrInvalidArgument
	}
	if limit <= 0 {
		return nil, nil, true, nil
	}
	if err := validateKeyPartsAgainstLayout(prefix, index.Layout[:len(prefix)]); err != nil {
		return nil, nil, false, err
	}
	if len(prefix) > len(index.Layout) {
		return nil, nil, false, dberrors.ErrInvalidArgument
	}
	if len(after) > 0 {
		if len(after) > len(index.Layout) || !keyPartsHasPrefix(after, prefix) {
			return nil, nil, false, dberrors.ErrInvalidArgument
		}
		if err := validateKeyPartsAgainstLayout(after, index.Layout[:len(after)]); err != nil {
			return nil, nil, false, err
		}
	}

	scanPrefix, err := encodeTableIndexScanPrefix(s.hashSlot, t.spec.ID, index.ID, prefix)
	if err != nil {
		return nil, nil, false, err
	}
	span := keycodec.NewPrefixSpan(scanPrefix)
	if len(after) > 0 {
		afterKey, err := encodeTableIndexScanPrefix(s.hashSlot, t.spec.ID, index.ID, after)
		if err != nil {
			return nil, nil, false, err
		}
		span.Start = keycodec.PrefixEnd(afterKey)
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, nil, false, err
	}
	defer iter.Close()

	basePrefix := encodeIndexPrefix(s.hashSlot, t.spec.ID, index.ID)
	rows := make([]R, 0, limit)
	var cursor KeyParts
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return nil, nil, false, err
		}
		indexParts, primaryParts, ok := t.decodeIndexKey(basePrefix, iter.Key(), index)
		if !ok || !keyPartsHasPrefix(indexParts, prefix) {
			continue
		}
		row, exists, err := t.getByPrimaryKey(s.db, s.hashSlot, primaryParts)
		if err != nil {
			return nil, nil, false, err
		}
		if !exists || !t.rowMatchesIndex(row, index, indexParts) {
			continue
		}
		if len(rows) == limit {
			return rows, cursor, false, nil
		}
		rows = append(rows, row)
		cursor = append(KeyParts(nil), indexParts...)
	}
	if err := iter.Error(); err != nil {
		return nil, nil, false, err
	}
	return rows, nil, true, nil
}

// StageCreate stages a row insert in a metadata batch.
func (t Table[R]) StageCreate(b *Batch, hashSlot HashSlot, row R) error {
	return t.stageWrite(b, hashSlot, row, tableWriteCreate)
}

// StageUpdate stages an existing-row update in a metadata batch.
func (t Table[R]) StageUpdate(b *Batch, hashSlot HashSlot, row R) error {
	return t.stageWrite(b, hashSlot, row, tableWriteUpdate)
}

// StageUpsert stages a row upsert in a metadata batch.
func (t Table[R]) StageUpsert(b *Batch, hashSlot HashSlot, row R) error {
	return t.stageWrite(b, hashSlot, row, tableWriteUpsert)
}

// StageDelete stages a row delete in a metadata batch.
func (t Table[R]) StageDelete(b *Batch, hashSlot HashSlot, pk KeyParts) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := t.validatePrimaryKey(pk); err != nil {
		return err
	}
	primaryKey, err := t.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		old, exists, err := t.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		if exists {
			if err := t.stageDeleteIndexEntries(batch, hashSlot, old, pk); err != nil {
				return err
			}
		}
		if err := batch.Delete(primaryKey); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{exists: false}
		return nil
	})
	return nil
}

func (t Table[R]) write(ctx context.Context, s *Shard, row R, mode tableWriteMode) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if t.spec.Validate != nil {
		if err := t.spec.Validate(row); err != nil {
			return err
		}
	}
	pk, err := t.primaryKey(row)
	if err != nil {
		return err
	}
	value, err := t.spec.EncodeValue(row)
	if err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := t.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existingValue, exists, err := s.db.get(primaryKey)
	if err != nil {
		return err
	}
	var existing R
	if exists {
		existing, err = t.spec.DecodeValue(pk, existingValue)
		if err != nil {
			return err
		}
	}
	switch mode {
	case tableWriteCreate:
		if exists {
			return dberrors.ErrAlreadyExists
		}
	case tableWriteUpdate:
		if !exists {
			return dberrors.ErrNotFound
		}
	}

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if exists {
		if err := t.stageDeleteIndexEntries(batch, s.hashSlot, existing, pk); err != nil {
			return err
		}
	}
	if err := t.stageUniqueIndexChecks(ctx, s, row, pk); err != nil {
		return err
	}
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	if err := t.stagePutIndexEntries(batch, s.hashSlot, row, pk); err != nil {
		return err
	}
	return batch.Commit(true)
}

func (t Table[R]) stageWrite(b *Batch, hashSlot HashSlot, row R, mode tableWriteMode) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if t.spec.Validate != nil {
		if err := t.spec.Validate(row); err != nil {
			return err
		}
	}
	pk, err := t.primaryKey(row)
	if err != nil {
		return err
	}
	value, err := t.spec.EncodeValue(row)
	if err != nil {
		return err
	}
	primaryKey, err := t.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	value = append([]byte(nil), value...)
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		old, exists, err := t.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		switch mode {
		case tableWriteCreate:
			if _, ok := state.tableCreates[string(primaryKey)]; ok {
				return dberrors.ErrAlreadyExists
			}
			if exists {
				return dberrors.ErrAlreadyExists
			}
			state.tableCreates[string(primaryKey)] = struct{}{}
		case tableWriteUpdate:
			if !exists {
				return dberrors.ErrNotFound
			}
		}
		if exists {
			if err := t.stageDeleteIndexEntries(batch, hashSlot, old, pk); err != nil {
				return err
			}
		}
		if err := t.stageUniqueIndexChecksWithState(ctx, state, hashSlot, row, pk); err != nil {
			return err
		}
		if err := batch.Set(primaryKey, value); err != nil {
			return err
		}
		if err := t.stagePutIndexEntries(batch, hashSlot, row, pk); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func (t Table[R]) getByPrimaryKey(db *MetaDB, hashSlot HashSlot, pk KeyParts) (R, bool, error) {
	var zero R
	if err := t.validatePrimaryKey(pk); err != nil {
		return zero, false, err
	}
	primaryKey, err := t.primaryRowKey(hashSlot, pk)
	if err != nil {
		return zero, false, err
	}
	value, ok, err := db.get(primaryKey)
	if err != nil || !ok {
		return zero, ok, err
	}
	row, err := t.spec.DecodeValue(pk, value)
	if err != nil {
		return zero, false, err
	}
	return row, true, nil
}

func (t Table[R]) loadBatchRow(state *batchCommitState, hashSlot HashSlot, pk KeyParts, primaryKey []byte) (R, bool, error) {
	var zero R
	if overlay, ok := state.tableRows[string(primaryKey)]; ok {
		if !overlay.exists {
			return zero, false, nil
		}
		row, err := t.spec.DecodeValue(pk, overlay.value)
		if err != nil {
			return zero, false, err
		}
		return row, true, nil
	}
	return t.getByPrimaryKey(state.db, hashSlot, pk)
}

func (t Table[R]) scanPrimary(ctx context.Context, s *Shard, prefix KeyParts, after KeyParts, limit int, unlimited bool) ([]R, KeyParts, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, false, err
	}
	if err := t.validatePrimaryPrefix(prefix); err != nil {
		return nil, nil, false, err
	}
	if len(after) > 0 {
		if err := t.validatePrimaryKey(after); err != nil {
			return nil, nil, false, err
		}
	}

	basePrefix := encodeRowPrefix(s.hashSlot, t.spec.ID)
	scanPrefix, err := encodeKeyParts(basePrefix, prefix)
	if err != nil {
		return nil, nil, false, err
	}
	span := keycodec.NewPrefixSpan(scanPrefix)
	if len(after) > 0 {
		afterKey, err := t.primaryRowKey(s.hashSlot, after)
		if err != nil {
			return nil, nil, false, err
		}
		span.Start = keycodec.PrefixEnd(afterKey)
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, nil, false, err
	}
	defer iter.Close()

	rows := make([]R, 0, positiveLimit(limit))
	var cursor KeyParts
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := contextErr(ctx); err != nil {
			return nil, nil, false, err
		}
		pk, ok := decodeTablePrimaryRowKey(basePrefix, iter.Key(), t.spec.Primary.Layout, t.spec.Primary.FamilyID)
		if !ok || !keyPartsHasPrefix(pk, prefix) {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, nil, false, err
		}
		row, err := t.spec.DecodeValue(pk, value)
		if err != nil {
			return nil, nil, false, err
		}
		if !unlimited && len(rows) == limit {
			return rows, cursor, false, nil
		}
		rows = append(rows, row)
		cursor = append(KeyParts(nil), pk...)
	}
	if err := iter.Error(); err != nil {
		return nil, nil, false, err
	}
	return rows, nil, true, nil
}

func (t Table[R]) primaryKey(row R) (KeyParts, error) {
	parts := t.spec.Primary.Key(row)
	if err := t.validatePrimaryKey(parts); err != nil {
		return nil, err
	}
	return parts, nil
}

func (t Table[R]) indexByID(indexID uint16) (IndexSpec[R], bool) {
	for _, index := range t.spec.Indexes {
		if index.ID == indexID {
			return index, true
		}
	}
	return IndexSpec[R]{}, false
}

func (t Table[R]) stagePutIndexEntries(batch *engine.Batch, hashSlot HashSlot, row R, pk KeyParts) error {
	for _, index := range t.spec.Indexes {
		indexParts, ok := index.Key(row)
		if !ok {
			continue
		}
		if err := validateKeyPartsAgainstLayout(indexParts, index.Layout); err != nil {
			return err
		}
		key, err := encodeTableIndexKey(hashSlot, t.spec.ID, index.ID, indexParts, pk)
		if err != nil {
			return err
		}
		if err := batch.Set(key, nil); err != nil {
			return err
		}
	}
	return nil
}

func (t Table[R]) stageDeleteIndexEntries(batch *engine.Batch, hashSlot HashSlot, row R, pk KeyParts) error {
	for _, index := range t.spec.Indexes {
		indexParts, ok := index.Key(row)
		if !ok {
			continue
		}
		if err := validateKeyPartsAgainstLayout(indexParts, index.Layout); err != nil {
			return err
		}
		key, err := encodeTableIndexKey(hashSlot, t.spec.ID, index.ID, indexParts, pk)
		if err != nil {
			return err
		}
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

func (t Table[R]) stageUniqueIndexChecks(ctx context.Context, s *Shard, row R, pk KeyParts) error {
	for _, index := range t.spec.Indexes {
		if !index.Unique {
			continue
		}
		indexParts, ok := index.Key(row)
		if !ok {
			continue
		}
		if err := validateKeyPartsAgainstLayout(indexParts, index.Layout); err != nil {
			return err
		}
		prefix, err := encodeTableIndexScanPrefix(s.hashSlot, t.spec.ID, index.ID, indexParts)
		if err != nil {
			return err
		}
		span := keycodec.NewPrefixSpan(prefix)
		iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
		if err != nil {
			return err
		}
		basePrefix := encodeIndexPrefix(s.hashSlot, t.spec.ID, index.ID)
		for ok := iter.First(); ok; ok = iter.Next() {
			if err := contextErr(ctx); err != nil {
				_ = iter.Close()
				return err
			}
			_, indexedPK, ok := t.decodeIndexKey(basePrefix, iter.Key(), index)
			if ok && !indexedPK.Equal(pk) {
				_ = iter.Close()
				return dberrors.ErrAlreadyExists
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return err
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (t Table[R]) stageUniqueIndexChecksWithState(ctx context.Context, state *batchCommitState, hashSlot HashSlot, row R, pk KeyParts) error {
	shard := &Shard{db: state.db, hashSlot: hashSlot}
	if err := t.stageUniqueIndexChecks(ctx, shard, row, pk); err != nil {
		return err
	}
	rowPrefix := encodeRowPrefix(hashSlot, t.spec.ID)
	for primaryKey, overlay := range state.tableRows {
		if !overlay.exists {
			continue
		}
		overlayPK, ok := decodeTablePrimaryRowKey(rowPrefix, []byte(primaryKey), t.spec.Primary.Layout, t.spec.Primary.FamilyID)
		if !ok || overlayPK.Equal(pk) {
			continue
		}
		overlayRow, err := t.spec.DecodeValue(overlayPK, overlay.value)
		if err != nil {
			return err
		}
		for _, index := range t.spec.Indexes {
			if !index.Unique {
				continue
			}
			left, leftOK := index.Key(row)
			right, rightOK := index.Key(overlayRow)
			if leftOK && rightOK && left.Equal(right) {
				return dberrors.ErrAlreadyExists
			}
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (t Table[R]) decodeIndexKey(prefix []byte, key []byte, index IndexSpec[R]) (KeyParts, KeyParts, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return nil, nil, false
	}
	indexParts, rest, err := decodeKeyParts(key[len(prefix):], index.Layout)
	if err != nil {
		return nil, nil, false
	}
	primaryParts, rest, err := decodeKeyParts(rest, t.spec.Primary.Layout)
	if err != nil || len(rest) != 0 {
		return nil, nil, false
	}
	return indexParts, primaryParts, true
}

func (t Table[R]) rowMatchesIndex(row R, index IndexSpec[R], expected KeyParts) bool {
	actual, ok := index.Key(row)
	return ok && actual.Equal(expected)
}

func (t Table[R]) primaryRowKey(hashSlot HashSlot, pk KeyParts) ([]byte, error) {
	if err := t.validatePrimaryKey(pk); err != nil {
		return nil, err
	}
	return encodeTablePrimaryRowKey(hashSlot, t.spec.ID, pk, t.spec.Primary.FamilyID)
}

func (t Table[R]) validatePrimaryKey(pk KeyParts) error {
	return validateKeyPartsAgainstLayout(pk, t.spec.Primary.Layout)
}

func (t Table[R]) validatePrimaryPrefix(prefix KeyParts) error {
	if len(prefix) > len(t.spec.Primary.Layout) {
		return dberrors.ErrInvalidArgument
	}
	return validateKeyPartsAgainstLayout(prefix, t.spec.Primary.Layout[:len(prefix)])
}

func validateKeyPartsAgainstLayout(parts KeyParts, layout KeyLayout) error {
	if len(parts) != len(layout) {
		return dberrors.ErrInvalidArgument
	}
	for i, part := range parts {
		if part.Kind != layout[i] {
			return dberrors.ErrInvalidArgument
		}
		if part.Kind == KeyString {
			if err := validateKeyString(part.S); err != nil {
				return err
			}
		}
	}
	return nil
}

func keyPartsHasPrefix(parts KeyParts, prefix KeyParts) bool {
	if len(prefix) > len(parts) {
		return false
	}
	for i := range prefix {
		if parts[i] != prefix[i] {
			return false
		}
	}
	return true
}

func positiveLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return 0
}
