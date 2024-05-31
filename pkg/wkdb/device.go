package wkdb

import (
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) getDeviceId(uid string, deviceFlag uint64) (uint64, error) {
	indexKey := key.NewDeviceIndexUidAndDeviceFlagKey(uid, deviceFlag)
	uidIndexValue, closer, err := wk.shardDB(uid).Get(indexKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	if len(uidIndexValue) == 0 {
		return 0, nil
	}
	return wk.endian.Uint64(uidIndexValue), nil
}

func (wk *wukongDB) GetDevice(uid string, deviceFlag uint64) (Device, error) {

	// 获取设备id
	id, err := wk.getDeviceId(uid, deviceFlag)
	if err != nil {
		return EmptyDevice, err
	}

	if id == 0 {
		return EmptyDevice, ErrDeviceNotExist
	}

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewDeviceColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewDeviceColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var device = EmptyDevice
	wk.iterDevice(iter, func(d Device) bool {
		if d.Id == id {
			device = d
			return false
		}
		return true
	})

	if device == EmptyDevice {
		return EmptyDevice, ErrDeviceNotExist
	}
	return device, nil
}

func (wk *wukongDB) GetDevices(uid string) ([]Device, error) {

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewDeviceIndexUidAndDeviceFlagKey(uid, 0),
		UpperBound: key.NewDeviceIndexUidAndDeviceFlagKey(uid, math.MaxUint64),
	})
	defer iter.Close()

	var devices []Device
	err := wk.iterDevice(iter, func(d Device) bool {
		devices = append(devices, d)
		return true
	})
	if err != nil {
		return nil, err
	}
	return devices, nil
}

func (wk *wukongDB) GetDeviceCount(uid string) (int, error) {
	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewDeviceIndexUidAndDeviceFlagKey(uid, 0),
		UpperBound: key.NewDeviceIndexUidAndDeviceFlagKey(uid, math.MaxUint64),
	})
	defer iter.Close()

	var count int
	err := wk.iterDevice(iter, func(d Device) bool {
		count++
		return true
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (wk *wukongDB) AddOrUpdateDevice(d Device) error {

	isCreate := false
	if d.Id == 0 {
		// 获取device的索引主键
		id, err := wk.getDeviceId(d.Uid, d.DeviceFlag)
		if err != nil {
			return err
		}
		if id != 0 {
			d.Id = id
		} else {
			isCreate = true
			d.Id = uint64(wk.prmaryKeyGen.Generate().Int64())
		}
	}
	db := wk.shardDB(d.Uid)
	batch := db.NewBatch()
	defer batch.Close()
	err := wk.writeDevice(d, isCreate, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) writeDevice(d Device, isCreate bool, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.Uid), []byte(d.Uid), wk.sync); err != nil {
		return err
	}

	// token
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.Token), []byte(d.Token), wk.sync); err != nil {
		return err
	}

	// deviceFlag
	var deviceFlagBytes = make([]byte, 8)
	wk.endian.PutUint64(deviceFlagBytes, d.DeviceFlag)
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.DeviceFlag), deviceFlagBytes, wk.sync); err != nil {
		return err
	}

	// deviceLevel
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.DeviceLevel), []byte{d.DeviceLevel}, wk.sync); err != nil {
		return err
	}

	// updatedAt
	var nowBytes = make([]byte, 8)
	wk.endian.PutUint64(nowBytes, uint64(time.Now().Unix()))
	err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.UpdatedAt), nowBytes, wk.sync)
	if err != nil {
		return err
	}

	// createdAt
	if isCreate {
		err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.CreatedAt), nowBytes, wk.sync)
		if err != nil {
			return err
		}
	}
	// uid-deviceFlag index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, d.Id)
	if err = w.Set(key.NewDeviceIndexUidAndDeviceFlagKey(d.Uid, d.DeviceFlag), idBytes, wk.sync); err != nil {
		return err
	}
	return nil
}

// 解析出设备信息
// id !=0 时，解析出id对应的用户信息
func (wk *wukongDB) iterDevice(iter *pebble.Iterator, iterFnc func(d Device) bool) error {
	var (
		preId          uint64
		preDevice      Device
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParseDeviceColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(preDevice) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			preDevice = Device{Id: primaryKey}
		}

		switch columnName {
		case key.TableDevice.Column.Uid:
			preDevice.Uid = string(iter.Value())
		case key.TableDevice.Column.Token:
			preDevice.Token = string(iter.Value())
		case key.TableDevice.Column.DeviceFlag:
			preDevice.DeviceFlag = wk.endian.Uint64(iter.Value())
		case key.TableDevice.Column.DeviceLevel:
			preDevice.DeviceLevel = iter.Value()[0]
		case key.TableDevice.Column.UpdatedAt:
			preDevice.UpdatedAt = time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)
		case key.TableDevice.Column.CreatedAt:
			preDevice.CreatedAt = time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)

		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preDevice)
	}
	return nil
}
