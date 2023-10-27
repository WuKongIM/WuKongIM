package multiraft

import (
	"context"
	"fmt"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Replica struct {
	opts     *ReplicaOptions
	raftNode *Raft
	walStore *WALStorage
	wklog.Log
}

func NewReplica(opts *ReplicaOptions) *Replica {

	walPath := path.Join(opts.DataDir, "wal")
	r := &Replica{
		opts:     opts,
		walStore: NewWALStorage(walPath),
		Log:      wklog.NewWKLog(fmt.Sprintf("Replica[%d]", opts.ReplicaID)),
	}
	raftOpts := r.getRaftOptions(opts)
	r.raftNode = NewRaft(raftOpts)

	return r
}

func (r *Replica) Start() error {
	err := r.walStore.Open()
	if err != nil {
		return err
	}
	applied, err := r.opts.ReplicaRaftStorage.GetApplied(r.opts.ReplicaID)
	if err != nil {
		return err
	}
	restart, err := r.isRestart()
	if err != nil {
		return err
	}
	r.raftNode.opts.Restart = restart

	r.raftNode.opts.Applied = applied
	err = r.raftNode.Start()
	if err != nil {
		return err
	}

	return nil
}

func (r *Replica) OnRaftMessage(m *RaftMessageReq) {
	r.raftNode.OnRaftMessage(m.Message)
}

func (r *Replica) isRestart() (bool, error) {
	hardstate, err := r.opts.ReplicaRaftStorage.GetHardState(r.opts.ReplicaID)
	if err != nil {
		return false, err
	}
	if !raft.IsEmptyHardState(hardstate) {
		return true, nil
	}
	lastIndex, err := r.raftNode.storage.LastIndex()
	if err != nil {
		return false, err
	}
	if lastIndex > 0 {
		return true, nil
	}
	return false, nil
}

func (r *Replica) Stop() {
	r.raftNode.Stop()
	r.walStore.Close()
}

func (r *Replica) Propose(ctx context.Context, data []byte) error {
	return r.raftNode.Propose(ctx, data)
}

func (r *Replica) getRaftOptions(opts *ReplicaOptions) *RaftOptions {
	logPrefix := fmt.Sprintf("raft[%d-%d]", opts.PeerID, opts.ReplicaID)
	raftOpts := NewRaftOptions()
	raftOpts.ID = opts.PeerID
	raftOpts.Logger = NewLogger(logPrefix)
	raftOpts.RaftStorage = r
	raftOpts.logPrefix = logPrefix
	raftOpts.Peers = opts.Peers
	raftOpts.LeaderChange = opts.LeaderChange
	raftOpts.OnApply = func(entries []raftpb.Entry) error {
		err := opts.ReplicaRaftStorage.SetApplied(opts.ReplicaID, entries[len(entries)-1].Index)
		if err != nil {
			r.Warn("set applied error", zap.Error(err))
		}
		return opts.StateMachine.Apply(opts.ReplicaID, entries)
	}
	raftOpts.OnSend = func(ctx context.Context, msg raftpb.Message) error {
		err := opts.Transporter.Send(ctx, &RaftMessageReq{
			ReplicaID: opts.ReplicaID,
			Message:   msg,
		})
		return err
	}

	return raftOpts
}
