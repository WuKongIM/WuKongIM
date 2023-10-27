package cluster

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
	pb "go.etcd.io/raft/v3/raftpb"
)

func (c *Cluster) InitialState() (hardState pb.HardState, confState pb.ConfState, err error) {
	err = c.boltdb.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.hardStateKey)
		data := bucket.Get(c.hardStateKey)
		if len(data) == 0 {
			return nil
		}
		return hardState.Unmarshal(data)
	})
	if err != nil {
		return
	}
	err = c.boltdb.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.confStateKey)
		data := bucket.Get(c.confStateKey)
		if len(data) == 0 {
			return nil
		}
		return confState.Unmarshal(data)
	})
	return
}

func (c *Cluster) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {

	return c.raftStorage.Entries(lo, hi, maxSize)
}

func (c *Cluster) Term(i uint64) (uint64, error) {
	return c.raftStorage.Term(i)
}

func (c *Cluster) LastIndex() (uint64, error) {
	return c.raftStorage.LastIndex()
}

func (c *Cluster) FirstIndex() (uint64, error) {
	return c.raftStorage.FirstIndex()
}

func (c *Cluster) Snapshot() (pb.Snapshot, error) {
	return pb.Snapshot{}, nil
}

func (c *Cluster) CreateSnapshot(index uint64, cs *pb.ConfState, data []byte) (pb.Snapshot, error) {
	return pb.Snapshot{}, nil
}

func (c *Cluster) ApplySnapshot(snap pb.Snapshot) error {
	return nil
}

func (c *Cluster) Append(entries []pb.Entry) error {
	return c.raftStorage.Append(entries)
}

func (c *Cluster) SetHardState(st pb.HardState) error {
	return c.boltdb.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.hardStateKey)
		data, err := st.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(c.hardStateKey, data)
	})
}

func (c *Cluster) HardState() (pb.HardState, error) {
	var hardState pb.HardState
	err := c.boltdb.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.hardStateKey)
		data := bucket.Get(c.hardStateKey)
		if len(data) == 0 {
			return nil
		}
		return hardState.Unmarshal(data)
	})
	return hardState, err
}

func (c *Cluster) SetConfState(confState pb.ConfState) error {
	err := c.boltdb.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.confStateKey)
		data, err := confState.Marshal()
		if err != nil {
			return err
		}
		return bucket.Put(c.confStateKey, data)
	})
	return err
}

func (c *Cluster) SetApplied(applied uint64) error {
	return c.boltdb.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.appliedKey)
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, applied)
		return bucket.Put(c.appliedKey, data)
	})
}

func (c *Cluster) Applied() (uint64, error) {
	var applied uint64
	err := c.boltdb.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(c.appliedKey)
		data := bucket.Get(c.appliedKey)
		if len(data) == 0 {
			return nil
		}
		applied = binary.BigEndian.Uint64(data)
		return nil
	})
	return applied, err
}
