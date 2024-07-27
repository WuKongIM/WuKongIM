package wkdb

import (
	"math"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) GetUser(uid string) (User, error) {

	id, err := wk.getUserId(uid)
	if err != nil {
		return EmptyUser, err
	}

	if id == 0 {
		return EmptyUser, ErrNotFound
	}

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewUserColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewUserColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var usr = EmptyUser

	err = wk.iteratorUser(iter, false, func(u User) bool {
		if u.Id == id {
			usr = u
			return false
		}
		return true
	})
	if err != nil {
		return EmptyUser, err
	}
	if usr == EmptyUser {
		return EmptyUser, ErrNotFound
	}
	return usr, nil
}

func (wk *wukongDB) ExistUser(uid string) (bool, error) {
	id, err := wk.getUserId(uid)
	if err != nil {
		return false, err
	}
	return id != 0, nil
}

func (wk *wukongDB) SearchUser(req UserSearchReq) ([]User, error) {
	if req.Uid != "" {
		us, err := wk.GetUser(req.Uid)
		if err != nil {
			return nil, err
		}
		return []User{us}, nil
	}
	var users []User

	var startId uint64 = req.OffsetId
	var endId uint64 = math.MaxUint64
	if req.OffsetId > 0 {
		if req.Pre {
			startId = uint64(req.OffsetId + 1)
			endId = math.MaxUint64
		} else {
			startId = 0
			endId = uint64(req.OffsetId)
		}
	}

	for _, db := range wk.dbs {
		currentSize := 0
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewUserColumnKey(startId, key.MinColumnKey),
			UpperBound: key.NewUserColumnKey(endId, key.MaxColumnKey),
		})
		defer iter.Close()

		err := wk.iteratorUser(iter, !req.Pre, func(u User) bool {
			if req.OffsetId > 0 {
				if req.Pre {
					if u.Id <= req.OffsetId {
						return false
					}
				} else {
					if u.Id >= req.OffsetId {
						return false
					}
				}
			}

			currentSize++
			if currentSize > req.Limit {
				return false
			}
			users = append(users, u)
			return true
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(users, func(i, j int) bool {
		return users[i].Id > users[j].Id
	})
	if req.Limit > 0 && len(users) > req.Limit {

		if req.Pre {
			users = users[len(users)-req.Limit:]
		} else {
			users = users[:req.Limit]
		}
	}

	return users, nil

}

func (wk *wukongDB) AddOrUpdateUser(u User) error {
	isCreate := false
	if u.Id == 0 {
		// 获取uid的索引主键
		id, err := wk.getUserId(u.Uid)
		if err != nil {
			return err
		}
		if id != 0 {
			u.Id = id
		} else {
			isCreate = true
			u.Id = uint64(wk.prmaryKeyGen.Generate().Int64())
		}
	}
	db := wk.shardDB(u.Uid)
	batch := db.NewBatch()
	defer batch.Close()
	err := wk.writeUser(u, isCreate, batch)
	if err != nil {
		return err
	}
	if isCreate {
		err = wk.IncUserCount(1)
		if err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) incUserDeviceCount(uid string, count int, db *pebble.DB) error {

	wk.dblock.userLock.Lock(uid)
	defer wk.dblock.userLock.unlock(uid)

	id, err := wk.getUserId(uid)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}

	deviceCountBytes, closer, err := db.Get(key.NewUserColumnKey(id, key.TableUser.Column.DeviceCount))
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}
	var deviceCount uint32
	if len(deviceCountBytes) > 0 {
		deviceCount = wk.endian.Uint32(deviceCountBytes)
	} else {
		deviceCountBytes = make([]byte, 4)
	}

	deviceCount += uint32(count)

	wk.endian.PutUint32(deviceCountBytes, deviceCount)

	return db.Set(key.NewUserColumnKey(id, key.TableUser.Column.DeviceCount), deviceCountBytes, wk.sync)

}

func (wk *wukongDB) getUserId(uid string) (uint64, error) {
	indexKey := key.NewUserIndexUidKey(uid)
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

func (wk *wukongDB) writeUser(u User, isCreate bool, w pebble.Writer) error {
	var (
		err error
	)

	// uid
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.Uid), []byte(u.Uid), wk.noSync); err != nil {
		return err
	}

	// updatedAt
	var nowBytes = make([]byte, 8)
	wk.endian.PutUint64(nowBytes, uint64(time.Now().Unix()))
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.UpdatedAt), nowBytes, wk.noSync); err != nil {
		return err
	}

	// createdAt
	if isCreate {
		err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.CreatedAt), nowBytes, wk.noSync)
		if err != nil {
			return err
		}
	}
	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, u.Id)
	if err = w.Set(key.NewUserIndexUidKey(u.Uid), idBytes, wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) iteratorUser(iter *pebble.Iterator, reverse bool, iterFnc func(u User) bool) error {
	var (
		preId          uint64
		preUser        User
		lastNeedAppend bool = true
		hasData        bool = false
	)

	stepFnc := iter.Next
	if reverse {
		if !iter.Last() {
			return nil
		}
		stepFnc = iter.Prev
	} else {
		if !iter.First() {
			return nil
		}
	}

	for ; iter.Valid(); stepFnc() {
		primaryKey, columnName, err := key.ParseUserColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(preUser) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			preUser = User{Id: primaryKey}
		}

		switch columnName {
		case key.TableUser.Column.Uid:
			preUser.Uid = string(iter.Value())
		case key.TableUser.Column.DeviceCount:
			preUser.DeviceCount = wk.endian.Uint32(iter.Value())
		case key.TableUser.Column.OnlineDeviceCount:
			preUser.OnlineDeviceCount = wk.endian.Uint32(iter.Value())
		case key.TableUser.Column.ConnCount:
			preUser.ConnCount = wk.endian.Uint32(iter.Value())
		case key.TableUser.Column.SendMsgCount:
			preUser.SendMsgCount = wk.endian.Uint64(iter.Value())
		case key.TableUser.Column.RecvMsgCount:
			preUser.RecvMsgCount = wk.endian.Uint64(iter.Value())
		case key.TableUser.Column.SendMsgBytes:
			preUser.SendMsgBytes = wk.endian.Uint64(iter.Value())
		case key.TableUser.Column.RecvMsgBytes:
			preUser.RecvMsgBytes = wk.endian.Uint64(iter.Value())
		case key.TableUser.Column.CreatedAt:
			ct := time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)
			preUser.CreatedAt = &ct
		case key.TableUser.Column.UpdatedAt:
			up := time.Unix(int64(wk.endian.Uint64(iter.Value())), 0)
			preUser.UpdatedAt = &up

		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preUser)
	}
	return nil
}
