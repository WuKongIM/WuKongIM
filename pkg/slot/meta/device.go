package meta

import (
	"context"

	"github.com/cockroachdb/pebble/v2"
)

type Device struct {
	UID         string
	DeviceFlag  int64
	Token       string
	DeviceLevel int64
}

func (s *ShardStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, error) {
	if err := s.validate(); err != nil {
		return Device{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return Device{}, err
	}
	if uid == "" || len(uid) > maxKeyStringLen {
		return Device{}, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	key := encodeDevicePrimaryKey(s.slot, uid, deviceFlag, devicePrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return Device{}, err
	}

	token, deviceLevel, err := decodeDeviceFamilyValue(key, value)
	if err != nil {
		return Device{}, err
	}

	return Device{
		UID:         uid,
		DeviceFlag:  deviceFlag,
		Token:       token,
		DeviceLevel: deviceLevel,
	}, nil
}

func (s *ShardStore) UpsertDevice(ctx context.Context, d Device) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateDevice(d); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeDevicePrimaryKey(s.slot, d.UID, d.DeviceFlag, devicePrimaryFamilyID)
	value := encodeDeviceFamilyValue(d.Token, d.DeviceLevel, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func validateDevice(d Device) error {
	if d.UID == "" || len(d.UID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}
