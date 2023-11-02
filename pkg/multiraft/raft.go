package multiraft

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Raft struct {
	node *raft.RawNode
	opts *RaftOptions
	wklog.Log
	leaderID uint64
	storage  RaftStorage

	ticker *time.Ticker

	localMsgs struct {
		sync.Mutex
		active, recycled []raftpb.Message
	}

	stopChan   chan struct{}
	propc      chan msgWithResult
	recvc      chan pb.Message
	confc      chan pb.ConfChangeV2
	confstatec chan pb.ConfState
	cancelCtx  context.Context
	cancelFunc context.CancelFunc
}

func NewRaft(opts *RaftOptions) *Raft {
	logPrefix := fmt.Sprintf("raft[%d]", opts.ID)
	if opts.logPrefix != "" {
		logPrefix = opts.logPrefix
	}
	r := &Raft{
		opts:       opts,
		Log:        wklog.NewWKLog(logPrefix),
		stopChan:   make(chan struct{}),
		propc:      make(chan msgWithResult, 256),
		recvc:      make(chan pb.Message, 256),
		confc:      make(chan pb.ConfChangeV2),
		confstatec: make(chan pb.ConfState),
	}
	r.cancelCtx, r.cancelFunc = context.WithCancel(context.Background())
	opts.Storage = opts.RaftStorage
	r.storage = opts.RaftStorage
	// storage := NewLogStorage(opts.ReplicaID, r.walStorage, r.opts.RaftStorage, opts.Peers)
	// opts.Storage = storage
	// r.storage = storage

	if opts.Heartbeat == 0 {
		r.ticker = &time.Ticker{}
	} else {
		r.ticker = time.NewTicker(opts.Heartbeat)
	}

	return r
}

func (r *Raft) Start() error {
	r.Info("raft start")

	// r.opts.Transporter.OnRaftMessage(r.onRaftMessage)

	err := r.initRaftNode()
	if err != nil {
		return err
	}
	go r.run()
	return nil
}

func (r *Raft) Stop() {
	r.Info("raft stop")
	r.cancelFunc()
	r.ticker.Stop()
	close(r.stopChan)
}

func (r *Raft) run() {
	var (
		err error
	)
	for {
		r.HandleReady()
		select {
		case <-r.ticker.C:
			r.Tick()
		case pm := <-r.propc:
			m := pm.m
			m.From = r.opts.ID
			err := r.node.Step(m)
			if pm.result != nil {
				pm.result <- err
				close(pm.result)
			}
		case m := <-r.recvc:
			err = r.node.Step(m)
			if err != nil {
				r.Warn("failed to step raft message", zap.Error(err))
			}
		case cc := <-r.confc:
			cs := r.node.ApplyConfChange(cc)
			select {
			case r.confstatec <- *cs:
			case <-r.stopChan:
			}
		case <-r.stopChan:
			return
		}
	}
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
			outboundMsgs, msgStorageAppend, msgStorageApply = splitLocalStorageMsgs(asyncRd.Messages)
		} else {
			return
		}
		if softState != nil && softState.Lead != r.leaderID {
			r.Info("raft leader changed", zap.Uint64("newLeader", softState.Lead), zap.Uint64("oldLeader", r.leaderID))
			if r.opts.LeaderChange != nil {
				r.opts.LeaderChange(softState.Lead, r.leaderID)
			}
			if r.opts.RoleChange != nil {
				r.opts.RoleChange(softState.RaftState)
			}
			r.leaderID = softState.Lead
		}
		if !raft.IsEmptyHardState(rd.HardState) {
			err = r.storage.SetHardState(rd.HardState)
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
				err := r.storage.Append(msgStorageAppend.Entries)
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
			fmt.Println("has msg storage apply")

			err := r.apply(msgStorageApply)
			if err != nil {
				r.Panic("failed to apply entries", zap.Error(err))
				return
			}
			r.sendRaftMessages(ctx, msgStorageApply.Responses)
		}
	}

}

func (r *Raft) apply(msgStorageApply raftpb.Message) error {
	if len(msgStorageApply.Entries) == 0 {
		return nil
	}
	// for _, entry := range msgStorageApply.Entries {
	// 	switch entry.Type {
	// 	case raftpb.EntryConfChange:
	// 		var cc raftpb.ConfChange
	// 		err := cc.Unmarshal(entry.Data)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		fmt.Println("apply conf change--->", cc.Type.String())
	// 		confState := r.node.ApplyConfChange(cc)
	// 		if confState != nil {
	// 			err = r.opts.RaftStorage.SetConfState(*confState)
	// 			if err != nil {
	// 				r.Warn("failed to set conf state", zap.Error(err))
	// 			}
	// 		}

	// 	}
	// }

	if r.opts.OnApply != nil {
		return r.opts.OnApply(msgStorageApply.Entries)
	}
	return nil
}

func (r *Raft) OnRaftMessage(m raftpb.Message) {
	// if len(m.Message.Entries) == 0 {
	// 	return
	// }
	err := r.Step(context.Background(), m)
	if err != nil {
		r.Error("failed to step raft message", zap.Error(err))
		return
	}

	// for _, entry := range m.Entries {
	// 	switch entry.Type {
	// 	case raftpb.EntryConfChange:
	// 		var cc raftpb.ConfChange
	// 		err := cc.Unmarshal(entry.Data)
	// 		if err != nil {
	// 			r.Error("failed to unmarshal conf change", zap.Error(err))
	// 			return
	// 		}
	// 		fmt.Println("onRaftMessage conf change--->", cc.Type.String())

	// 		switch cc.Type {
	// 		case raftpb.ConfChangeAddNode:
	// 			r.ApplyConfChange(cc)
	// 		}
	// 	}
	// }

}

func (r *Raft) Campaign(ctx context.Context) error {
	return r.Step(ctx, pb.Message{Type: pb.MsgHup})
}

// Bootstrap is used to bootstrap a new cluster.
func (r *Raft) Bootstrap(peers []Peer) error {
	raftPeers := make([]raft.Peer, 0, len(peers))
	if len(peers) > 0 {
		for _, peer := range peers {
			raftPeers = append(raftPeers, raft.Peer{
				ID: peer.ID,
				Context: []byte(wkutil.ToJSON(map[string]interface{}{
					"addr": peer.Addr,
				})),
			})
		}
	}
	return r.node.Bootstrap(raftPeers)
}

func (r *Raft) ForgetLeader() error {
	return r.node.ForgetLeader()
}

func (r *Raft) Step(ctx context.Context, m raftpb.Message) error {
	return r.stepWithWaitOption(ctx, m, false)
}

func (r *Raft) stepWithWaitOption(ctx context.Context, m pb.Message, wait bool) error {
	if m.Type != pb.MsgProp {
		select {
		case r.recvc <- m:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-r.stopChan:
			return raft.ErrStopped
		}
	}
	ch := r.propc
	pm := msgWithResult{m: m}
	if wait {
		pm.result = make(chan error, 1)
	}
	select {
	case ch <- pm:
		if !wait {
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopChan:
		return raft.ErrStopped
	}
	select {
	case err := <-pm.result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopChan:
		return raft.ErrStopped
	}
	return nil
}

func (r *Raft) Tick() {
	r.node.Tick()
}

func (r *Raft) Propose(ctx context.Context, data []byte) error {
	return r.stepWithWaitOption(ctx, pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Data: data}}}, true)
}

func (r *Raft) ProposeConfChange(ctx context.Context, cc raftpb.ConfChange) error {
	msg, err := confChangeToMsg(cc)
	if err != nil {
		return err
	}
	return r.Step(ctx, msg)
	// err := r.opts.Transporter.AddPeer(peer)
	// if err != nil {
	// 	return err
	// }
	// return r.node.ProposeConfChange(raftpb.ConfChange{
	// 	Type:   raftpb.ConfChangeAddNode,
	// 	NodeID: peer.ID,
	// 	Context: []byte(wkutil.ToJSON(map[string]interface{}{
	// 		"addr": peer.Addr,
	// 	})),
	// })
}
func (r *Raft) ApplyConfChange(cc pb.ConfChangeI) *pb.ConfState {
	var cs pb.ConfState
	select {
	case r.confc <- cc.AsV2():
	case <-r.stopChan:
	}
	select {
	case cs = <-r.confstatec:
	case <-r.stopChan:
	}
	return &cs
}

func (r *Raft) GetLeaderID() uint64 {
	return r.leaderID
}

func (r *Raft) GetOptions() *RaftOptions {
	return r.opts
}
func confChangeToMsg(c pb.ConfChangeI) (pb.Message, error) {
	typ, data, err := pb.MarshalConfChange(c)
	if err != nil {
		return pb.Message{}, err
	}
	return pb.Message{Type: pb.MsgProp, Entries: []pb.Entry{{Type: typ, Data: data}}}, nil
}

func (r *Raft) initRaftNode() error {

	// applied, err := r.storage.Applied()
	// if err != nil {
	// 	return err
	// }
	// hardState, err := r.storage.HardState()
	// if err != nil {
	// 	r.Panic("failed to get hard state", zap.Error(err))
	// }

	// if applied > hardState.Commit {
	// 	applied = hardState.Commit
	// }

	// r.opts.Applied = applied

	var err error
	r.node, err = raft.NewRawNode(r.opts.Config)
	if err != nil {
		return err
	}

	raftPeers := make([]raft.Peer, 0, len(r.opts.Peers))
	if len(r.opts.Peers) > 0 {
		for _, peer := range r.opts.Peers {
			raftPeers = append(raftPeers, raft.Peer{
				ID: peer.ID,
				Context: []byte(wkutil.ToJSON(map[string]interface{}{
					"addr": peer.Addr,
				})),
			})
		}
	}
	if len(raftPeers) > 0 && !r.opts.Restart {
		err = r.node.Bootstrap(raftPeers)
		if err != nil {
			return err
		}
	}
	return nil
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
		case r.opts.ID:
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

func (r *Raft) sendLocalRaftMsg(msg raftpb.Message) {
	if msg.To != r.opts.ID {
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
		// fmt.Println("step----->", m.Type.String())
		if err := r.node.Step(m); err != nil {
			r.Fatal("unexpected error stepping local raft message", zap.Error(err))
		}
		// NB: we can reset messages in the localMsgs.recycled slice without holding
		// the localMsgs mutex because no-one ever writes to localMsgs.recycled and
		// we are holding raftMu, which must be held to switch localMsgs.active and
		// localMsgs.recycled.
		localMsgs[i].Reset() // for GC
	}

}

func (r *Raft) sendRaftMessage(ctx context.Context, msg raftpb.Message) {
	// r.Info("send raft message", zap.String("type", msg.Type.String()), zap.Uint64("to", msg.To), zap.Uint64("index", msg.Index), zap.Int("entities", len(msg.Entries)))

	if r.opts.OnSend != nil {
		err := r.opts.OnSend(ctx, msg)
		if err != nil {
			r.Error("failed to send raft message", zap.Error(err))
			return
		}
	}

}

func verboseRaftLoggingEnabled() bool {
	return true
}

func logRaftReady(ctx context.Context, ready raft.Ready) {
	if !verboseRaftLoggingEnabled() {
		return
	}

	var buf bytes.Buffer
	if ready.SoftState != nil {
		fmt.Fprintf(&buf, "  SoftState updated: %+v\n", *ready.SoftState)
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		fmt.Fprintf(&buf, "  HardState updated: %+v\n", ready.HardState)
	}
	for i, e := range ready.Entries {
		fmt.Fprintf(&buf, "  New Entry[%d]: %.200s\n",
			i, raft.DescribeEntry(e, raftEntryFormatter))
	}
	for i, e := range ready.CommittedEntries {
		fmt.Fprintf(&buf, "  Committed Entry[%d]: %.200s\n",
			i, raft.DescribeEntry(e, raftEntryFormatter))
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
		snap := ready.Snapshot
		snap.Data = nil
		fmt.Fprintf(&buf, "  Snapshot updated: %v\n", snap)
	}
	for i, m := range ready.Messages {
		fmt.Fprintf(&buf, "  Outgoing Message[%d]: %.2000s\n",
			i, raftDescribeMessage(m, raftEntryFormatter))
	}
	fmt.Printf("raft ready  (must-sync=%t)\n%s", ready.MustSync, buf.String())
}

// This is a fork of raft.DescribeMessage with a tweak to avoid logging
// snapshot data.
func raftDescribeMessage(m raftpb.Message, f raft.EntryFormatter) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%x->%x %v Term:%d Log:%d/%d", m.From, m.To, m.Type, m.Term, m.LogTerm, m.Index)
	if m.Reject {
		fmt.Fprintf(&buf, " Rejected (Hint: %d)", m.RejectHint)
	}
	if m.Commit != 0 {
		fmt.Fprintf(&buf, " Commit:%d", m.Commit)
	}
	if len(m.Entries) > 0 {
		fmt.Fprintf(&buf, " Entries:[")
		for i, e := range m.Entries {
			if i != 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(raft.DescribeEntry(e, f))
		}
		fmt.Fprintf(&buf, "]")
	}
	if len(m.Responses) > 0 {
		fmt.Fprintf(&buf, " Responses:[")
		for i, r := range m.Responses {
			if i != 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%v", raftDescribeMessage(r, f))
		}
		fmt.Fprintf(&buf, "]")
	}
	if m.Snapshot != nil {
		snap := *m.Snapshot
		snap.Data = nil
		fmt.Fprintf(&buf, " Snapshot:%v", snap)
	}
	return buf.String()
}

func raftEntryFormatter(data []byte) string {

	return string(data)
}

// asyncReady 仅读取
type asyncReady struct {
	*raft.SoftState
	ReadStates []raft.ReadState
	Messages   []raftpb.Message
}

// makeAsyncReady constructs an asyncReady from the provided Ready.
func makeAsyncReady(rd raft.Ready) asyncReady {
	return asyncReady{
		SoftState:  rd.SoftState,
		ReadStates: rd.ReadStates,
		Messages:   rd.Messages,
	}
}

func hasMsg(m raftpb.Message) bool { return m.Type != 0 }

func splitLocalStorageMsgs(msgs []raftpb.Message) (otherMsgs []raftpb.Message, msgStorageAppend, msgStorageApply raftpb.Message) {
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

type msgWithResult struct {
	m      pb.Message
	result chan error
}
