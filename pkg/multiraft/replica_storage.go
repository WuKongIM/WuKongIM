package multiraft

import (
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

func (r *Replica) InitialState() (hardState pb.HardState, confState pb.ConfState, err error) {
	replicaRaftStorage := r.opts.ReplicaRaftStorage
	confState, err = replicaRaftStorage.GetConfState(r.opts.ReplicaID)
	if err != nil {
		return
	}
	hardState, err = replicaRaftStorage.GetHardState(r.opts.ReplicaID)
	if err != nil {
		return
	}
	return
}

func (r *Replica) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {

	r.Info("Entries---->", zap.Uint64("lo", lo), zap.Uint64("hi", hi))
	return r.walStore.Entries(lo, hi, maxSize)
}

func (r *Replica) Term(i uint64) (uint64, error) {

	return r.walStore.Term(i)
}

func (r *Replica) LastIndex() (uint64, error) {

	return r.walStore.LastIndex()
}

func (r *Replica) FirstIndex() (uint64, error) {

	return r.walStore.FirstIndex()
}

func (r *Replica) Snapshot() (pb.Snapshot, error) {
	return pb.Snapshot{}, nil
}

func (r *Replica) Append(entries []pb.Entry) error {

	return r.walStore.Append(entries)
}

func (r *Replica) SetHardState(st pb.HardState) error {
	replicaRaftStorage := r.opts.ReplicaRaftStorage
	return replicaRaftStorage.SetHardState(r.opts.ReplicaID, st)
}

func (r *Replica) SetConfState(confState pb.ConfState) error {
	replicaRaftStorage := r.opts.ReplicaRaftStorage
	return replicaRaftStorage.SetConfState(r.opts.ReplicaID, confState)
}
