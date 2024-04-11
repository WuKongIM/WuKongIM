package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) getUserIdByUid(uid string) (uint64, error) {
	uidIndexKey := key.NewUserIndexUidKey(uid)
	uidIndexValue, closer, err := wk.shardDB(uid).Get(uidIndexKey)
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

func (wk *wukongDB) GetUser(uid string, deviceFlag uint8) (User, error) {

	// 获取uid的索引主键
	id, err := wk.getUserIdByUid(uid)
	if err != nil {
		return EmptyUser, err
	}

	db := wk.shardDB(uid)
	// 获取用户信息
	var iter *pebble.Iterator
	if id > 0 {
		iter = db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewUserColumnKey(id, [2]byte{0, 0}),
			UpperBound: key.NewUserColumnKey(id, [2]byte{0xff, 0xff}),
		})
	} else {
		iter = db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewUserColumnKey(0, [2]byte{0, 0}),
			UpperBound: key.NewUserColumnKey(math.MaxUint64, [2]byte{0xff, 0xff}),
		})

	}
	defer iter.Close()

	users, err := wk.parseUser(iter, id, 1)
	if err != nil {
		if err == pebble.ErrNotFound {
			return EmptyUser, nil
		}
		return EmptyUser, err
	}

	if len(users) == 0 {
		return EmptyUser, nil
	}
	return users[0], nil
}

func (wk *wukongDB) UpdateUser(u User) error {

	if u.Id == 0 {
		// 获取uid的索引主键
		id, err := wk.getUserIdByUid(u.Uid)
		if err != nil {
			return err
		}
		if id != 0 {
			u.Id = id
		} else {
			u.Id = uint64(wk.prmaryKeyGen.Generate().Int64())
		}
	}
	db := wk.shardDB(u.Uid)
	batch := db.NewBatch()
	defer batch.Close()
	err := wk.writeUser(u, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.wo)
}

func (wk *wukongDB) writeUser(u User, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.Uid), []byte(u.Uid), wk.wo); err != nil {
		return err
	}

	// token
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.Token), []byte(u.Token), wk.wo); err != nil {
		return err
	}

	// deviceFlag
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.DeviceFlag), []byte{u.DeviceFlag}, wk.wo); err != nil {
		return err
	}

	// deviceLevel
	if err = w.Set(key.NewUserColumnKey(u.Id, key.TableUser.Column.DeviceLevel), []byte{u.DeviceLevel}, wk.wo); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, u.Id)
	if err = w.Set(key.NewUserIndexUidKey(u.Uid), idBytes, wk.wo); err != nil {
		return err
	}
	return nil
}

// 解析出用户信息
// id !=0 时，解析出id对应的用户信息
func (wk *wukongDB) parseUser(iter *pebble.Iterator, id uint64, limit int) ([]User, error) {
	var (
		users          = make([]User, 0, limit)
		preId          uint64
		preUser        User
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParseUserColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}

		if id != 0 && primaryKey != id {
			continue
		}

		if preId != primaryKey {
			if preId != 0 {
				users = append(users, preUser)
				if len(users) >= limit {
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
		case key.TableUser.Column.Token:
			preUser.Token = string(iter.Value())
		case key.TableUser.Column.DeviceFlag:
			preUser.DeviceFlag = iter.Value()[0]
		case key.TableUser.Column.DeviceLevel:
			preUser.DeviceLevel = iter.Value()[0]
		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		users = append(users, preUser)
	}
	return users, nil
}
