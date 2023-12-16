package raftgroup

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft/wal"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	bolt "go.etcd.io/bbolt"
	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type IRaftStorage interface {
	raft.Storage
	Append(entries []pb.Entry) error
	SetHardState(hardState pb.HardState) error
	SetConfState(confState *pb.ConfState) error
	Open() error
	Close() error
}

type RaftStorage struct {
	nodeID  uint64
	shardID uint32
	dataDir string
	walLog  *wal.Log
	walPath string
	wklog.Log
	sync.Mutex
	shardStorage *shardStorage
}

func NewRaftStorage(dataDir string, nodeID uint64, shardID uint32, shardStorage *shardStorage) *RaftStorage {

	return &RaftStorage{
		shardID:      shardID,
		nodeID:       nodeID,
		dataDir:      dataDir,
		walPath:      path.Join(dataDir, "wal", fmt.Sprintf("%d", shardID)),
		Log:          wklog.NewWKLog(fmt.Sprintf("RaftStorage[%d][%d]", nodeID, shardID)),
		shardStorage: shardStorage,
	}
}

func (r *RaftStorage) Open() error {
	lg, err := wal.Open(r.walPath, wal.DefaultOptions)
	if err != nil {
		return err
	}
	r.walLog = lg
	return nil
}

func (r *RaftStorage) Close() error {
	err := r.walLog.Close()
	if err != nil {
		r.Warn("close wal log error", zap.Error(err))
	}

	return nil
}

func (r *RaftStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	r.Lock()
	defer r.Unlock()
	entries := make([]pb.Entry, 0, hi)
	for i := lo; i <= hi-1; i++ {
		ent, err := r.readEntry(i)
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

func (r *RaftStorage) SetHardState(hardState pb.HardState) error {

	return r.shardStorage.SetHardState(r.shardID, hardState)
}

func (r *RaftStorage) SetConfState(confState *pb.ConfState) error {
	return r.shardStorage.SetConfState(r.shardID, *confState)
}

func (r *RaftStorage) Append(entries []pb.Entry) error {
	r.Lock()
	defer r.Unlock()
	if len(entries) == 0 {
		return nil
	}
	lastIdx, _ := r.LastIndex()
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
		return r.walLog.WriteBatch(batch)
	} else {
		ent := entries[0]
		entData, err := ent.Marshal()
		if err != nil {
			return err
		}
		return r.walLog.Write(ent.Index, entData)
	}
}

func (r *RaftStorage) InitialState() (pb.HardState, pb.ConfState, error) {
	hardState, err := r.shardStorage.GetHardState(r.shardID)
	if err != nil {
		return pb.HardState{}, pb.ConfState{}, err
	}
	confState, err := r.shardStorage.GetConfState(r.shardID)
	if err != nil {
		return pb.HardState{}, pb.ConfState{}, err
	}
	return hardState, confState, nil
}

func (r *RaftStorage) Term(i uint64) (uint64, error) {
	r.Lock()
	defer r.Unlock()
	entry, err := r.readEntry(i)
	if err != nil {
		if errors.Is(err, wal.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return entry.Term, nil
}

func (r *RaftStorage) LastIndex() (uint64, error) {
	return r.walLog.LastIndex()
}

func (r *RaftStorage) FirstIndex() (uint64, error) {

	firstIndex, err := r.walLog.FirstIndex()
	if err != nil {
		return 0, err
	}
	if firstIndex == 0 {
		return 1, nil
	}
	return firstIndex, nil
}

func (r *RaftStorage) Snapshot() (pb.Snapshot, error) {
	return pb.Snapshot{}, nil
}

func (r *RaftStorage) readEntry(index uint64) (pb.Entry, error) {
	data, err := r.walLog.Read(index)
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

type shardStorage struct {
	hardStateKey     string
	raftStateRootKey []byte
	appliedKey       string
	wklog.Log
	db           *bolt.DB
	storePath    string
	confStateKey string
}

func newShardStorage(dataDir string) *shardStorage {
	return &shardStorage{
		hardStateKey:     "hardState",
		Log:              wklog.NewWKLog("shardStorage"),
		raftStateRootKey: []byte("raftStateRootKey"),
		appliedKey:       "appliedKey",
		confStateKey:     "confState",
		storePath:        path.Join(dataDir, "shardmeta.db"),
	}
}

func (s *shardStorage) Open() error {
	var err error
	db, err := bolt.Open(s.storePath, 0755, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	s.db = db
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(s.raftStateRootKey)
		if err != nil {
			return err
		}
		return err
	})
	return err
}

func (s *shardStorage) Close() error {
	return s.db.Close()
}

func (s *shardStorage) SetHardState(shardID uint32, hardState pb.HardState) error {

	return s.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.hardStateKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		data, err := hardState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

func (s *shardStorage) GetHardState(shardID uint32) (pb.HardState, error) {
	var hardState pb.HardState
	err := s.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.hardStateKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		data := bucket.Get(key)
		if data == nil {
			return nil
		}
		return hardState.Unmarshal(data)
	})
	return hardState, err
}

func (s *shardStorage) SetApplied(shardID uint32, applied uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.appliedKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		var data = make([]byte, 8)
		binary.BigEndian.PutUint64(data, applied)
		return bucket.Put(key, data)
	})
}

func (s *shardStorage) GetApplied(shardID uint32) (uint64, error) {
	var applied uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.appliedKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		data := bucket.Get(key)
		if len(data) == 0 {
			return nil
		}
		applied = binary.BigEndian.Uint64(data)
		return nil
	})
	return applied, err
}

func (s *shardStorage) GetConfState(shardID uint32) (confState pb.ConfState, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.confStateKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		data := bucket.Get(key)
		if data == nil {
			return nil
		}
		return confState.Unmarshal(data)
	})
	return confState, err
}

func (s *shardStorage) SetConfState(shardID uint32, confState pb.ConfState) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", s.confStateKey, shardID))
		bucket := tx.Bucket(s.raftStateRootKey)
		data, err := confState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

type MemoryStorage struct {
	*raft.MemoryStorage
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		MemoryStorage: raft.NewMemoryStorage(),
	}
}

func (m *MemoryStorage) Open() error {
	return nil
}

func (m *MemoryStorage) Close() error {
	return nil
}

func (m *MemoryStorage) SetConfState(confState *pb.ConfState) error {
	return nil
}
