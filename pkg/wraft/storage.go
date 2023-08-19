// Copyright (c) 2022 Shanghai Xinbida Network Technology Co., Ltd. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package wraft

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wal"
	bolt "go.etcd.io/bbolt"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type WALStorage struct {
	sync.Mutex
	walLog *wal.Log
	wklog.Log
	metaDB *bolt.DB

	hardStateKey      []byte
	confStateKey      []byte
	committedIndexKey []byte
	appliedKey        []byte
	peersKey          []byte
	walPath           string
	metaPath          string
}

func NewWALStorage(walPath string, metaPath string) *WALStorage {
	w := &WALStorage{
		Log:               wklog.NewWKLog("WALStorage"),
		hardStateKey:      []byte("hardState"),
		committedIndexKey: []byte("committedIndex"),
		confStateKey:      []byte("confState"),
		appliedKey:        []byte("appliedKey"),
		peersKey:          []byte("peersKey"),
		walPath:           walPath,
		metaPath:          metaPath,
	}

	return w
}

func (w *WALStorage) Exist() bool {
	_, err := os.Stat(w.metaPath)
	return err == nil
}

func (w *WALStorage) Open() error {
	lg, err := wal.Open(w.walPath, wal.DefaultOptions)
	if err != nil {
		return err
	}
	w.walLog = lg

	w.metaDB, err = bolt.Open(w.metaPath, 0755, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}

	err = w.metaDB.Batch(func(t *bolt.Tx) error {
		_, err := t.CreateBucketIfNotExists(w.hardStateKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(w.committedIndexKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(w.confStateKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(w.appliedKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(w.peersKey)
		return err
	})
	if err != nil {
		return err
	}
	return nil
}

func (w *WALStorage) Close() error {
	err := w.walLog.Close()
	if err != nil {
		w.Warn("close wal log error", zap.Error(err))
	}
	err = w.metaDB.Close()
	if err != nil {
		w.Warn("close meta db error", zap.Error(err))
	}
	return nil
}

func (w *WALStorage) InitialState() (hardState pb.HardState, confState pb.ConfState, err error) {

	err = w.metaDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.hardStateKey)
		data := bucket.Get(w.hardStateKey)
		if len(data) == 0 {
			return nil
		}
		return hardState.Unmarshal(data)
	})
	if err != nil {
		return
	}
	err = w.metaDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.confStateKey)
		data := bucket.Get(w.confStateKey)
		if len(data) == 0 {
			return nil
		}
		return confState.Unmarshal(data)
	})
	fmt.Println("InitialState------->", hardState.String(), confState.String())
	return
}

func (w *WALStorage) SetHardState(st pb.HardState) error {
	fmt.Println("SetHardState------->", st)
	return w.metaDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.hardStateKey)
		data, err := st.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(w.hardStateKey, data)
	})

}

func (w *WALStorage) HardState() (pb.HardState, error) {
	var hardState pb.HardState
	w.metaDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.hardStateKey)
		data := bucket.Get(w.hardStateKey)
		if len(data) == 0 {
			return nil
		}
		err := hardState.Unmarshal(data)
		if err != nil {
			return err
		}
		return nil
	})
	return hardState, nil
}

func (w *WALStorage) SetConfState(confState pb.ConfState) error {
	err := w.metaDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.confStateKey)
		data, err := confState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(w.confStateKey, data)
	})
	return err
}

func (w *WALStorage) SetApplied(applied uint64) error {
	return w.metaDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.appliedKey)
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, applied)
		return bucket.Put(w.appliedKey, data)
	})
}

func (w *WALStorage) Applied() (uint64, error) {
	var applied uint64
	err := w.metaDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.appliedKey)
		data := bucket.Get(w.appliedKey)
		if len(data) == 0 {
			return nil
		}
		applied = binary.BigEndian.Uint64(data)
		return nil
	})
	return applied, err
}

func (w *WALStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	fmt.Println("Entries------->", lo, hi, maxSize)

	entries := make([]pb.Entry, 0, hi)
	for i := lo; i <= hi-1; i++ {
		ent, err := w.readEntry(i)
		if err != nil {
			if errors.Is(err, wal.ErrNotFound) {
				break
			}
			return nil, err
		}
		entries = append(entries, ent)
	}
	return limitSize(entries, maxSize), nil
}

func (w *WALStorage) Append(entries []pb.Entry) error {
	fmt.Println("Append------->", entries)
	if len(entries) == 0 {
		return nil
	}

	lastIdx, _ := w.LastIndex()

	if lastIdx >= entries[len(entries)-1].Index { //
		return nil
	}

	if lastIdx >= entries[0].Index {
		entries = entries[lastIdx-entries[0].Index+1:]
	}
	if len(entries) == 0 {
		return nil
	}

	if len(entries) > 1 {
		batch := new(wal.Batch)
		for _, ent := range entries {
			entData, err := ent.Marshal()
			if err != nil {
				return err
			}
			batch.Write(ent.Index, entData)
		}
		return w.walLog.WriteBatch(batch)
	} else {
		ent := entries[0]
		entData, err := ent.Marshal()
		if err != nil {
			return err
		}
		return w.walLog.Write(ent.Index, entData)
	}
}

func (w *WALStorage) Term(i uint64) (uint64, error) {

	entry, err := w.readEntry(i)
	if err != nil {
		if errors.Is(err, wal.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return entry.Term, nil
}

func (w *WALStorage) LastIndex() (uint64, error) {
	return w.walLog.LastIndex()
}

func (w *WALStorage) FirstIndex() (uint64, error) {

	firstIndex, err := w.walLog.FirstIndex()
	if err != nil {
		return 0, err
	}
	return firstIndex + 1, nil
}

func (w *WALStorage) Snapshot() (pb.Snapshot, error) {

	return pb.Snapshot{}, nil
}
func (w *WALStorage) ApplySnapshot(snap pb.Snapshot) error {
	panic("no implement Snapshot")
}

func (w *WALStorage) UpdateCommittedIndex(committedIndex uint64) error {
	return w.metaDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(w.committedIndexKey)
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, committedIndex)
		return bucket.Put(w.committedIndexKey, data)
	})
}

func (w *WALStorage) readEntry(index uint64) (pb.Entry, error) {
	data, err := w.walLog.Read(index)
	if err != nil {
		return pb.Entry{}, err
	}
	var ent pb.Entry
	err = ent.Unmarshal(data)
	return ent, err
}

func limitSize(ents []pb.Entry, maxSize uint64) []pb.Entry {
	if len(ents) == 0 {
		return ents
	}
	size := ents[0].Size()
	var limit int
	for limit = 1; limit < len(ents); limit++ {
		size += ents[limit].Size()
		if uint64(size) > maxSize {
			break
		}
	}
	return ents[:limit]
}
