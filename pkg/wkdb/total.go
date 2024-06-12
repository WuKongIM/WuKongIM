package wkdb

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) IncMessageCount(v int) error {
	wk.dblock.totalLock.lockMessageCount()
	defer wk.dblock.totalLock.unlockMessageCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Message)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)

}

func (wk *wukongDB) IncUserCount(v int) error {
	wk.dblock.totalLock.lockUserCount()
	defer wk.dblock.totalLock.unlockUserCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.User)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) IncDeviceCount(v int) error {
	wk.dblock.totalLock.lockDeviceCount()
	defer wk.dblock.totalLock.unlockDeviceCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Device)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) IncSessionCount(v int) error {
	wk.dblock.totalLock.lockSessionCount()
	defer wk.dblock.totalLock.unlockSessionCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Session)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) IncChannelCount(v int) error {
	wk.dblock.totalLock.lockChannelCount()
	defer wk.dblock.totalLock.unlockChannelCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Channel)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) IncConversationCount(v int) error {
	wk.dblock.totalLock.lockConversationCount()
	defer wk.dblock.totalLock.unlockConversationCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Conversation)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) GetTotalMessageCount() (int, error) {
	wk.dblock.totalLock.lockMessageCount()
	defer wk.dblock.totalLock.unlockMessageCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Message)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}

func (wk *wukongDB) GetTotalUserCount() (int, error) {
	wk.dblock.totalLock.lockUserCount()
	defer wk.dblock.totalLock.unlockUserCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.User)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}

func (wk *wukongDB) GetTotalDeviceCount() (int, error) {
	wk.dblock.totalLock.lockDeviceCount()
	defer wk.dblock.totalLock.unlockDeviceCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Device)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}

func (wk *wukongDB) GetTotalSessionCount() (int, error) {
	wk.dblock.totalLock.lockSessionCount()
	defer wk.dblock.totalLock.unlockSessionCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Session)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil

}

func (wk *wukongDB) GetTotalChannelCount() (int, error) {
	wk.dblock.totalLock.lockChannelCount()
	defer wk.dblock.totalLock.unlockChannelCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Channel)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}

func (wk *wukongDB) GetTotalConversationCount() (int, error) {
	wk.dblock.totalLock.lockConversationCount()
	defer wk.dblock.totalLock.unlockConversationCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.Conversation)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}

func (wk *wukongDB) IncChannelClusterConfigCount(v int) error {
	wk.dblock.totalLock.lockChannelClusterConfigCount()
	defer wk.dblock.totalLock.unlockChannelClusterConfigCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.ChannelClusterConfig)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	} else {
		result = make([]byte, 8)
	}
	count += v
	wk.endian.PutUint64(result, uint64(count))

	return db.Set(keyBytes, result, wk.sync)
}

func (wk *wukongDB) GetTotalChannelClusterConfigCount() (int, error) {
	wk.dblock.totalLock.lockChannelClusterConfigCount()
	defer wk.dblock.totalLock.unlockChannelClusterConfigCount()

	db := wk.defaultShardDB()

	keyBytes := key.NewTotalColumnKey(key.TableTotal.Column.ChannelClusterConfig)
	result, closer, err := db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return 0, err
	}
	if closer != nil {
		defer closer.Close()
	}

	var count int
	if len(result) > 0 {
		count = int(wk.endian.Uint64(result))
	}

	return count, nil
}
