package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
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

// PluginUserBindingCursor identifies the last emitted plugin binding in index order.
type PluginUserBindingCursor struct {
	// PluginNo is the plugin number currently being scanned.
	PluginNo string
	// UID is the last emitted UID for PluginNo.
	UID string
}

// BindPluginUser creates or updates one UID to plugin binding idempotently.
func (s *ShardStore) BindPluginUser(ctx context.Context, binding PluginUserBinding) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validatePluginUserBinding(binding); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	batch := s.db.db.NewBatch()
	defer batch.Close()
	existing, err := s.getPluginUserBindingLocked(binding.UID, binding.PluginNo)
	if err == nil {
		binding.CreatedAtMS = existing.CreatedAtMS
		if binding.UpdatedAtMS < existing.UpdatedAtMS {
			binding.UpdatedAtMS = existing.UpdatedAtMS
		}
	} else if err != ErrNotFound {
		return err
	}
	if err := stageBindPluginUser(batch, s.slot, binding); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

// UnbindPluginUser removes one UID to plugin binding idempotently.
func (s *ShardStore) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validatePluginUserBindingIdentity(uid, pluginNo); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	batch := s.db.db.NewBatch()
	defer batch.Close()
	if err := stageUnbindPluginUser(batch, s.slot, uid, pluginNo); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

// ListPluginBindingsByUID lists all plugin bindings for a UID in plugin number order.
func (s *ShardStore) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginUserBinding, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}
	if err := validatePluginBindingUID(uid); err != nil {
		return nil, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodePluginUserBindingPrimaryPrefix(s.slot, uid)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: nextPrefix(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	bindings := make([]PluginUserBinding, 0, 4)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(iter.Key(), prefix) {
			break
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}
		binding, err := decodePluginUserBindingPrimaryRecord(iter.Key(), value, prefix, uid)
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
func (s *ShardStore) ScanPluginBindingsByPluginNo(ctx context.Context, pluginNo string, after PluginUserBindingCursor, limit int) ([]PluginUserBinding, PluginUserBindingCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := validatePluginBindingPluginNo(pluginNo); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if err := validatePluginUserBindingCursor(pluginNo, after); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	if limit <= 0 {
		return nil, PluginUserBindingCursor{}, false, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodePluginUserBindingPluginIndexPrefix(s.slot, pluginNo)
	lowerBound := prefix
	if after.UID != "" {
		lowerBound = nextPrefix(encodePluginUserBindingPluginIndexKey(s.slot, pluginNo, after.UID))
	}
	iter, err := s.db.db.NewIter(&pebble.IterOptions{LowerBound: lowerBound, UpperBound: nextPrefix(prefix)})
	if err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}
	defer iter.Close()

	bindings := make([]PluginUserBinding, 0, limit+1)
	for ok := iter.SeekGE(lowerBound); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, PluginUserBindingCursor{}, false, err
		}
		if !bytes.HasPrefix(iter.Key(), prefix) {
			break
		}
		uid, err := decodePluginUserBindingPluginIndexUID(iter.Key(), prefix)
		if err != nil {
			return nil, PluginUserBindingCursor{}, false, err
		}
		binding, err := s.getPluginUserBindingLocked(uid, pluginNo)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, PluginUserBindingCursor{}, false, err
		}
		bindings = append(bindings, binding)
		if len(bindings) > limit {
			cursor := pluginBindingToCursor(bindings[limit-1])
			return bindings[:limit], cursor, true, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, PluginUserBindingCursor{}, false, err
	}

	cursor := after
	if len(bindings) > 0 {
		cursor = pluginBindingToCursor(bindings[len(bindings)-1])
	}
	return bindings, cursor, false, nil
}

// ExistPluginBindingByUID reports whether a UID has at least one plugin binding.
func (s *ShardStore) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return false, err
	}
	if err := validatePluginBindingUID(uid); err != nil {
		return false, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodePluginUserBindingPrimaryPrefix(s.slot, uid)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: nextPrefix(prefix)})
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

func (s *ShardStore) getPluginUserBindingLocked(uid, pluginNo string) (PluginUserBinding, error) {
	primaryKey := encodePluginUserBindingPrimaryKey(s.slot, uid, pluginNo, pluginUserBindingPrimaryFamilyID)
	value, err := s.db.getValue(primaryKey)
	if err != nil {
		return PluginUserBinding{}, err
	}
	binding, err := decodePluginUserBindingFamilyValue(primaryKey, value)
	if err != nil {
		return PluginUserBinding{}, err
	}
	binding.UID = uid
	binding.PluginNo = pluginNo
	return binding, nil
}

func stageBindPluginUser(batch *pebble.Batch, hashSlot uint16, binding PluginUserBinding) error {
	primaryKey := encodePluginUserBindingPrimaryKey(hashSlot, binding.UID, binding.PluginNo, pluginUserBindingPrimaryFamilyID)
	if err := batch.Set(primaryKey, encodePluginUserBindingFamilyValue(binding, primaryKey), nil); err != nil {
		return err
	}
	indexKey := encodePluginUserBindingPluginIndexKey(hashSlot, binding.PluginNo, binding.UID)
	return batch.Set(indexKey, nil, nil)
}

func stageUnbindPluginUser(batch *pebble.Batch, hashSlot uint16, uid, pluginNo string) error {
	primaryKey := encodePluginUserBindingPrimaryKey(hashSlot, uid, pluginNo, pluginUserBindingPrimaryFamilyID)
	if err := batch.Delete(primaryKey, nil); err != nil {
		return err
	}
	indexKey := encodePluginUserBindingPluginIndexKey(hashSlot, pluginNo, uid)
	return batch.Delete(indexKey, nil)
}

func validatePluginUserBinding(binding PluginUserBinding) error {
	if err := validatePluginUserBindingIdentity(binding.UID, binding.PluginNo); err != nil {
		return err
	}
	if binding.UpdatedAtMS < binding.CreatedAtMS {
		return ErrInvalidArgument
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
	if uid == "" || len(uid) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validatePluginBindingPluginNo(pluginNo string) error {
	if pluginNo == "" || len(pluginNo) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validatePluginUserBindingCursor(pluginNo string, cursor PluginUserBindingCursor) error {
	if cursor == (PluginUserBindingCursor{}) {
		return nil
	}
	if cursor.PluginNo != pluginNo {
		return ErrInvalidArgument
	}
	return validatePluginBindingUID(cursor.UID)
}

func pluginBindingToCursor(binding PluginUserBinding) PluginUserBindingCursor {
	return PluginUserBindingCursor{PluginNo: binding.PluginNo, UID: binding.UID}
}

func encodePluginUserBindingPrimaryPrefix(hashSlot uint16, uid string) []byte {
	key := encodeStatePrefix(hashSlot, PluginUserBindingTable.ID)
	key = appendKeyString(key, uid)
	return key
}

func encodePluginUserBindingPrimaryKey(hashSlot uint16, uid, pluginNo string, familyID uint16) []byte {
	key := encodePluginUserBindingPrimaryPrefix(hashSlot, uid)
	key = appendKeyString(key, pluginNo)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodePluginUserBindingPluginIndexPrefix(hashSlot uint16, pluginNo string) []byte {
	key := encodeIndexPrefix(hashSlot, PluginUserBindingTable.ID, pluginUserBindingPluginIndexID)
	key = appendKeyString(key, pluginNo)
	return key
}

func encodePluginUserBindingPluginIndexKey(hashSlot uint16, pluginNo, uid string) []byte {
	key := encodePluginUserBindingPluginIndexPrefix(hashSlot, pluginNo)
	key = appendKeyString(key, uid)
	return key
}

func decodePluginUserBindingPrimaryRecord(key, value, prefix []byte, uid string) (PluginUserBinding, error) {
	rest := key[len(prefix):]
	pluginNo, rest, err := decodeKeyString(rest)
	if err != nil {
		return PluginUserBinding{}, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return PluginUserBinding{}, ErrCorruptValue
	}
	if familyID != uint64(pluginUserBindingPrimaryFamilyID) {
		return PluginUserBinding{}, fmt.Errorf("%w: invalid plugin binding family %d", ErrCorruptValue, familyID)
	}
	if len(rest[n:]) != 0 {
		return PluginUserBinding{}, ErrCorruptValue
	}
	binding, err := decodePluginUserBindingFamilyValue(key, value)
	if err != nil {
		return PluginUserBinding{}, err
	}
	binding.UID = uid
	binding.PluginNo = pluginNo
	return binding, nil
}

func decodePluginUserBindingPluginIndexUID(key, prefix []byte) (string, error) {
	uid, rest, err := decodeKeyString(key[len(prefix):])
	if err != nil {
		return "", err
	}
	if len(rest) != 0 {
		return "", ErrCorruptValue
	}
	return uid, nil
}

func encodePluginUserBindingFamilyValue(binding PluginUserBinding, key []byte) []byte {
	payload := make([]byte, 0, 24)
	payload = appendIntValue(payload, pluginUserBindingColumnIDCreatedAtMS, 0, binding.CreatedAtMS)
	payload = appendIntValue(payload, pluginUserBindingColumnIDUpdatedAtMS, pluginUserBindingColumnIDCreatedAtMS, binding.UpdatedAtMS)
	return wrapFamilyValue(key, payload)
}

func decodePluginUserBindingFamilyValue(key, value []byte) (PluginUserBinding, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return PluginUserBinding{}, err
	}
	var (
		binding     PluginUserBinding
		colID       uint16
		haveCreated bool
		haveUpdated bool
	)
	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]
		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return PluginUserBinding{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta
		if valueType != valueTypeInt {
			return PluginUserBinding{}, fmt.Errorf("%w: invalid plugin binding column %d type %d", ErrCorruptValue, colID, valueType)
		}
		raw, n := binary.Uvarint(payload)
		if n <= 0 {
			return PluginUserBinding{}, fmt.Errorf("metadb: invalid plugin binding int payload")
		}
		payload = payload[n:]
		switch colID {
		case pluginUserBindingColumnIDCreatedAtMS:
			binding.CreatedAtMS = decodeZigZagInt64(raw)
			haveCreated = true
		case pluginUserBindingColumnIDUpdatedAtMS:
			binding.UpdatedAtMS = decodeZigZagInt64(raw)
			haveUpdated = true
		default:
			return PluginUserBinding{}, fmt.Errorf("%w: invalid plugin binding column %d", ErrCorruptValue, colID)
		}
	}
	if !haveCreated || !haveUpdated {
		return PluginUserBinding{}, fmt.Errorf("%w: missing plugin binding timestamp", ErrCorruptValue)
	}
	return binding, nil
}
