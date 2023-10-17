package multiraft

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft/wal"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	bolt "go.etcd.io/bbolt"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type SateStorage interface {
	InitialState() (hardState pb.HardState, confState pb.ConfState, err error)
	SetHardState(st pb.HardState) error
	HardState() (pb.HardState, error)
	SetConfState(confState pb.ConfState) error
	SetApplied(applied uint64) error
	Applied() (uint64, error)
}

type LogStorage struct {
	walStore    *WALStorage
	id          uint64
	sateStorage SateStorage
}

func NewLogStorage(id uint64, walStore *WALStorage, sateStorage SateStorage) *LogStorage {
	return &LogStorage{
		walStore:    walStore,
		sateStorage: sateStorage,
	}
}

func (l *LogStorage) InitialState() (hardState pb.HardState, confState pb.ConfState, err error) {

	return l.sateStorage.InitialState()
}

func (l *LogStorage) SetHardState(st pb.HardState) error {
	return l.sateStorage.SetHardState(st)
}

func (l *LogStorage) HardState() (pb.HardState, error) {
	return l.sateStorage.HardState()
}

func (l *LogStorage) SetConfState(confState pb.ConfState) error {
	return l.sateStorage.SetConfState(confState)
}

func (l *LogStorage) SetApplied(applied uint64) error {
	return l.sateStorage.SetApplied(applied)
}

func (l *LogStorage) Applied() (uint64, error) {
	return l.sateStorage.Applied()
}

func (l *LogStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	return l.walStore.Entries(lo, hi, maxSize)
}

func (l *LogStorage) Append(entries []pb.Entry) error {
	return l.walStore.Append(entries)
}

func (l *LogStorage) Term(i uint64) (uint64, error) {
	return l.walStore.Term(i)
}

func (l *LogStorage) LastIndex() (uint64, error) {
	return l.walStore.LastIndex()
}

func (l *LogStorage) FirstIndex() (uint64, error) {
	return l.walStore.FirstIndex()
}

func (l *LogStorage) Snapshot() (pb.Snapshot, error) {
	return l.walStore.Snapshot()
}

func (l *LogStorage) ApplySnapshot(snap pb.Snapshot) error {
	return l.walStore.ApplySnapshot(snap)
}

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

func NewWALStorage(walPath string) *WALStorage {
	w := &WALStorage{
		Log:               wklog.NewWKLog("WALStorage"),
		hardStateKey:      []byte("hardState"),
		committedIndexKey: []byte("committedIndex"),
		confStateKey:      []byte("confState"),
		appliedKey:        []byte("appliedKey"),
		peersKey:          []byte("peersKey"),
		walPath:           walPath,
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

	// firstIndex, err := w.walLog.FirstIndex()
	// if err != nil {
	// 	return 0, err
	// }
	// return firstIndex + 1, nil
	return 1, nil // TODO: 因为没有快照，所以这里应该永远返回1
}

func (w *WALStorage) Snapshot() (pb.Snapshot, error) {

	return pb.Snapshot{}, nil
}
func (w *WALStorage) ApplySnapshot(snap pb.Snapshot) error {
	panic("no implement Snapshot")
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
