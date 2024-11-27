package wkdb

import (
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdateTester(tester Tester) error {

	batch := wk.defaultShardBatchDB().NewBatch()

	tester.Id = key.HashWithString(tester.No)

	if err := wk.writeTester(batch, tester); err != nil {
		return err
	}

	return batch.CommitWait()
}

func (wk *wukongDB) GetTester(no string) (Tester, error) {

	db := wk.defaultShardDB()

	id := key.HashWithString(no)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewTesterColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewTesterColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var tester Tester
	err := wk.iteratorTester(iter, func(t Tester) bool {
		tester = t
		return false
	})
	return tester, err
}

func (wk *wukongDB) GetTesters() ([]Tester, error) {

	db := wk.defaultShardDB()

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewTesterColumnKey(0, key.TableTester.Column.No),
		UpperBound: key.NewTesterColumnKey(math.MaxUint64, key.TableTester.Column.No),
	})
	defer iter.Close()

	var testers []Tester
	err := wk.iteratorTester(iter, func(t Tester) bool {
		testers = append(testers, t)
		return true
	})
	return testers, err
}

func (wk *wukongDB) RemoveTester(no string) error {

	batch := wk.defaultShardBatchDB().NewBatch()

	id := key.HashWithString(no)

	batch.DeleteRange(key.NewTesterColumnKey(id, key.MinColumnKey), key.NewTesterColumnKey(id, key.MaxColumnKey))

	return batch.CommitWait()
}

func (wk *wukongDB) writeTester(w *Batch, tester Tester) error {

	w.Set(key.NewTesterColumnKey(tester.Id, key.TableTester.Column.No), []byte(tester.No))
	w.Set(key.NewTesterColumnKey(tester.Id, key.TableTester.Column.Addr), []byte(tester.Addr))

	if tester.CreatedAt != nil {
		ct := uint64(tester.CreatedAt.UnixNano())
		var createdAtBytes = make([]byte, 8)
		wk.endian.PutUint64(createdAtBytes, ct)
		w.Set(key.NewTesterColumnKey(tester.Id, key.TableTester.Column.CreatedAt), createdAtBytes)
	}

	if tester.UpdatedAt != nil {
		up := uint64(tester.UpdatedAt.UnixNano())
		var updatedAtBytes = make([]byte, 8)
		wk.endian.PutUint64(updatedAtBytes, up)
		w.Set(key.NewTesterColumnKey(tester.Id, key.TableTester.Column.UpdatedAt), updatedAtBytes)
	}

	return nil
}

func (wk *wukongDB) iteratorTester(iter *pebble.Iterator, iterFnc func(tester Tester) bool) error {

	var (
		preId          uint64
		preTester      Tester
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParseTesterColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(preTester) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			preTester = Tester{Id: primaryKey}
		}

		switch columnName {
		case key.TableTester.Column.No:
			preTester.No = string(iter.Value())
		case key.TableTester.Column.Addr:
			preTester.Addr = string(iter.Value())
		case key.TableTester.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preTester.CreatedAt = &t
			}

		case key.TableTester.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preTester.UpdatedAt = &t
			}

		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preTester)
	}
	return nil
}
