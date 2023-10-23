package multiraft

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	bolt "go.etcd.io/bbolt"
	pb "go.etcd.io/raft/v3/raftpb"
)

type ReplicaRaftStorage interface {
	Open() error
	Close() error
	SetHardState(replicaID uint32, hardState pb.HardState) error
	GetHardState(replicaID uint32) (pb.HardState, error)
	GetConfState(replicaID uint32) (confState pb.ConfState, err error)
	SetConfState(replicaID uint32, confState pb.ConfState) error
	SetApplied(replicaID uint32, applied uint64) error
	GetApplied(replicaID uint32) (uint64, error)
}

type ReplicaMemoryRaftStorage struct {
	hardState map[uint32]pb.HardState
	confState map[uint32]pb.ConfState
	applied   map[uint32]uint64
}

func NewReplicaMemoryRaftStorage() *ReplicaMemoryRaftStorage {
	return &ReplicaMemoryRaftStorage{
		hardState: make(map[uint32]pb.HardState),
		confState: make(map[uint32]pb.ConfState),
		applied:   make(map[uint32]uint64),
	}
}

func (m *ReplicaMemoryRaftStorage) Open() error {
	return nil
}
func (m *ReplicaMemoryRaftStorage) Close() error {
	return nil
}

func (m *ReplicaMemoryRaftStorage) SetHardState(replicaID uint32, hardState pb.HardState) error {
	m.hardState[replicaID] = hardState
	return nil
}

func (m *ReplicaMemoryRaftStorage) GetHardState(replicaID uint32) (pb.HardState, error) {
	return m.hardState[replicaID], nil
}

func (m *ReplicaMemoryRaftStorage) GetConfState(replicaID uint32) (confState pb.ConfState, err error) {
	return m.confState[replicaID], nil
}

func (m *ReplicaMemoryRaftStorage) SetConfState(replicaID uint32, confState pb.ConfState) error {
	m.confState[replicaID] = confState
	return nil
}

func (m *ReplicaMemoryRaftStorage) SetApplied(replicaID uint32, applied uint64) error {
	m.applied[replicaID] = applied
	return nil
}

func (m *ReplicaMemoryRaftStorage) GetApplied(replicaID uint32) (uint64, error) {
	return m.applied[replicaID], nil
}

type ReplicaBoltRaftStorage struct {
	hardStateKey      string
	confStateKey      string
	committedIndexKey string
	appliedKey        string
	raftStateRootKey  []byte
	wklog.Log
	storePath string
	db        *bolt.DB
}

func NewReplicaBoltRaftStorage(storePath string) *ReplicaBoltRaftStorage {
	return &ReplicaBoltRaftStorage{
		Log:               wklog.NewWKLog("ReplicaBoltRaftStorage"),
		raftStateRootKey:  []byte("raftStateRootKey"),
		hardStateKey:      "hardState",
		committedIndexKey: "committedIndex",
		confStateKey:      "confState",
		appliedKey:        "appliedKey",
		storePath:         storePath,
	}
}

func (r *ReplicaBoltRaftStorage) Open() error {
	var err error
	db, err := bolt.Open(r.storePath, 0755, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	r.db = db

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(r.raftStateRootKey)
		if err != nil {
			return err
		}
		return err
	})

	return err
}

func (r *ReplicaBoltRaftStorage) Close() error {
	return r.db.Close()
}

func (r *ReplicaBoltRaftStorage) SetHardState(replicaID uint32, hardState pb.HardState) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.hardStateKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		data, err := hardState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

func (r *ReplicaBoltRaftStorage) GetHardState(replicaID uint32) (pb.HardState, error) {
	var hardState pb.HardState
	err := r.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.hardStateKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		data := bucket.Get(key)
		if data == nil {
			return nil
		}
		return hardState.Unmarshal(data)
	})
	return hardState, err
}

func (r *ReplicaBoltRaftStorage) GetConfState(replicaID uint32) (confState pb.ConfState, err error) {
	err = r.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.confStateKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		data := bucket.Get(key)
		if data == nil {
			return nil
		}
		return confState.Unmarshal(data)
	})
	return confState, err
}

func (r *ReplicaBoltRaftStorage) SetConfState(replicaID uint32, confState pb.ConfState) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.confStateKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		data, err := confState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

func (r *ReplicaBoltRaftStorage) SetApplied(replicaID uint32, applied uint64) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.appliedKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		var data = make([]byte, 8)
		binary.BigEndian.PutUint64(data, applied)
		return bucket.Put(key, data)
	})
}

func (r *ReplicaBoltRaftStorage) GetApplied(replicaID uint32) (uint64, error) {
	var applied uint64
	err := r.db.View(func(tx *bolt.Tx) error {
		key := []byte(fmt.Sprintf("%s.%d", r.appliedKey, replicaID))
		bucket := tx.Bucket(r.raftStateRootKey)
		data := bucket.Get(key)
		if len(data) == 0 {
			return nil
		}
		applied = binary.BigEndian.Uint64(data)
		return nil
	})
	return applied, err
}
