package meta

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const (
	pluginBindingPrimaryFamilyID uint16 = 0
	pluginBindingPluginIndexID   uint16 = 2
)

// PluginUserBinding records one cluster-authoritative UID to plugin binding.
type PluginUserBinding struct {
	// UID is the user id used as the routing key for this binding.
	UID string
	// PluginNo is the plugin selected for the UID.
	PluginNo string
	// CreatedAtMS records when the binding was first created.
	CreatedAtMS int64
	// UpdatedAtMS records when the binding was last updated.
	UpdatedAtMS int64
}

// PluginUserBindingCursor identifies the last emitted plugin binding.
type PluginUserBindingCursor struct {
	// PluginNo is the plugin number currently being scanned.
	PluginNo string
	// UID is the last emitted UID for PluginNo.
	UID string
}

// BindPluginUser creates or updates one UID to plugin binding idempotently.
func (s *Shard) BindPluginUser(ctx context.Context, binding PluginUserBinding) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validatePluginUserBinding(binding); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodePluginBindingRowKey(s.hashSlot, binding.UID, binding.PluginNo, pluginBindingPrimaryFamilyID)
	existing, exists, err := s.getPluginUserBindingByKey(ctx, primaryKey, binding.UID, binding.PluginNo)
	if err != nil {
		return err
	}
	if exists {
		binding.CreatedAtMS = existing.CreatedAtMS
		if binding.UpdatedAtMS < existing.UpdatedAtMS {
			binding.UpdatedAtMS = existing.UpdatedAtMS
		}
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := stageBindPluginUser(batch, s.hashSlot, binding); err != nil {
		return err
	}
	return batch.Commit(true)
}

// UnbindPluginUser removes one UID to plugin binding idempotently.
func (s *Shard) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := stageUnbindPluginUser(batch, s.hashSlot, uid, pluginNo); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ListPluginBindingsByUID lists all plugin bindings for a UID in plugin order.
func (s *Shard) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginUserBinding, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validatePluginBindingUID(uid); err != nil {
		return nil, err
	}
	prefix := encodePluginBindingRowPrefix(s.hashSlot, uid)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	bindings := make([]PluginUserBinding, 0, 4)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pluginNo, familyID, ok := decodePluginBindingRowKey(prefix, iter.Key())
		if !ok {
			return nil, dberrors.ErrCorruptValue
		}
		if familyID != pluginBindingPrimaryFamilyID {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		binding, err := decodePluginUserBindingValue(uid, pluginNo, value)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return bindings, nil
}

// ScanPluginBindingsByPluginNo scans plugin bindings by plugin number and UID.
func (s *Shard) ScanPluginBindingsByPluginNo(ctx context.Context, pluginNo string, cursor PluginUserBindingCursor, limit int) ([]PluginUserBinding, PluginUserBindingCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := validatePluginBindingPluginNo(pluginNo); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := validatePluginUserBindingCursor(pluginNo, cursor); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if limit <= 0 {
		return nil, PluginUserBindingCursor{}, false, dberrors.ErrInvalidArgument
	}
	prefix := encodePluginBindingPluginIndexPrefix(s.hashSlot, pluginNo)
	span := keycodec.NewPrefixSpan(prefix)
	if cursor.UID != "" {
		span.Start = keycodec.PrefixEnd(encodePluginBindingPluginIndexKey(s.hashSlot, pluginNo, cursor.UID))
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	defer iter.Close()

	bindings := make([]PluginUserBinding, 0, limit)
	nextCursor := cursor
	hasMore := false
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, PluginUserBindingCursor{}, false, err
		}
		uid, err := decodePluginBindingPluginIndexUID(prefix, iter.Key())
		if err != nil {
			return nil, PluginUserBindingCursor{}, false, err
		}
		primaryKey := encodePluginBindingRowKey(s.hashSlot, uid, pluginNo, pluginBindingPrimaryFamilyID)
		binding, exists, err := s.getPluginUserBindingByKey(ctx, primaryKey, uid, pluginNo)
		if err != nil {
			return nil, PluginUserBindingCursor{}, false, err
		}
		if !exists {
			continue
		}
		if len(bindings) == limit {
			hasMore = true
			break
		}
		bindings = append(bindings, binding)
		nextCursor = pluginBindingToCursor(binding)
	}
	if err := iter.Error(); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	return bindings, nextCursor, !hasMore, nil
}

// ExistPluginBindingByUID reports whether a UID has at least one plugin binding.
func (s *Shard) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	if err := validatePluginBindingUID(uid); err != nil {
		return false, err
	}
	prefix := encodePluginBindingRowPrefix(s.hashSlot, uid)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	ok := iter.First()
	if err := iter.Error(); err != nil {
		return false, err
	}
	return ok, nil
}

func (s *Shard) getPluginUserBindingByKey(ctx context.Context, key []byte, uid, pluginNo string) (PluginUserBinding, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return PluginUserBinding{}, false, err
		}
	}
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return PluginUserBinding{}, ok, err
	}
	binding, err := decodePluginUserBindingValue(uid, pluginNo, value)
	if err != nil {
		return PluginUserBinding{}, false, err
	}
	return binding, true, nil
}

func stageBindPluginUser(batch *engine.Batch, hashSlot HashSlot, binding PluginUserBinding) error {
	primaryKey := encodePluginBindingRowKey(hashSlot, binding.UID, binding.PluginNo, pluginBindingPrimaryFamilyID)
	if err := batch.Set(primaryKey, encodePluginUserBindingValue(binding)); err != nil {
		return err
	}
	return batch.Set(encodePluginBindingPluginIndexKey(hashSlot, binding.PluginNo, binding.UID), nil)
}

func stageUnbindPluginUser(batch *engine.Batch, hashSlot HashSlot, uid, pluginNo string) error {
	if err := batch.Delete(encodePluginBindingRowKey(hashSlot, uid, pluginNo, pluginBindingPrimaryFamilyID)); err != nil {
		return err
	}
	return batch.Delete(encodePluginBindingPluginIndexKey(hashSlot, pluginNo, uid))
}

func validatePluginUserBinding(binding PluginUserBinding) error {
	if err := validatePluginUserBindingIdentity(binding.UID, binding.PluginNo); err != nil {
		return err
	}
	if binding.UpdatedAtMS < binding.CreatedAtMS {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validatePluginUserBindingIdentity(uid, pluginNo string) error {
	if err := validatePluginBindingUID(uid); err != nil {
		return err
	}
	return validatePluginBindingPluginNo(pluginNo)
}

func validatePluginBindingUID(uid string) error {
	return validateKeyString(uid)
}

func validatePluginBindingPluginNo(pluginNo string) error {
	return validateKeyString(pluginNo)
}

func validatePluginUserBindingCursor(pluginNo string, cursor PluginUserBindingCursor) error {
	if cursor == (PluginUserBindingCursor{}) {
		return nil
	}
	if cursor.PluginNo != pluginNo || cursor.UID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validatePluginBindingUID(cursor.UID)
}

func pluginBindingToCursor(binding PluginUserBinding) PluginUserBindingCursor {
	return PluginUserBindingCursor{PluginNo: binding.PluginNo, UID: binding.UID}
}

func encodePluginUserBindingValue(binding PluginUserBinding) []byte {
	value := appendValueInt64(nil, binding.CreatedAtMS)
	return appendValueInt64(value, binding.UpdatedAtMS)
}

func decodePluginUserBindingValue(uid, pluginNo string, value []byte) (PluginUserBinding, error) {
	createdAt, rest, err := readValueInt64(value)
	if err != nil {
		return PluginUserBinding{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return PluginUserBinding{}, err
	}
	if len(rest) != 0 {
		return PluginUserBinding{}, dberrors.ErrCorruptValue
	}
	return PluginUserBinding{UID: uid, PluginNo: pluginNo, CreatedAtMS: createdAt, UpdatedAtMS: updatedAt}, nil
}

func decodePluginBindingRowKey(prefix []byte, key []byte) (string, uint16, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return "", 0, false
	}
	pluginNo, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 2 {
		return "", 0, false
	}
	return pluginNo, binary.BigEndian.Uint16(rest), true
}

func decodePluginBindingPluginIndexUID(prefix []byte, key []byte) (string, error) {
	if !bytes.HasPrefix(key, prefix) {
		return "", dberrors.ErrCorruptValue
	}
	uid, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 0 {
		return "", dberrors.ErrCorruptValue
	}
	return uid, nil
}
