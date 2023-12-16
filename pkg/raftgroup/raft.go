package raftgroup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.etcd.io/etcd/pkg/v3/contention"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Raft struct {
	node *raft.RawNode
	opts *RaftOptions
	wklog.Log
	leaderID         atomic.Uint64
	leaderChangeChan chan struct{}

	cancelCtx  context.Context
	cancelFunc context.CancelFunc

	// contention detectors for raft heartbeat message
	td *contention.TimeoutDetector

	localMsgs struct {
		sync.Mutex
		active, recycled []raftpb.Message
	}

	heartbeatTick int

	preHeartbeatRespTime time.Time
}

func NewRaft(opts *RaftOptions) *Raft {

	lg := wklog.NewWKLog(fmt.Sprintf("Raft[%d][%d]", opts.NodeID, opts.ShardID))

	heartbeatTick := 1

	node, err := raft.NewRawNode(&raft.Config{
		ID:                       opts.NodeID,
		AsyncStorageWrites:       true,
		ElectionTick:             opts.ElectionTick,
		HeartbeatTick:            opts.HeartbeatTick,
		PreVote:                  true,
		CheckQuorum:              false,
		MaxInflightMsgs:          4096 / 8,
		MaxSizePerMsg:            1 * 1024 * 1024,
		MaxCommittedSizePerReady: 2048,
		Storage:                  opts.Storage,
		Applied:                  opts.Applied,
		Logger:                   newRaftLogger(lg),
	})
	if err != nil {
		lg.Panic(err.Error())
	}

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	return &Raft{
		opts:             opts,
		Log:              lg,
		node:             node,
		cancelCtx:        cancelCtx,
		cancelFunc:       cancelFunc,
		leaderChangeChan: make(chan struct{}, 1),
		td:               contention.NewTimeoutDetector(2 * time.Duration(heartbeatTick) * opts.Heartbeat),
		heartbeatTick:    heartbeatTick,
	}
}

func (r *Raft) Tick() {
	preTickState := r.node.BasicStatus().RaftState
	r.node.Tick()
	postTickState := r.node.BasicStatus().RaftState
	if preTickState != postTickState {
		if postTickState == raft.StatePreCandidate {
			if r.opts.OnRaftTimeoutCampaign != nil {
				r.opts.OnRaftTimeoutCampaign(r)
			}
		}
	}
}

func (r *Raft) Step(msg raftpb.Message) error {

	if msg.Type == raftpb.MsgHeartbeatResp {
		if r.preHeartbeatRespTime.IsZero() {
			r.preHeartbeatRespTime = time.Now()
		}
		exceed := time.Since(r.preHeartbeatRespTime)
		if exceed > time.Duration(r.opts.ElectionTick)*r.opts.Heartbeat {
			// TODO: limit request rate.
			r.Warn(
				"recv heartbeat to slow",
				zap.Uint32("sharedID", r.opts.ShardID),
				zap.String("to", fmt.Sprintf("%x", msg.To)),
				zap.Duration("heartbeat-interval", r.opts.Heartbeat),
				zap.Duration("exceeded-duration", exceed),
			)

		}
		r.preHeartbeatRespTime = time.Now()
	}
	err := r.node.Step(msg)
	if err != nil {
		return err
	}
	r.setReady()
	return nil
}

func (r *Raft) Ready() raft.Ready {
	return r.node.Ready()
}

func (r *Raft) HasReady() bool {
	return r.node.HasReady()
}

func (r *Raft) HandleReady() {
	var (
		rd               raft.Ready
		ctx              = r.cancelCtx
		outboundMsgs     []raftpb.Message
		msgStorageAppend raftpb.Message
		msgStorageApply  raftpb.Message
		softState        *raft.SoftState
		hasReady         bool
		err              error
	)
	for {
		r.deliverLocalRaftMsgsRaft()
		if hasReady = r.node.HasReady(); hasReady {
			rd = r.node.Ready()
			// log print
			// logRaftReady(ctx, rd)
			asyncRd := makeAsyncReady(rd)
			softState = asyncRd.SoftState
			outboundMsgs, msgStorageAppend, msgStorageApply = SplitLocalStorageMsgs(asyncRd.Messages)
		} else {
			return
		}
		if softState != nil {
			r.td.Reset()
		}
		if softState != nil && softState.Lead != r.leaderID.Load() {
			r.Info("raft leader changed", zap.Uint64("newLeader", softState.Lead), zap.Uint64("oldLeader", r.leaderID.Load()))
			if r.opts.LeaderChange != nil {
				r.opts.LeaderChange(softState.Lead, r.leaderID.Load())
			}
			if softState.Lead != r.leaderID.Load() {
				select {
				case r.leaderChangeChan <- struct{}{}:
				default:
				}
			}
			r.leaderID.Store(softState.Lead)
		}
		if !raft.IsEmptyHardState(rd.HardState) {
			err = r.opts.Storage.SetHardState(rd.HardState)
			if err != nil {
				r.Warn("failed to set hard state", zap.Error(err))
			}

		}
		r.sendRaftMessages(ctx, outboundMsgs)

		// ----------------- handle storage append -----------------
		if hasMsg(msgStorageAppend) {
			if msgStorageAppend.Snapshot != nil {
				r.Panic("unexpected MsgStorageAppend with snapshot")
				return
			}
			if len(msgStorageAppend.Entries) > 0 {
				err := r.opts.Storage.Append(msgStorageAppend.Entries)
				if err != nil {
					r.Panic("failed to append entries", zap.Error(err))
					return
				}

			}
			if len(msgStorageAppend.Responses) > 0 {
				r.sendRaftMessages(ctx, msgStorageAppend.Responses)
			}

		}
		// ----------------- handle storage apply -----------------
		if hasMsg(msgStorageApply) {
			err := r.apply(msgStorageApply)
			if err != nil {
				r.Panic("failed to apply entries", zap.Error(err))
				return
			}
			r.sendRaftMessages(ctx, msgStorageApply.Responses)
		}
	}
}

func (r *Raft) Campaign() error {
	err := r.node.Campaign()
	if err != nil {
		return err
	}
	r.setReady()
	return nil
}

func (r *Raft) Propose(data []byte) error {
	err := r.node.Propose(data)
	if err != nil {
		return err
	}
	r.setReady()
	return nil
}

func (r *Raft) ProposeConfChange(cc raftpb.ConfChange) error {
	err := r.node.ProposeConfChange(cc)
	if err != nil {
		return err
	}
	r.setReady()
	return nil
}

func (r *Raft) ApplyConfChange(cc raftpb.ConfChange) *raftpb.ConfState {
	confState := r.node.ApplyConfChange(cc)
	r.setReady()
	return confState
}

func (r *Raft) Bootstrap() error {
	peers := make([]raft.Peer, len(r.opts.Members))
	hasSelf := false
	for idx, member := range r.opts.Members {
		peers[idx] = raft.Peer{
			ID:      member.NodeID,
			Context: setConfContextByAddr(member.ServerAddr),
		}
		if member.NodeID == r.opts.NodeID {
			hasSelf = true
		}
	}
	if len(peers) != 0 && !hasSelf {
		return fmt.Errorf("self node not found in members")
	}
	lastIndex, err := r.opts.Storage.LastIndex()
	if err != nil {
		return err
	}
	if lastIndex == 0 {
		err = r.node.Bootstrap(peers)
		if err != nil {
			return err
		}
	}
	// if len(peers) > 0 {
	// 	r.HandleReady()
	// 	err = r.node.Campaign()
	// 	if err != nil {
	// 		return err
	// 	}
	// }
	r.setReady()
	return nil
}

func (r *Raft) LeaderID() uint64 {
	return r.leaderID.Load()
}

func (r *Raft) WaitLeaderChange(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-r.leaderChangeChan:
		return nil
	case <-timeoutCtx.Done():
		return fmt.Errorf("wait leader change timeout")
	}
}

func (r *Raft) setReady() {
	if r.opts.onSetReady != nil {
		r.opts.onSetReady(r)
	}
}

func getAddrByConfContext(cc []byte) (string, error) {
	resultMap, err := wkutil.JSONToMap(string(cc))
	if err != nil {
		return "", err
	}
	if resultMap["url"] == nil {
		return "", fmt.Errorf("url not found")
	}
	addr := resultMap["url"].(string)
	return addr, nil
}

func setConfContextByAddr(addr string) []byte {
	addrStr := wkutil.ToJSON(map[string]string{
		"url": addr,
	})
	return []byte(addrStr)
}

func SplitLocalStorageMsgs(msgs []raftpb.Message) (otherMsgs []raftpb.Message, msgStorageAppend, msgStorageApply raftpb.Message) {
	for i := len(msgs) - 1; i >= 0; i-- {
		switch msgs[i].Type {
		case raftpb.MsgStorageAppend:
			if hasMsg(msgStorageAppend) {
				panic("two MsgStorageAppend")
			}
			msgStorageAppend = msgs[i]
		case raftpb.MsgStorageApply:
			if hasMsg(msgStorageApply) {
				panic("two MsgStorageApply")
			}
			msgStorageApply = msgs[i]
		default:
			return msgs[:i+1], msgStorageAppend, msgStorageApply
		}
	}
	// Only local storage messages.
	return nil, msgStorageAppend, msgStorageApply
}

func (r *Raft) sendRaftMessages(ctx context.Context, messages []raftpb.Message) {
	var lastAppResp raftpb.Message
	for _, message := range messages {
		// r.Info("loop send raft message", zap.Uint64("to", message.To), zap.String("type", message.Type.String()))
		switch message.To {
		case raft.LocalAppendThread:
			// To local append thread.
			// NOTE: we don't currently split append work off into an async goroutine.
			// Instead, we handle messages to LocalAppendThread inline on the raft
			// scheduler goroutine, so this code path is unused.
			r.Panic("unsupported, currently processed inline on raft scheduler goroutine")
		case raft.LocalApplyThread:
			// To local apply thread.
			// NOTE: we don't currently split apply work off into an async goroutine.
			// Instead, we handle messages to LocalAppendThread inline on the raft
			// scheduler goroutine, so this code path is unused.
			r.Panic("unsupported, currently processed inline on raft scheduler goroutine")
		case r.opts.NodeID:
			// To local raft instance.
			r.sendLocalRaftMsg(message)
		default:
			// To remote raft instance.
			drop := false
			switch message.Type {
			case raftpb.MsgAppResp:
				if !message.Reject && message.Index > lastAppResp.Index {
					lastAppResp = message
					drop = true
				}
			}
			if !drop {
				r.sendRaftMessage(ctx, message)
			}
		}
	}
	if lastAppResp.Index > 0 {
		r.sendRaftMessage(ctx, lastAppResp)
	}
}

func (r *Raft) sendRaftMessage(ctx context.Context, msg raftpb.Message) {
	if msg.Type == raftpb.MsgHeartbeat {
		ok, exceed := r.td.Observe(msg.To)
		if !ok {
			// TODO: limit request rate.
			r.Warn(
				"leader failed to send out heartbeat on time; took too long, leader is overloaded likely from slow disk",
				zap.Uint32("sharedID", r.opts.ShardID),
				zap.String("to", fmt.Sprintf("%x", msg.To)),
				zap.Duration("heartbeat-interval", r.opts.Heartbeat),
				zap.Duration("expected-duration", 2*time.Duration(r.heartbeatTick)*r.opts.Heartbeat),
				zap.Duration("exceeded-duration", exceed),
			)
		}
	}
	if r.opts.OnSend != nil {
		err := r.opts.OnSend(ctx, r, msg)
		if err != nil {
			r.Error("failed to send raft message", zap.Error(err))
			return
		}
	}

}

func (r *Raft) sendLocalRaftMsg(msg raftpb.Message) {
	if msg.To != r.opts.NodeID {
		panic("incorrect message target")
	}
	r.localMsgs.Lock()
	r.localMsgs.active = append(r.localMsgs.active, msg)
	r.localMsgs.Unlock()
}

func (r *Raft) deliverLocalRaftMsgsRaft() {
	r.localMsgs.Lock()
	localMsgs := r.localMsgs.active
	r.localMsgs.active, r.localMsgs.recycled = r.localMsgs.recycled, r.localMsgs.active[:0]
	// Don't recycle large slices.
	if cap(r.localMsgs.recycled) > 16 {
		r.localMsgs.recycled = nil
	}
	r.localMsgs.Unlock()

	for i, m := range localMsgs {
		if err := r.node.Step(m); err != nil {
			r.Fatal("unexpected error stepping local raft message", zap.Error(err))
		}
		// NB: we can reset messages in the localMsgs.recycled slice without holding
		// the localMsgs mutex because no-one ever writes to localMsgs.recycled and
		// we are holding raftMu, which must be held to switch localMsgs.active and
		// localMsgs.recycled.
		localMsgs[i].Reset() // for GC
	}
	if len(localMsgs) > 0 {
		r.setReady()
	}

}

func (r *Raft) apply(msgStorageApply raftpb.Message) error {
	if len(msgStorageApply.Entries) == 0 {
		return nil
	}

	if r.opts.OnApply != nil {
		return r.opts.OnApply(r, msgStorageApply.Entries)
	}
	return nil
}

func hasMsg(m raftpb.Message) bool { return m.Type != 0 }

func makeAsyncReady(rd raft.Ready) asyncReady {
	return asyncReady{
		SoftState:  rd.SoftState,
		ReadStates: rd.ReadStates,
		Messages:   rd.Messages,
	}
}

// asyncReady 仅读取
type asyncReady struct {
	*raft.SoftState
	ReadStates []raft.ReadState
	Messages   []raftpb.Message
}
