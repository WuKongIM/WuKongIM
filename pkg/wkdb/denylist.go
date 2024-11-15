package wkdb

import (
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddDenylist(channelId string, channelType uint8, members []Member) error {

	wk.metrics.AddDenylistAdd(1)

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	w := db.NewIndexedBatch()
	defer w.Close()
	for _, member := range members {
		member.Id = key.HashWithString(member.Uid)
		if err := wk.writeDenylist(channelId, channelType, member, w); err != nil {
			return err
		}
	}
	err = wk.incChannelInfoDenylistCount(channelPrimaryId, len(members), w)
	if err != nil {
		wk.Error("incChannelInfoDenylistCount failed", zap.Error(err))
		return err

	}
	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetDenylist(channelId string, channelType uint8) ([]Member, error) {

	wk.metrics.GetDenylistAdd(1)

	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewDenylistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	members := make([]Member, 0)
	err := wk.iterateDenylist(iter, func(m Member) bool {
		members = append(members, m)
		return true
	})
	return members, err
}

func (wk *wukongDB) ExistDenylist(channelId string, channelType uint8, uid string) (bool, error) {

	wk.metrics.ExistDenylistAdd(1)

	uidIndexKey := key.NewDenylistIndexKey(channelId, channelType, key.TableDenylist.Index.Uid, key.HashWithString(uid))
	_, closer, err := wk.channelDb(channelId, channelType).Get(uidIndexKey)
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (wk *wukongDB) RemoveDenylist(channelId string, channelType uint8, uids []string) error {

	wk.metrics.RemoveDenylistAdd(1)

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	members, err := wk.getDenylistByUids(channelId, channelType, uids)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := db.NewIndexedBatch()
	defer w.Close()
	for _, member := range members {
		if err := wk.removeDenylist(channelId, channelType, member, w); err != nil {
			return err
		}
	}

	err = wk.incChannelInfoDenylistCount(channelPrimaryId, -len(members), w)
	if err != nil {
		wk.Error("RemoveDenylist: incChannelInfoDenylistCount failed", zap.Error(err))
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) RemoveAllDenylist(channelId string, channelType uint8) error {

	wk.metrics.RemoveAllDenylistAdd(1)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	db := wk.channelDb(channelId, channelType)

	batch := db.NewIndexedBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewDenylistPrimaryKey(channelId, channelType, 0), key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	if err = wk.deleteAllDenylistIndex(channelId, channelType, batch); err != nil {
		return err
	}

	// 黑名单数量设置为0
	err = wk.incChannelInfoDenylistCount(channelPrimaryId, 0, batch)
	if err != nil {
		wk.Error("RemoveAllDenylist: incChannelInfoDenylistCount failed", zap.Error(err))
		return err

	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) removeDenylist(channelId string, channelType uint8, member Member, w pebble.Writer) error {
	var (
		err error
	)
	// remove all column
	if err = w.DeleteRange(key.NewDenylistColumnKey(channelId, channelType, member.Id, key.MinColumnKey), key.NewDenylistColumnKey(channelId, channelType, member.Id, key.MaxColumnKey), wk.noSync); err != nil {
		return err
	}

	// delete index
	if err = wk.deleteDenylistIndex(channelId, channelType, member, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getDenylistByUids(channelId string, channelType uint8, uids []string) ([]Member, error) {
	members := make([]Member, 0, len(uids))
	db := wk.channelDb(channelId, channelType)
	for _, uid := range uids {
		id := key.HashWithString(uid)
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewDenylistColumnKey(channelId, channelType, id, key.MinColumnKey),
			UpperBound: key.NewDenylistColumnKey(channelId, channelType, id, key.MaxColumnKey),
		})
		defer iter.Close()

		err := wk.iterateDenylist(iter, func(member Member) bool {
			members = append(members, member)
			return true
		})
		if err != nil {
			return nil, err
		}

	}
	return members, nil
}

func (wk *wukongDB) writeDenylist(channelId string, channelType uint8, member Member, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewDenylistColumnKey(channelId, channelType, member.Id, key.TableDenylist.Column.Uid), []byte(member.Uid), wk.noSync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, member.Id)
	if err = w.Set(key.NewDenylistIndexKey(channelId, channelType, key.TableDenylist.Index.Uid, member.Id), idBytes, wk.noSync); err != nil {
		return err
	}

	// createdAt
	if member.CreatedAt != nil {
		ct := uint64(member.CreatedAt.UnixNano())
		createdAt := make([]byte, 8)
		wk.endian.PutUint64(createdAt, ct)
		if err = w.Set(key.NewDenylistColumnKey(channelId, channelType, member.Id, key.TableDenylist.Column.CreatedAt), createdAt, wk.noSync); err != nil {
			return err
		}

		// createdAt second index
		if err = w.Set(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.CreatedAt, ct, member.Id), nil, wk.noSync); err != nil {
			return err
		}

	}

	if member.UpdatedAt != nil {
		// updatedAt
		updatedAt := make([]byte, 8)
		wk.endian.PutUint64(updatedAt, uint64(member.UpdatedAt.UnixNano()))
		if err = w.Set(key.NewDenylistColumnKey(channelId, channelType, member.Id, key.TableDenylist.Column.UpdatedAt), updatedAt, wk.noSync); err != nil {
			return err
		}

		// updatedAt second index
		if err = w.Set(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.UpdatedAt, uint64(member.UpdatedAt.UnixNano()), member.Id), nil, wk.noSync); err != nil {
			return err
		}
	}

	return nil
}

func (wk *wukongDB) iterateDenylist(iter *pebble.Iterator, iterFnc func(member Member) bool) error {
	var (
		preId          uint64
		preMember      Member
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseDenylistColumnKey(iter.Key())
		if err != nil {
			return err
		}
		if id != preId {
			if preId != 0 {
				if !iterFnc(preMember) {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preMember = Member{
				Id: id,
			}
		}

		switch columnName {
		case key.TableDenylist.Column.Uid:
			preMember.Uid = string(iter.Value())
		case key.TableDenylist.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preMember.CreatedAt = &t
			}

		case key.TableDenylist.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preMember.UpdatedAt = &t
			}
		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preMember)
	}
	return nil

}

// 增加频道黑名单数量
func (wk *wukongDB) incChannelInfoDenylistCount(id uint64, count int, batch *pebble.Batch) error {
	wk.dblock.denylistCountLock.lock(id)
	defer wk.dblock.denylistCountLock.unlock(id)
	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.DenylistCount, key.TableChannelInfo.SecondIndex.DenylistCount, count, batch)
}

func (wk *wukongDB) deleteAllDenylistIndex(channelId string, channelType uint8, w pebble.Writer) error {
	var err error
	// uid index
	if err = w.DeleteRange(key.NewDenylistIndexKey(channelId, channelType, key.TableDenylist.Index.Uid, 0), key.NewDenylistIndexKey(channelId, channelType, key.TableDenylist.Index.Uid, math.MaxUint64), wk.noSync); err != nil {
		return err
	}

	// createdAt second index
	if err = w.DeleteRange(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.CreatedAt, 0, 0), key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.CreatedAt, math.MaxUint64, 0), wk.noSync); err != nil {
		return err
	}

	// updatedAt second index
	if err = w.DeleteRange(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.UpdatedAt, 0, 0), key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.UpdatedAt, math.MaxUint64, 0), wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) deleteDenylistIndex(channelId string, channelType uint8, member Member, w pebble.Writer) error {
	var (
		err error
	)
	// uid index
	if err = w.Delete(key.NewDenylistIndexKey(channelId, channelType, key.TableDenylist.Index.Uid, member.Id), wk.noSync); err != nil {
		return err
	}

	// createdAt
	if member.CreatedAt != nil {
		ct := uint64(member.CreatedAt.UnixNano())
		if err = w.Delete(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.CreatedAt, ct, member.Id), wk.noSync); err != nil {
			return err
		}

	}

	if member.UpdatedAt != nil {
		// updatedAt
		if err = w.Delete(key.NewDenylistSecondIndexKey(channelId, channelType, key.TableDenylist.SecondIndex.UpdatedAt, uint64(member.UpdatedAt.UnixNano()), member.Id), wk.noSync); err != nil {
			return err
		}
	}

	return nil
}
