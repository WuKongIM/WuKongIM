package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddSubscribers(channelId string, channelType uint8, subscribers []string) error {

	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for _, uid := range subscribers {
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
		if err := wk.writeSubscriber(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) GetSubscribers(channelId string, channelType uint8) ([]string, error) {
	iter := wk.shardDB(channelId).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSubscriberPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewSubscriberPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	return wk.parseSubscriber(iter, 0)
}

func (wk *wukongDB) RemoveSubscribers(channelId string, channelType uint8, subscribers []string) error {
	idMap, err := wk.getSubscriberIdsByUids(channelId, channelType, subscribers)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeSubscriber(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) RemoveAllSubscriber(channelId string, channelType uint8) error {

	batch := wk.shardDB(channelId).NewBatch()
	defer batch.Close()

	// 删除数据
	err := batch.DeleteRange(key.NewSubscriberPrimaryKey(channelId, channelType, 0), key.NewSubscriberPrimaryKey(channelId, channelType, math.MaxUint64), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewSubscriberIndexUidLowKey(channelId, channelType), key.NewSubscriberIndexUidHighKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	return batch.Commit(wk.wo)
}

func (wk *wukongDB) AddOrUpdateChannel(channelInfo ChannelInfo) error {

	primaryKey, err := wk.getChannelPrimaryKey(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil {
		return err
	}

	if primaryKey == 0 {
		primaryKey = uint64(wk.prmaryKeyGen.Generate().Int64())
	}

	w := wk.shardDB(channelInfo.ChannelId).NewBatch()
	defer w.Close()
	if err := wk.writeChannelInfo(primaryKey, channelInfo, w); err != nil {
		return err
	}

	return w.Commit(wk.wo)
}

func (wk *wukongDB) GetChannel(channelId string, channelType uint8) (ChannelInfo, error) {

	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return EmptyChannelInfo, err
	}
	if id == 0 {
		return EmptyChannelInfo, nil
	}

	iter := wk.shardDB(channelId).NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelInfoColumnKey(id, [2]byte{0x00, 0x00}),
		UpperBound: key.NewChannelInfoColumnKey(id, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()
	channelInfos, err := wk.parseChannelInfo(iter, 1)
	if err != nil {
		return EmptyChannelInfo, err
	}

	if len(channelInfos) == 0 {
		return EmptyChannelInfo, nil

	}
	return channelInfos[0], nil
}

func (wk *wukongDB) ExistChannel(channelId string, channelType uint8) (bool, error) {
	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return false, err
	}
	return id > 0, nil
}

func (wk *wukongDB) DeleteChannel(channelId string, channelType uint8) error {
	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	batch := wk.shardDB(channelId).NewBatch()
	defer batch.Close()
	// 删除索引
	err = batch.Delete(key.NewChannelInfoIndexKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	// 删除数据
	err = batch.DeleteRange(key.NewChannelInfoColumnKey(id, [2]byte{0x00, 0x00}), key.NewChannelInfoColumnKey(id, [2]byte{0xff, 0xff}), wk.wo)
	if err != nil {
		return err
	}
	return batch.Commit(wk.wo)
}

func (wk *wukongDB) UpdateChannelAppliedIndex(channelId string, channelType uint8, index uint64) error {

	indexBytes := make([]byte, 8)
	wk.endian.PutUint64(indexBytes, index)
	return wk.shardDB(channelId).Set(key.NewChannelCommonColumnKey(channelId, channelType, key.TableChannelCommon.Column.AppliedIndex), indexBytes, wk.wo)
}

func (wk *wukongDB) GetChannelAppliedIndex(channelId string, channelType uint8) (uint64, error) {

	data, closer, err := wk.shardDB(channelId).Get(key.NewChannelCommonColumnKey(channelId, channelType, key.TableChannelCommon.Column.AppliedIndex))
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return wk.endian.Uint64(data), nil
}

func (wk *wukongDB) AddDenylist(channelId string, channelType uint8, uids []string) error {
	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
		if err := wk.writeDenylist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) GetDenylist(channelId string, channelType uint8) ([]string, error) {
	iter := wk.shardDB(channelId).NewIter(&pebble.IterOptions{
		LowerBound: key.NewDenylistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	return wk.parseDenylist(iter, 0)
}

func (wk *wukongDB) RemoveDenylist(channelId string, channelType uint8, uids []string) error {
	idMap, err := wk.getDenylistIdsByUids(channelId, channelType, uids)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeDenylist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) RemoveAllDenylist(channelId string, channelType uint8) error {
	batch := wk.shardDB(channelId).NewBatch()
	defer batch.Close()

	// 删除数据
	err := batch.DeleteRange(key.NewDenylistPrimaryKey(channelId, channelType, 0), key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewDenylistIndexUidLowKey(channelId, channelType), key.NewDenylistIndexUidHighKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	return batch.Commit(wk.wo)
}

func (wk *wukongDB) AddAllowlist(channelId string, channelType uint8, uids []string) error {
	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
		if err := wk.writeAllowlist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) GetAllowlist(channelId string, channelType uint8) ([]string, error) {
	iter := wk.shardDB(channelId).NewIter(&pebble.IterOptions{
		LowerBound: key.NewAllowlistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewAllowlistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	return wk.parseAllowlist(iter, 0)
}

func (wk *wukongDB) RemoveAllowlist(channelId string, channelType uint8, uids []string) error {
	idMap, err := wk.getAllowlistIdsByUids(channelId, channelType, uids)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := wk.shardDB(channelId).NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeAllowlist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.wo)
}

func (wk *wukongDB) RemoveAllAllowlist(channelId string, channelType uint8) error {
	batch := wk.shardDB(channelId).NewBatch()
	defer batch.Close()

	// 删除数据
	err := batch.DeleteRange(key.NewAllowlistPrimaryKey(channelId, channelType, 0), key.NewAllowlistPrimaryKey(channelId, channelType, math.MaxUint64), wk.wo)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewAllowlistIndexUidLowKey(channelId, channelType), key.NewAllowlistIndexUidHighKey(channelId, channelType), wk.wo)
	if err != nil {
		return err
	}

	return batch.Commit(wk.wo)
}

func (wk *wukongDB) removeSubscriber(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewSubscriberColumnKey(channelId, channelType, id, key.TableUser.Column.Uid), wk.wo); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewSubscriberIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getSubscriberIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewSubscriberIndexUidKey(channelId, channelType, uid)
		uidIndexValue, closer, err := wk.shardDB(channelId).Get(uidIndexKey)
		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return nil, err
		}
		defer closer.Close()

		if len(uidIndexValue) == 0 {
			continue
		}
		resultMap[uid] = wk.endian.Uint64(uidIndexValue)
	}
	return resultMap, nil
}

func (wk *wukongDB) removeDenylist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewDenylistColumnKey(channelId, channelType, id, key.TableDenylist.Column.Uid), wk.wo); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewDenylistIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getDenylistIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewDenylistIndexUidKey(channelId, channelType, uid)
		uidIndexValue, closer, err := wk.shardDB(channelId).Get(uidIndexKey)
		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return nil, err
		}
		defer closer.Close()
		if len(uidIndexValue) == 0 {
			continue
		}
		resultMap[uid] = wk.endian.Uint64(uidIndexValue)
	}
	return resultMap, nil
}

func (wk *wukongDB) removeAllowlist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewAllowlistColumnKey(channelId, channelType, id, key.TableAllowlist.Column.Uid), wk.wo); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewAllowlistIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getAllowlistIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewAllowlistIndexUidKey(channelId, channelType, uid)
		uidIndexValue, closer, err := wk.shardDB(channelId).Get(uidIndexKey)

		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return nil, err
		}
		defer closer.Close()
		if len(uidIndexValue) == 0 {
			continue
		}
		resultMap[uid] = wk.endian.Uint64(uidIndexValue)
	}
	return resultMap, nil
}

func (wk *wukongDB) writeChannelInfo(primaryKey uint64, channelInfo ChannelInfo, w pebble.Writer) error {

	var (
		err error
	)
	// channelId
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.ChannelId), []byte(channelInfo.ChannelId), wk.noSync); err != nil {
		return err
	}

	// channelType
	channelTypeBytes := make([]byte, 1)
	channelTypeBytes[0] = channelInfo.ChannelType
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.ChannelType), channelTypeBytes, wk.noSync); err != nil {
		return err
	}

	// ban
	banBytes := make([]byte, 1)
	banBytes[0] = wkutil.BoolToUint8(channelInfo.Ban)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Ban), banBytes, wk.noSync); err != nil {
		return err
	}

	// large
	largeBytes := make([]byte, 1)
	largeBytes[0] = wkutil.BoolToUint8(channelInfo.Large)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Large), largeBytes, wk.noSync); err != nil {
		return err
	}

	// disband
	disbandBytes := make([]byte, 1)
	disbandBytes[0] = wkutil.BoolToUint8(channelInfo.Disband)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Disband), disbandBytes, wk.noSync); err != nil {
		return err
	}

	// channel index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, primaryKey)
	if err = w.Set(key.NewChannelInfoIndexKey(channelInfo.ChannelId, channelInfo.ChannelType), idBytes, wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseChannelInfo(iter *pebble.Iterator, limit int) ([]ChannelInfo, error) {

	var (
		channelInfos   = make([]ChannelInfo, 0, limit)
		preId          uint64
		preChannelInfo ChannelInfo
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseChannelInfoColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {

				channelInfos = append(channelInfos, preChannelInfo)
				if limit != 0 && len(channelInfos) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preChannelInfo = ChannelInfo{}
		}

		switch columnName {
		case key.TableChannelInfo.Column.ChannelId:
			preChannelInfo.ChannelId = string(iter.Value())
		case key.TableChannelInfo.Column.ChannelType:
			preChannelInfo.ChannelType = iter.Value()[0]
		case key.TableChannelInfo.Column.Ban:
			preChannelInfo.Ban = wkutil.Uint8ToBool(iter.Value()[0])
		case key.TableChannelInfo.Column.Large:
			preChannelInfo.Large = wkutil.Uint8ToBool(iter.Value()[0])
		case key.TableChannelInfo.Column.Disband:
			preChannelInfo.Disband = wkutil.Uint8ToBool(iter.Value()[0])

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		channelInfos = append(channelInfos, preChannelInfo)
	}
	return channelInfos, nil
}

func (wk *wukongDB) getChannelPrimaryKey(channelId string, channelType uint8) (uint64, error) {
	primaryKey := key.NewChannelInfoIndexKey(channelId, channelType)
	indexValue, closer, err := wk.shardDB(channelId).Get(primaryKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	if len(indexValue) == 0 {
		return 0, nil
	}
	return wk.endian.Uint64(indexValue), nil
}

func (wk *wukongDB) writeSubscriber(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewSubscriberColumnKey(channelId, channelType, id, key.TableUser.Column.Uid), []byte(uid), wk.wo); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewSubscriberIndexUidKey(channelId, channelType, uid), idBytes, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseSubscriber(iter *pebble.Iterator, limit int) ([]string, error) {

	var (
		subscribers    = make([]string, 0, limit)
		preId          uint64
		preSubscriber  string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseSubscriberColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {
				subscribers = append(subscribers, preSubscriber)
				if limit != 0 && len(subscribers) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preSubscriber = ""
		}
		if columnName == key.TableUser.Column.Uid {
			preSubscriber = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		subscribers = append(subscribers, preSubscriber)
	}
	return subscribers, nil
}

func (wk *wukongDB) writeDenylist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewDenylistColumnKey(channelId, channelType, id, key.TableDenylist.Column.Uid), []byte(uid), wk.wo); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewDenylistIndexUidKey(channelId, channelType, uid), idBytes, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseDenylist(iter *pebble.Iterator, limit int) ([]string, error) {

	var (
		uids           = make([]string, 0, limit)
		preId          uint64
		preUid         string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseDenylistColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {
				uids = append(uids, preUid)
				if limit != 0 && len(uids) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preUid = ""
		}
		if columnName == key.TableDenylist.Column.Uid {
			preUid = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		uids = append(uids, preUid)
	}
	return uids, nil
}

func (wk *wukongDB) writeAllowlist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewAllowlistColumnKey(channelId, channelType, id, key.TableAllowlist.Column.Uid), []byte(uid), wk.wo); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewAllowlistIndexUidKey(channelId, channelType, uid), idBytes, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseAllowlist(iter *pebble.Iterator, limit int) ([]string, error) {

	var (
		uids           = make([]string, 0, limit)
		preId          uint64
		preUid         string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseAllowlistColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {
				uids = append(uids, preUid)
				if limit != 0 && len(uids) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preUid = ""
		}
		if columnName == key.TableAllowlist.Column.Uid {
			preUid = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		uids = append(uids, preUid)
	}
	return uids, nil
}
