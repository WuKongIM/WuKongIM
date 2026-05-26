package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// Device stores per-device token state for a UID.
type Device struct {
	UID         string
	DeviceFlag  int64
	Token       string
	DeviceLevel int64
}

var deviceTable = registerMetaTable(TableSpec[Device]{
	ID:   TableIDDevice,
	Name: "device",
	Columns: []schema.Column{
		{ID: columnIDStringKey, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: columnIDIntKey, Name: "device_flag", Type: schema.TypeInt64, Required: true},
		{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: devicePrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue}}},
	Primary: PrimarySpec[Device]{
		IndexID:  devicePrimaryIndexID,
		FamilyID: devicePrimaryFamilyID,
		Name:     "pk_device",
		Columns:  []uint16{columnIDStringKey, columnIDIntKey},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered},
		Key:      func(device Device) KeyParts { return KeyParts{String(device.UID), Int64Ordered(device.DeviceFlag)} },
	},
	Validate: validateDevice,
	EncodeValue: func(device Device) ([]byte, error) {
		return encodeDeviceValue(device), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (Device, error) {
		return decodeDeviceValue(primary[0].S, primary[1].I64, value)
	},
})

// DeviceTable describes the device table schema.
var DeviceTable = deviceTable.Schema()

// UpsertDevice stores a device regardless of prior existence.
func (s *Shard) UpsertDevice(ctx context.Context, device Device) error {
	return deviceTable.Upsert(ctx, s, device)
}

// GetDevice returns one device by UID and device flag.
func (s *Shard) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, bool, error) {
	if err := validateKeyString(uid); err != nil {
		return Device{}, false, err
	}
	return deviceTable.Get(ctx, s, KeyParts{String(uid), Int64Ordered(deviceFlag)})
}

func validateDevice(device Device) error {
	return validateKeyString(device.UID)
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
