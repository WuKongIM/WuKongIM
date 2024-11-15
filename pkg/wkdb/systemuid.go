package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddSystemUids(uids []string) error {

	wk.metrics.AddSystemUidsAdd(1)

	w := wk.defaultShardDB().NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := key.HashWithString(uid)
		if err := wk.writeSystemUid(id, uid, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.sync)
}

func (wk *wukongDB) RemoveSystemUids(uids []string) error {

	wk.metrics.RemoveSystemUidsAdd(1)

	w := wk.defaultShardDB().NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := key.HashWithString(uid)
		if err := wk.removeSystemUid(id, w); err != nil {
			return err
		}
	}
	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetSystemUids() ([]string, error) {

	wk.metrics.GetSystemUidsAdd(1)

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewSystemUidColumnKey(0, key.TableSystemUid.Column.Uid),
		UpperBound: key.NewSystemUidColumnKey(math.MaxUint64, key.TableSystemUid.Column.Uid),
	})
	defer iter.Close()
	return wk.parseSystemUid(iter)
}

func (wk *wukongDB) writeSystemUid(id uint64, uid string, w *pebble.Batch) error {
	return w.Set(key.NewSystemUidColumnKey(id, key.TableSystemUid.Column.Uid), []byte(uid), wk.noSync)
}

func (wk *wukongDB) removeSystemUid(id uint64, w *pebble.Batch) error {
	return w.Delete(key.NewSystemUidColumnKey(id, key.TableSystemUid.Column.Uid), wk.noSync)
}

func (wk *wukongDB) parseSystemUid(iter *pebble.Iterator) ([]string, error) {
	var uids []string
	for iter.First(); iter.Valid(); iter.Next() {
		uids = append(uids, string(iter.Value()))
	}
	return uids, nil
}
