package multiraft

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Raft struct {
	node *raft.RawNode
	opts *RaftOptions
	wklog.Log
	leaderID   uint64
	storage    *LogStorage
	walStorage *WALStorage

	localMsgs struct {
		sync.Mutex
		active, recycled []raftpb.Message
	}
}

func NewRaft(opts *RaftOptions) *Raft {

	r := &Raft{
		opts: opts,
		Log:  wklog.NewWKLog(fmt.Sprintf("raft[%d]", opts.ID)),
	}
	walPath := path.Join(opts.DataDir, "wal")
	r.walStorage = NewWALStorage(walPath)
	storage := NewLogStorage(opts.ID, r.walStorage, r.opts.SateStorage)
	opts.Storage = storage
	r.storage = storage

	return r
}

func (r *Raft) Start() error {
	r.Info("raft start")
	err := r.walStorage.Open()
	if err != nil {
		return err
	}

	err = r.initRaftNode()
	if err != nil {
		return err
	}
	return nil
}

func (r *Raft) Stop() {
	r.Info("raft stop")
	r.walStorage.Close()
}

func (r *Raft) HandleReady() {

	var (
		rd               raft.Ready
		ctx              = context.Background()
		outboundMsgs     []raftpb.Message
		msgStorageAppend raftpb.Message
		msgStorageApply  raftpb.Message
		softState        *raft.SoftState
		hasReady         bool
	)
	for {
		r.deliverLocalRaftMsgsRaft()
		if hasReady = r.node.HasReady(); hasReady {
			rd = r.node.Ready()
			// log print
			logRaftReady(ctx, rd)
			asyncRd := makeAsyncReady(rd)
			softState = asyncRd.SoftState
			outboundMsgs, msgStorageAppend, msgStorageApply = splitLocalStorageMsgs(asyncRd.Messages)
		} else {
			return
		}
		if softState != nil && softState.Lead != r.leaderID {
			r.Debug("raft leader changed", zap.Uint64("newLeader", softState.Lead), zap.Uint64("oldLeader", r.leaderID))
			r.leaderID = softState.Lead
		}
		r.sendRaftMessages(ctx, outboundMsgs)

		stateMatchine := r.opts.StateMachine

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
			err := stateMatchine.Apply(msgStorageApply.Entries)
			if err != nil {
				r.Panic("failed to apply entries", zap.Error(err))
				return
			}
			r.sendRaftMessages(ctx, msgStorageApply.Responses)
		}
	}

}

func (r *Raft) Campaign() error {

	return r.node.Campaign()
}

func (r *Raft) Step(m raftpb.Message) error {

	return r.node.Step(m)
}

func (r *Raft) initRaftNode() error {

	r.storage.SetConfState(raftpb.ConfState{
		Voters: []uint64{1},
	})
	r.storage.SetHardState(raftpb.HardState{
		Term:   1,
		Vote:   1,
		Commit: 0,
	})

	applied, err := r.storage.Applied()
	if err != nil {
		return err
	}
	hardState, err := r.storage.HardState()
	if err != nil {
		r.Panic("failed to get hard state", zap.Error(err))
	}

	if applied > hardState.Commit {
		applied = hardState.Commit
	}

	r.opts.Applied = applied

	r.node, err = raft.NewRawNode(r.opts.Config)
	if err != nil {
		return err
	}
	return nil
}

func (r *Raft) sendRaftMessages(ctx context.Context, messages []raftpb.Message) {
	var lastAppResp raftpb.Message
	for _, message := range messages {
		fmt.Println("message.type--->", message.Type.String())
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
				fmt.Println("send---remote--->", message.Type.String())
				r.sendRaftMessage(ctx, message)
			}
		}
	}
	if lastAppResp.Index > 0 {
		r.sendRaftMessage(ctx, lastAppResp)
	}
}

func (r *Raft) sendLocalRaftMsg(msg raftpb.Message) {
	fmt.Println("1111sendLocalRaftMsg--->", msg.Type.String())
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
		fmt.Println("step----->", m.Type.String())
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
	r.opts.Transporter.Send(ctx, msg)
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
		fmt.Fprintf(&buf, "  Outgoing Message[%d]: %.200s\n",
			i, raftDescribeMessage(m, raftEntryFormatter))
	}
	fmt.Printf("raft ready (must-sync=%t)\n%s", ready.MustSync, buf.String())
}

// This is a fork of raft.DescribeMessage with a tweak to avoid logging
// snapshot data.
func raftDescribeMessage(m raftpb.Message, f raft.EntryFormatter) string {
	var buf bytes.Buffer
	fmt.Println("m.To--->", m.From, m.To)
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
