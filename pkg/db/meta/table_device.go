package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// Device stores per-device token state for a UID.
type Device struct {
	UID         string
	DeviceFlag  int64
	Token       string
	DeviceLevel int64
}

// UpsertDevice stores a device regardless of prior existence.
func (s *Shard) UpsertDevice(ctx context.Context, device Device) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(device.UID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	return commitSet(batch, encodeDeviceRowKey(s.hashSlot, device.UID, device.DeviceFlag, devicePrimaryFamilyID), encodeDeviceValue(device))
}

// GetDevice returns one device by UID and device flag.
func (s *Shard) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, bool, error) {
	if err := s.check(ctx); err != nil {
		return Device{}, false, err
	}
	if err := validateKeyString(uid); err != nil {
		return Device{}, false, err
	}
	value, ok, err := s.db.get(encodeDeviceRowKey(s.hashSlot, uid, deviceFlag, devicePrimaryFamilyID))
	if err != nil || !ok {
		return Device{}, ok, err
	}
	device, err := decodeDeviceValue(uid, deviceFlag, value)
	return device, err == nil, err
}

func encodeDeviceValue(device Device) []byte {
	value := appendValueString(nil, device.Token)
	value = appendValueInt64(value, device.DeviceLevel)
	return value
}

func decodeDeviceValue(uid string, deviceFlag int64, value []byte) (Device, error) {
	token, rest, err := readValueString(value)
	if err != nil {
		return Device{}, err
	}
	deviceLevel, rest, err := readValueInt64(rest)
	if err != nil {
		return Device{}, err
	}
	if len(rest) != 0 {
		return Device{}, dberrors.ErrCorruptValue
	}
	return Device{UID: uid, DeviceFlag: deviceFlag, Token: token, DeviceLevel: deviceLevel}, nil
}
