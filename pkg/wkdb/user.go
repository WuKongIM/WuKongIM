package wkdb

import (
	"math"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) GetUser(uid string) (User, error) {

	wk.metrics.GetUserAdd(1)

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

	err = wk.iteratorUser(iter, func(u User) bool {
		if u.Id == id {
			usr = u
			return false
		}
		return true
	})
	if err != nil {
		return EmptyUser, err
	}
	return usr, nil
}

func (wk *wukongDB) ExistUser(uid string) (bool, error) {

	wk.metrics.ExistUserAdd(1)

	return wk.existUser(uid)
}

func (wk *wukongDB) existUser(uid string) (bool, error) {
	user, err := wk.GetUser(uid)
	if err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}
	if IsEmptyUser(user) {
		return false, nil
	}
	return true, nil
}

func (wk *wukongDB) SearchUser(req UserSearchReq) ([]User, error) {

	wk.metrics.SearchUserAdd(1)

	if req.Uid != "" {
		us, err := wk.GetUser(req.Uid)
		if err != nil {
			return nil, err
		}
		return []User{us}, nil
	}
	iterFnc := func(users *[]User) func(u User) bool {
		currentSize := 0
		return func(u User) bool {
			if req.Pre {
				if req.OffsetCreatedAt > 0 && u.CreatedAt != nil && u.CreatedAt.UnixNano() <= req.OffsetCreatedAt {
					return false
				}
			} else {
				if req.OffsetCreatedAt > 0 && u.CreatedAt != nil && u.CreatedAt.UnixNano() >= req.OffsetCreatedAt {
					return false
				}
			}

			if currentSize > req.Limit {
				return false
			}
			currentSize++
			*users = append(*users, u)

			return true
		}
	}

	allUsers := make([]User, 0, req.Limit*len(wk.dbs))
	for _, db := range wk.dbs {
		users := make([]User, 0, req.Limit)
		fnc := iterFnc(&users)

		start := uint64(req.OffsetCreatedAt)
		end := uint64(math.MaxUint64)
		if req.OffsetCreatedAt > 0 {
			if req.Pre {
				start = uint64(req.OffsetCreatedAt + 1)
				end = uint64(math.MaxUint64)
			} else {
				start = 0
				end = uint64(req.OffsetCreatedAt)
			}
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewUserSecondIndexKey(key.TableUser.SecondIndex.CreatedAt, start, 0),
			UpperBound: key.NewUserSecondIndexKey(key.TableUser.SecondIndex.CreatedAt, end, 0),
		})
		defer iter.Close()

		var iterStepFnc func() bool
		if req.Pre {
			if !iter.First() {
				continue
			}
			iterStepFnc = iter.Next
		} else {
			if !iter.Last() {
				continue
			}
			iterStepFnc = iter.Prev
		}
		for ; iter.Valid(); iterStepFnc() {
			_, id, err := key.ParseUserSecondIndexKey(iter.Key())
			if err != nil {
				return nil, err
			}

			dataIter := db.NewIter(&pebble.IterOptions{
				LowerBound: key.NewUserColumnKey(id, key.MinColumnKey),
				UpperBound: key.NewUserColumnKey(id, key.MaxColumnKey),
			})
			defer dataIter.Close()

			var u User
			err = wk.iteratorUser(dataIter, func(user User) bool {
				u = user
				return false
			})
			if err != nil {
				return nil, err
			}
			if !fnc(u) {
				break
			}
		}
		allUsers = append(allUsers, users...)
	}
	// 降序排序
	sort.Slice(allUsers, func(i, j int) bool {
		return allUsers[i].CreatedAt.UnixNano() > allUsers[j].CreatedAt.UnixNano()
	})

	if req.Limit > 0 && len(allUsers) > req.Limit {
		if req.Pre {
			allUsers = allUsers[len(allUsers)-req.Limit:]
		} else {
			allUsers = allUsers[:req.Limit]
		}
	}

	return allUsers, nil

}

func (wk *wukongDB) AddUser(u User) error {

	wk.metrics.AddUserAdd(1)

	u.Id = key.HashWithString(u.Uid)

	oldUser, err := wk.GetUser(u.Uid)
	if err != nil && err != ErrNotFound {
		return err
	}

	db := wk.sharedBatchDB(u.Uid)
	batch := db.NewBatch()

	exist := !IsEmptyUser(oldUser)

	if exist {
		oldUser.CreatedAt = nil
		err = wk.deleteUserIndex(oldUser, batch)
		if err != nil {
			return err
		}
	}
	if exist {
		u.CreatedAt = nil // 不允许更新创建时间
	}
	err = wk.writeUser(u, batch)
	if err != nil {
		return err
	}

	return batch.CommitWait()
}

func (wk *wukongDB) UpdateUser(u User) error {

	wk.metrics.UpdateUserAdd(1)

	u.Id = key.HashWithString(u.Uid)

	oldUser, err := wk.GetUser(u.Uid)
	if err != nil && err != ErrNotFound {
		return err
	}

	db := wk.sharedBatchDB(u.Uid)
	batch := db.NewBatch()

	exist := !IsEmptyUser(oldUser)

	if exist {
		oldUser.CreatedAt = nil
		err = wk.deleteUserIndex(oldUser, batch)
		if err != nil {
			return err
		}
	}

	if exist {
		u.CreatedAt = nil // 不允许更新创建时间
	}
	err = wk.writeUser(u, batch)
	if err != nil {
		return err
	}

	return batch.CommitWait()
}

// func (wk *wukongDB) incUserDeviceCount(uid string, count int, db *pebble.DB) error {

// 	wk.dblock.userLock.Lock(uid)
// 	defer wk.dblock.userLock.unlock(uid)

// 	id, err := wk.getUserId(uid)
// 	if err != nil {
// 		return err
// 	}
// 	if id == 0 {
// 		return nil
// 	}

// 	deviceCountBytes, closer, err := db.Get(key.NewUserColumnKey(id, key.TableUser.Column.DeviceCount))
// 	if err != nil && err != pebble.ErrNotFound {
// 		return err
// 	}
// 	if closer != nil {
// 		defer closer.Close()
// 	}
// 	var deviceCount uint32
// 	if len(deviceCountBytes) > 0 {
// 		deviceCount = wk.endian.Uint32(deviceCountBytes)
// 	} else {
// 		deviceCountBytes = make([]byte, 4)
// 	}

// 	deviceCount += uint32(count)

// 	wk.endian.PutUint32(deviceCountBytes, deviceCount)

// 	return db.Set(key.NewUserColumnKey(id, key.TableUser.Column.DeviceCount), deviceCountBytes, wk.sync)

// }

func (wk *wukongDB) getUserId(uid string) (uint64, error) {
	// indexKey := key.NewUserIndexUidKey(uid)
	// uidIndexValue, closer, err := wk.shardDB(uid).Get(indexKey)
	// if err != nil {
	// 	if err == pebble.ErrNotFound {
	// 		return 0, nil
	// 	}
	// 	return 0, err
	// }
	// defer closer.Close()

	// if len(uidIndexValue) == 0 {
	// 	return 0, nil
	// }
	// return wk.endian.Uint64(uidIndexValue), nil

	return key.HashWithString(uid), nil
}

func (wk *wukongDB) writeUser(u User, w *Batch) error {
	var (
		err error
	)

	// uid
	w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.Uid), []byte(u.Uid))

	if u.CreatedAt != nil {
		// createdAt
		ct := uint64(u.CreatedAt.UnixNano())
		var createdAtBytes = make([]byte, 8)
		wk.endian.PutUint64(createdAtBytes, ct)
		w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.CreatedAt), createdAtBytes)

	}

	if u.UpdatedAt != nil {
		// updatedAt
		up := uint64(u.UpdatedAt.UnixNano())
		var updatedAtBytes = make([]byte, 8)
		wk.endian.PutUint64(updatedAtBytes, up)
		w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.UpdatedAt), updatedAtBytes)

	}

	// write index
	if err = wk.writeUserIndex(u, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) writeUserIndex(u User, w *Batch) error {
	if u.CreatedAt != nil {
		// createdAt
		w.Set(key.NewUserSecondIndexKey(key.TableUser.SecondIndex.CreatedAt, uint64(u.CreatedAt.UnixNano()), u.Id), nil)
	}

	if u.UpdatedAt != nil {
		// updatedAt
		w.Set(key.NewUserSecondIndexKey(key.TableUser.SecondIndex.UpdatedAt, uint64(u.UpdatedAt.UnixNano()), u.Id), nil)
	}

	return nil
}

func (wk *wukongDB) deleteUserIndex(u User, w *Batch) error {
	if u.CreatedAt != nil {
		// createdAt
		w.Delete(key.NewUserSecondIndexKey(key.TableUser.SecondIndex.CreatedAt, uint64(u.CreatedAt.UnixNano()), u.Id))

	}

	if u.UpdatedAt != nil {
		// updatedAt
		w.Delete(key.NewUserSecondIndexKey(key.TableUser.SecondIndex.UpdatedAt, uint64(u.UpdatedAt.UnixNano()), u.Id))

	}
	return nil
}

func (wk *wukongDB) iteratorUser(iter *pebble.Iterator, iterFnc func(u User) bool) error {
	var (
		preId          uint64
		preUser        User
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
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
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preUser.CreatedAt = &t
			}

		case key.TableUser.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preUser.UpdatedAt = &t
			}

		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preUser)
	}
	return nil
}
