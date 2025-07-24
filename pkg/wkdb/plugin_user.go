package wkdb

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdatePluginUsers(pluginUsers []PluginUser) error {
	db := wk.defaultShardDB()
	batch := db.NewBatch()
	defer batch.Close()

	for _, pluginUser := range pluginUsers {
		if err := wk.writePluginUser(pluginUser, batch); err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) RemovePluginUser(pluginNo string, uid string) error {
	db := wk.defaultShardDB()
	batch := db.NewBatch()
	defer batch.Close()
	id := wk.getPluginId(pluginNo, uid)
	err := batch.DeleteRange(key.NewPluginUserColumnKey(id, key.MinColumnKey), key.NewPluginUserColumnKey(id, key.MaxColumnKey), wk.noSync)
	if err != nil {
		return err
	}
	err = wk.deletePluginUserSecondIndex(id, pluginNo, uid, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) getPluginId(pluginNo, uid string) uint64 {
	return key.HashWithString(fmt.Sprintf("%s_%s", pluginNo, uid))
}

func (wk *wukongDB) GetPluginUsers(pluginNo string) ([]PluginUser, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.PluginNo, key.HashWithString(pluginNo), 0),
		UpperBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.PluginNo, key.HashWithString(pluginNo), math.MaxUint64),
	})
	defer iter.Close()

	var pluginUsers []PluginUser
	for iter.First(); iter.Valid(); iter.Next() {
		_, id, err := key.ParsePluginUserSecondIndexKey(iter.Key())
		if err != nil {
			return nil, err
		}
		pluginUser, err := wk.getPluginUserById(id)
		if err != nil {
			return nil, err
		}
		pluginUsers = append(pluginUsers, pluginUser)
	}
	return pluginUsers, nil
}

func (wk *wukongDB) SearchPluginUsers(req SearchPluginUserReq) ([]PluginUser, error) {
	if req.Uid != "" && req.PluginNo != "" {
		id := wk.getPluginId(req.PluginNo, req.Uid)
		pluginUser, err := wk.getPluginUserById(id)
		if err != nil {
			return nil, err
		}
		return []PluginUser{pluginUser}, nil
	}

	if req.Uid != "" {
		return wk.searchPluginUsersByUid(req.Uid)
	}

	if req.PluginNo != "" {
		pluginUsers, err := wk.GetPluginUsers(req.PluginNo)
		if err != nil {
			return nil, err
		}
		return pluginUsers, nil
	}

	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserColumnKey(0, key.MinColumnKey),
		UpperBound: key.NewPluginUserColumnKey(math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	pluginUsers := make([]PluginUser, 0)
	if err := wk.iteratorPluginUser(iter, func(u PluginUser) bool {
		pluginUsers = append(pluginUsers, u)
		return true
	}); err != nil {
		return nil, err
	}

	return pluginUsers, nil

}

func (wk *wukongDB) searchPluginUsersByUid(uid string) ([]PluginUser, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), 0),
		UpperBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), math.MaxUint64),
	})
	defer iter.Close()

	pluginUsers := make([]PluginUser, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		_, id, err := key.ParsePluginUserSecondIndexKey(iter.Key())
		if err != nil {
			return nil, err
		}
		pluginUser, err := wk.getPluginUserById(id)
		if err != nil {
			return nil, err
		}
		pluginUsers = append(pluginUsers, pluginUser)
	}

	return pluginUsers, nil
}

func (wk *wukongDB) getPluginUserById(id uint64) (PluginUser, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewPluginUserColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var pluginUser PluginUser
	if err := wk.iteratorPluginUser(iter, func(u PluginUser) bool {
		pluginUser = u
		return false
	}); err != nil {
		return PluginUser{}, err
	}
	return pluginUser, nil
}

// GetHighestPriorityPluginByUid 获取用户最高优先级的插件
func (wk *wukongDB) GetHighestPriorityPluginByUid(uid string) (string, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), 0),
		UpperBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), math.MaxUint64),
	})
	defer iter.Close()

	var plugin Plugin
	var maxPriority uint32
	for iter.First(); iter.Valid(); iter.Next() {
		_, id, err := key.ParsePluginUserSecondIndexKey(iter.Key())
		if err != nil {
			return "", err
		}
		pluginNo, closer, err := db.Get(key.NewPluginUserColumnKey(id, key.TablePluginUser.Column.PluginNo))
		if closer != nil {
			defer closer.Close()
		}
		if err != nil {
			return "", err
		}
		p, err := wk.GetPlugin(string(pluginNo))
		if err != nil {
			return "", err
		}
		if plugin.No == "" || p.Priority > maxPriority {
			maxPriority = p.Priority
			plugin = p
		}
	}
	return plugin.No, nil
}

func (wk *wukongDB) ExistPluginByUid(uid string) (bool, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), 0),
		UpperBound: key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), math.MaxUint64),
	})
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		return true, nil
	}
	return false, nil
}

func (wk *wukongDB) writePluginUser(p PluginUser, w pebble.Writer) error {
	id := wk.getPluginId(p.PluginNo, p.Uid)
	var err error
	// pluginNo
	if err = w.Set(key.NewPluginUserColumnKey(id, key.TablePluginUser.Column.PluginNo), []byte(p.PluginNo), wk.noSync); err != nil {
		return err
	}

	// uid
	if err = w.Set(key.NewPluginUserColumnKey(id, key.TablePluginUser.Column.Uid), []byte(p.Uid), wk.noSync); err != nil {
		return err
	}

	// uid second index
	if err = w.Set(key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(p.Uid), id), nil, wk.noSync); err != nil {
		return err
	}

	// pluginNo second index
	if err = w.Set(key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.PluginNo, key.HashWithString(p.PluginNo), id), nil, wk.noSync); err != nil {
		return err
	}

	// createdAt
	if p.CreatedAt != nil {
		createdAtBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(createdAtBytes, uint64(p.CreatedAt.UnixNano()))
		if err = w.Set(key.NewPluginUserColumnKey(id, key.TablePluginUser.Column.CreatedAt), createdAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	// updatedAt
	if p.UpdatedAt != nil {
		updatedAtBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(updatedAtBytes, uint64(p.UpdatedAt.UnixNano()))
		if err = w.Set(key.NewPluginUserColumnKey(id, key.TablePluginUser.Column.UpdatedAt), updatedAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	return nil
}

func (wk *wukongDB) iteratorPluginUser(iter *pebble.Iterator, iterFnc func(PluginUser) bool) error {
	var (
		preId          uint64
		prePlugin      PluginUser
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParsePluginUserColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(prePlugin) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			prePlugin = PluginUser{}
		}

		switch columnName {
		case key.TablePluginUser.Column.PluginNo:
			prePlugin.PluginNo = string(iter.Value())
		case key.TablePluginUser.Column.Uid:
			prePlugin.Uid = string(iter.Value())
		case key.TablePluginUser.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				prePlugin.CreatedAt = &t
			}
		case key.TablePluginUser.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				prePlugin.UpdatedAt = &t
			}
		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(prePlugin)
	}
	return nil
}

func (wk *wukongDB) deletePluginUserSecondIndex(id uint64, pluginNo string, uid string, w pebble.Writer) error {
	if err := w.Delete(key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.Uid, key.HashWithString(uid), id), wk.noSync); err != nil {
		return err
	}

	if err := w.Delete(key.NewPluginUserSecondIndexKey(key.TablePluginUser.SecondIndex.PluginNo, key.HashWithString(pluginNo), id), wk.noSync); err != nil {
		return err
	}

	return nil
}
