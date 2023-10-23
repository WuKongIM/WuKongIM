package multiraft

import (
	"context"
	"fmt"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
	r.raftNode.opts.Applied = applied
	err = r.raftNode.Start()
	if err != nil {
		return err
	}

	restart, err := r.isRestart()
	if err != nil {
		return err
	}
	fmt.Println("restart---->", restart)
	if !restart && len(r.opts.Peers) > 0 {
		var peers = make([]raft.Peer, 0, len(r.opts.Peers))
		for _, peer := range r.opts.Peers {
			peers = append(peers, raft.Peer{
				ID: peer.ID,
				Context: []byte(wkutil.ToJSON(map[string]interface{}{
					"addr": peer.Addr,
				})),
			})
		}
		err = r.raftNode.Bootstrap(peers)
		if err != nil {
			return err
		}

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
	raftOpts := NewRaftOptions()
	raftOpts.ID = opts.PeerID
	raftOpts.RaftStorage = r
	raftOpts.LeaderChange = opts.LeaderChange
	raftOpts.OnApply = func(entries []raftpb.Entry) error {
		err := opts.ReplicaRaftStorage.SetApplied(opts.ReplicaID, entries[len(entries)-1].Index)
		if err != nil {
			r.Warn("set applied error", zap.Error(err))
		}
		return opts.StateMachine.Apply(opts.ReplicaID, entries)
	}
	raftOpts.OnSend = func(msg raftpb.Message) error {
		err := opts.Transporter.Send(context.Background(), &RaftMessageReq{
			ReplicaID: opts.ReplicaID,
			Message:   msg,
		})
		return err
	}

	return raftOpts
}
