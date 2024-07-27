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
		return EmptyDevice, ErrNotFound
	}

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewDeviceColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewDeviceColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var device = EmptyDevice
	err = wk.iterDevice(iter, func(d Device) bool {
		if d.Id == id {
			device = d
			return false
		}
		return true
	})
	if err != nil {
		return EmptyDevice, err
	}

	if device == EmptyDevice {
		return EmptyDevice, ErrNotFound
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
	err = batch.Commit(wk.sync)
	if err != nil {
		return err
	}
	if isCreate {
		err = wk.IncDeviceCount(1)
		if err != nil {
			return err
		}
		return wk.incUserDeviceCount(d.Uid, 1, db)
	}
	return nil
}

func (wk *wukongDB) SearchDevice(req DeviceSearchReq) ([]Device, error) {
	var devices []Device
	currentSize := 0

	iterFnc := func(d Device) bool {
		if currentSize > req.Limit*req.CurrentPage {
			return false
		}
		currentSize++
		if currentSize > (req.CurrentPage-1)*req.Limit && currentSize <= req.CurrentPage*req.Limit {
			devices = append(devices, d)
			return true
		}
		return true
	}

	for _, db := range wk.dbs {
		currentSize = 0

		has, err := wk.searchDeviceByIndex(req, db, iterFnc)
		if err != nil {
			return nil, err
		}
		if has { // 如果有触发索引，则无需全局查询
			continue
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewDeviceColumnKey(0, key.MinColumnKey),
			UpperBound: key.NewDeviceColumnKey(math.MaxUint64, key.MaxColumnKey),
		})
		defer iter.Close()
		if err = wk.iterDevice(iter, iterFnc); err != nil {
			return nil, err
		}
	}
	return devices, nil
}

func (wk *wukongDB) searchDeviceByIndex(req DeviceSearchReq, db *pebble.DB, iterFnc func(d Device) bool) (bool, error) {

	var exist bool
	var ids []uint64
	if req.Uid != "" && req.DeviceFlag != 0 {
		exist = true
		idBytes, closer, err := db.Get(key.NewDeviceIndexUidAndDeviceFlagKey(req.Uid, req.DeviceFlag))
		if err != nil {
			if err == pebble.ErrNotFound {
				return true, nil
			}
			return false, err
		}
		defer closer.Close()

		if len(idBytes) == 0 {
			return true, nil
		}
		id := wk.endian.Uint64(idBytes)
		ids = append(ids, id)

	}

	if !exist && req.Uid != "" {
		exist = true
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewDeviceIndexUidAndDeviceFlagKey(req.Uid, 0),
			UpperBound: key.NewDeviceIndexUidAndDeviceFlagKey(req.Uid, math.MaxUint64),
		})
		defer iter.Close()

		for iter.First(); iter.Valid(); iter.Next() {
			ids = append(ids, wk.endian.Uint64(iter.Value()))
		}
	}

	if !exist {
		return false, nil
	}

	for _, id := range ids {
		device, err := wk.getDeviceById(id, db)
		if err != nil {
			return false, err
		}
		if !iterFnc(device) {
			return true, nil
		}
	}
	return true, nil
}

func (wk *wukongDB) getDeviceById(id uint64, db *pebble.DB) (Device, error) {
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewDeviceColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewDeviceColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var device = EmptyDevice
	err := wk.iterDevice(iter, func(d Device) bool {
		if d.Id == id {
			device = d
			return false
		}
		return true
	})
	if err != nil {
		return EmptyDevice, err
	}

	if device == EmptyDevice {
		return EmptyDevice, ErrNotFound
	}
	return device, nil
}

func (wk *wukongDB) writeDevice(d Device, isCreate bool, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.Uid), []byte(d.Uid), wk.noSync); err != nil {
		return err
	}

	// token
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.Token), []byte(d.Token), wk.noSync); err != nil {
		return err
	}

	// deviceFlag
	var deviceFlagBytes = make([]byte, 8)
	wk.endian.PutUint64(deviceFlagBytes, d.DeviceFlag)
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.DeviceFlag), deviceFlagBytes, wk.noSync); err != nil {
		return err
	}

	// deviceLevel
	if err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.DeviceLevel), []byte{d.DeviceLevel}, wk.noSync); err != nil {
		return err
	}

	// updatedAt
	var nowBytes = make([]byte, 8)
	wk.endian.PutUint64(nowBytes, uint64(time.Now().Unix()))
	err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.UpdatedAt), nowBytes, wk.noSync)
	if err != nil {
		return err
	}

	// createdAt
	if isCreate {
		err = w.Set(key.NewDeviceColumnKey(d.Id, key.TableDevice.Column.CreatedAt), nowBytes, wk.noSync)
		if err != nil {
			return err
		}
	}
	// uid-deviceFlag index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, d.Id)
	if err = w.Set(key.NewDeviceIndexUidAndDeviceFlagKey(d.Uid, d.DeviceFlag), idBytes, wk.noSync); err != nil {
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
			up := time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)
			preDevice.UpdatedAt = &up
		case key.TableDevice.Column.CreatedAt:
			ct := time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)
			preDevice.CreatedAt = &ct

		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preDevice)
	}
	return nil
}
