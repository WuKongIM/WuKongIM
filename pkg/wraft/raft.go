// Copyright (c) 2022 Shanghai Xinbida Network Technology Co., Ltd. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package wraft

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/schedule"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type State struct {
	LeaderChanges atomic.Int64
}

type RaftNode struct {
	node   raft.Node
	ticker *time.Ticker
	tickMu *sync.Mutex

	wklog.Log
	stopped chan struct{}
	done    chan struct{}
	cfg     *RaftNodeConfig

	td *TimeoutDetector

	readStateC chan raft.ReadState

	// a chan to send/receive snapshot
	msgSnapC chan raftpb.Message

	// a chan to send out apply
	applyc chan ToApply
	// applyCancelContext context.Context
	// applyCancelFnc     context.CancelFunc
	monitor     Monitor
	raftStorage *WALStorage

	// FSM is the fsm to apply raft logs.
	fms FSM

	sched schedule.Scheduler

	// removedClusterIDMap contains the ids of removed members in the cluster.
	removedClusterIDMap     map[types.ID]bool
	removedClusterIDMapLock sync.RWMutex

	leadID atomic.Uint64

	grpcTransporter *GRPCTransporter

	OnLead func(lead uint64)

	reqIDGen *idutil.Generator

	w wait.Wait

	recvChan chan []byte

	applied uint64

	peers     Peers
	peersLock sync.RWMutex

	runing atomic.Bool
}

func NewRaftNode(fms FSM, cfg *RaftNodeConfig) *RaftNode {

	r := &RaftNode{
		Log:                 wklog.NewWKLog(fmt.Sprintf("raftNode[%s]", cfg.ID.String())),
		tickMu:              new(sync.Mutex),
		stopped:             make(chan struct{}, 10),
		done:                make(chan struct{}),
		cfg:                 cfg,
		td:                  NewTimeoutDetector(2 * cfg.Heartbeat),
		readStateC:          make(chan raft.ReadState, 1),
		applyc:              make(chan ToApply),
		raftStorage:         NewWALStorage(cfg.LogWALPath, cfg.MetaDBPath),
		removedClusterIDMap: make(map[types.ID]bool),
		msgSnapC:            make(chan raftpb.Message, cfg.MaxInFlightMsgSnap),
		fms:                 fms,
		sched:               schedule.NewFIFOScheduler(wklog.NewWKLog("Scheduler")),
		reqIDGen:            idutil.NewGenerator(uint16(cfg.ID), time.Now()),
		w:                   wait.New(),
		recvChan:            make(chan []byte),
	}
	// r.applyCancelContext, r.applyCancelFnc = context.WithCancel(context.Background())
	r.grpcTransporter = NewGRPCTransporter(r.recvChan, cfg)
	if cfg.Transport == nil {
		cfg.Transport = r.grpcTransporter
	}
	if cfg.Heartbeat == 0 {
		r.ticker = &time.Ticker{}
	} else {
		r.ticker = time.NewTicker(cfg.Heartbeat)
	}
	r.monitor = cfg.Monitor()
	return r
}

func (r *RaftNode) Tick() {
	r.tickMu.Lock()
	r.node.Tick()
	r.tickMu.Unlock()
}

func (r *RaftNode) Start() error {

	restartNode := r.raftStorage.Exist()

	err := r.raftStorage.Open()
	if err != nil {
		r.Panic("failed to open raft storage", zap.Error(err))
	}

	go r.loopRecv()
	err = r.grpcTransporter.Start()
	if err != nil {
		return err
	}

	cfg := r.cfg
	applied, err := r.raftStorage.Applied()
	if err != nil {
		return err
	}
	hardState, err := r.raftStorage.HardState()
	if err != nil {
		return err
	}

	if applied > hardState.Commit {
		applied = hardState.Commit
	}

	r.applied = applied

	r.Debug("applied", zap.Uint64("applied", applied))

	raftCfg := &raft.Config{
		ID:              uint64(r.cfg.ID),
		ElectionTick:    cfg.ElectionTicks,
		HeartbeatTick:   1,
		Storage:         r.raftStorage,
		MaxSizePerMsg:   cfg.MaxSizePerMsg,
		MaxInflightMsgs: cfg.MaxInflightMsgs,
		CheckQuorum:     true,
		PreVote:         cfg.PreVote,
		Applied:         r.applied,
	}

	peers, err := r.raftStorage.Peers()
	if err != nil {
		return err
	}
	for _, peer := range peers {
		if peer.ID != r.cfg.ID {
			err := r.grpcTransporter.AddPeer(peer.ID, peer.Addr)
			if err != nil {
				return err
			}
		}
	}

	if restartNode {
		r.node = r.restartClusterNode(raftCfg)
	} else if r.cfg.Peers != nil && len(r.cfg.Peers) == 0 {
		r.node = r.startSingleNode(raftCfg)
	} else {
		r.node = r.startNewClusterNode(raftCfg)
	}

	go r.handleRaft()
	go r.run()

	return nil
}

func (r *RaftNode) Addr() net.Addr {
	return r.grpcTransporter.transporterServer.Addr()
}

func (r *RaftNode) onStop() {
	r.Debug("onStop---->")
	r.node.Stop()

	err := r.grpcTransporter.Stop()
	if err != nil {
		r.Warn("failed to stop grpc transporter", zap.Error(err))
	}
	err = r.raftStorage.Close()
	if err != nil {
		r.Warn("failed to close raft storage", zap.Error(err))
	}
	r.Debug("onStop---->1")
	r.ticker.Stop()
	r.Debug("onStop---->2")
	close(r.done)
	r.sched.Stop()
	r.Debug("onStop---->3")

}

func (r *RaftNode) Stop() {
	r.Debug("Stop---->")
	select {
	case r.stopped <- struct{}{}:
		// Not already stopped, so trigger it
	case <-r.done:
		// Has already been stopped - no need to do anything
		return
	}
	r.Debug("Stop---->1")
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.stopped <- struct{}{}
	r.Debug("Stop---->2")
	// r.applyCancelFnc()
	// Block until the stop has been acknowledged by start()
	if r.runing.Load() {
		<-r.done
	}
	r.runing.Store(false)

	r.Debug("Stop---->3")

}

func (r *RaftNode) startSingleNode(cfg *raft.Config) raft.Node {
	r.Info("start single node")
	return raft.StartNode(cfg, []raft.Peer{{ID: cfg.ID}})
}

func (r *RaftNode) startNewClusterNode(cfg *raft.Config) raft.Node {

	r.Info("start new cluster node", zap.Any("peers", r.cfg.Peers))

	peers := make([]raft.Peer, len(r.cfg.Peers))
	for i, peer := range r.cfg.Peers {

		peers[i] = raft.Peer{ID: uint64(peer.ID), Context: []byte(wkutil.ToJSON(map[string]interface{}{
			"addr": peer.Addr,
		}))}
		if peer.ID != r.cfg.ID {
			err := r.grpcTransporter.AddPeer(peer.ID, peer.Addr)
			if err != nil {
				r.Panic("failed to add peer", zap.Error(err))
			}
		}

	}
	if r.cfg.Peers == nil {
		return raft.RestartNode(cfg)
	}
	return raft.StartNode(cfg, peers)
}

func (r *RaftNode) restartClusterNode(cfg *raft.Config) raft.Node {
	r.Info("restart cluster node")
	return raft.RestartNode(cfg)
}

func (r *RaftNode) run() {

	r.runing.Store(true)

	defer r.onStop()
	internalTimeout := time.Second
	islead := false
	var err error
	for {
		select {
		case <-r.ticker.C:
			r.Tick()
		case rd := <-r.node.Ready():
			r.Debug("================================ready=====================================")
			if rd.SoftState != nil {
				r.Debug("SoftState", zap.Uint64("Lead", rd.SoftState.Lead), zap.String("RaftState", rd.SoftState.RaftState.String()))
			}
			r.Debug("HardState", zap.String("hardState", rd.HardState.String()))
			for _, entry := range rd.Entries {
				r.Debug("entry", zap.String("entry", entry.String()))
			}
			for _, entry := range rd.CommittedEntries {
				r.Debug("committedEntry", zap.String("committedEntry", entry.String()))
			}
			for _, message := range rd.Messages {
				r.Debug("message", zap.String("message", message.String()))
			}
			r.Debug("=====================================================================")
			if rd.SoftState != nil {
				newLeader := rd.SoftState.Lead != raft.None && r.leadID.Load() != rd.SoftState.Lead
				if newLeader {
					r.monitor.LeaderChangesInc()
					if r.OnLead != nil {
						r.OnLead(rd.SoftState.Lead)
					}
				}
				r.leadID.Store(rd.SoftState.Lead)

				islead = rd.RaftState == raft.StateLeader

				// rh.UpdateLeadership(newLeader)

				r.td.Reset()

			}
			if len(rd.ReadStates) != 0 {
				select {
				case r.readStateC <- rd.ReadStates[len(rd.ReadStates)-1]:
				case <-time.After(internalTimeout):
					r.Warn("timed out sending read state", zap.Duration("timeout", internalTimeout))
				case <-r.stopped:
					return
				}
			}
			notifyc := make(chan struct{}, 1)
			raftAdvancedC := make(chan struct{}, 1)
			ap := ToApply{
				Entries:       rd.CommittedEntries,
				Snapshot:      rd.Snapshot,
				Notifyc:       notifyc,
				RaftAdvancedC: raftAdvancedC,
			}
			// r.updateCommittedIndex(&ap)
			r.Debug("ready------------1")
			select {
			case r.applyc <- ap:
			case <-r.stopped:
				r.Debug("cancel....")
				return
			}
			if islead {
				if len(rd.Messages) > 0 {
					r.cfg.Transport.Send(r.processMessages(rd.Messages))
				}
			}
			r.Debug("ready------------2")
			// Must save the snapshot file and WAL snapshot entry before saving any other entries or hardstate to
			// ensure that recovery after a snapshot restore is possible.
			if !raft.IsEmptySnap(rd.Snapshot) {
				if err = r.cfg.Storage.SaveSnap(rd.Snapshot); err != nil {
					r.Fatal("failed to save Raft snapshot", zap.Error(err))
				}
			}
			if !raft.IsEmptyHardState(rd.HardState) {
				err = r.raftStorage.SetHardState(rd.HardState)
				if err != nil {
					r.Fatal("failed to set Raft hard state", zap.Error(err))
				}
			}
			if len(rd.Entries) > 0 {
				err = r.raftStorage.Append(r.removeRepeatEntries(rd.Entries))
				if err != nil {
					r.Fatal("failed to append Raft entries", zap.Error(err))
				}
			}
			if !raft.IsEmptyHardState(rd.HardState) {
				r.monitor.SetProposalsCommitted(rd.HardState.Commit)
			}

			if !raft.IsEmptySnap(rd.Snapshot) {
				if err := r.cfg.Storage.Sync(); err != nil {
					r.Fatal("failed to sync Raft snapshot", zap.Error(err))
				}
				notifyc <- struct{}{}
				err := r.raftStorage.ApplySnapshot(rd.Snapshot)
				if err != nil {
					r.Fatal("failed to apply Raft snapshot", zap.Error(err))
				}
				r.Info("applied incoming Raft snapshot", zap.Uint64("snapshot-index", rd.Snapshot.Metadata.Index))

				if err := r.cfg.Storage.Release(rd.Snapshot); err != nil {
					r.Fatal("failed to release Raft wal", zap.Error(err))
				}
			}
			r.Debug("ready------------3")
			confChanged := false
			for _, ent := range rd.CommittedEntries {
				if ent.Type == raftpb.EntryConfChange {
					confChanged = true
					break
				}
			}
			if !islead {
				r.Debug("ready------------3-1")
				msgs := r.processMessages(rd.Messages)
				select {
				case notifyc <- struct{}{}:
				case <-r.stopped:
					return
				}
				r.Debug("ready------------3-2")
				if confChanged {
					select {
					case notifyc <- struct{}{}:
					case <-r.stopped:
						return
					}
				}
				r.Debug("ready------------3-3")
				if len(msgs) > 0 {
					r.cfg.Transport.Send(msgs)
				}

			} else {
				select {
				case notifyc <- struct{}{}:
				case <-r.stopped:
					return
				}
			}
			r.Debug("ready------------4")
			r.node.Advance()

			if confChanged {
				select {
				case raftAdvancedC <- struct{}{}:
				case <-r.stopped:
					return
				}
			}
			r.Debug("ready------------5")

		case <-r.stopped:
			return

		}
	}
}

// remove repeat entries

func (r *RaftNode) removeRepeatEntries(entries []raftpb.Entry) []raftpb.Entry {
	if len(entries) <= 1 {
		return entries
	}
	// fmt.Println("removeRepeatEntries", entries)
	var newEntries []raftpb.Entry
	for i := 0; i < len(entries)-1; i++ {
		if entries[i].Index == entries[i+1].Index {
			continue
		}
		newEntries = append(newEntries, entries[i])
	}
	newEntries = append(newEntries, entries[len(entries)-1])
	return newEntries
}

func (r *RaftNode) handleRaft() {

	defer func() {
		r.Debug("handleRaft stop")
	}()
	for {
		select {
		case ap := <-r.applyc:
			f := schedule.NewJob("server_applyAll", func(ctx context.Context) {
				err := r.fms.Apply(ap)
				if err != nil {
					r.Panic("failed to apply FMS", zap.Error(err))
				}
			})
			r.sched.Schedule(f)
		case <-r.stopped:
			return
		}
	}
}

// func (r *RaftNode) updateCommittedIndex(ap *ToApply) {
// 	var ci uint64
// 	if len(ap.Entries) != 0 {
// 		ci = ap.Entries[len(ap.Entries)-1].Index
// 	}
// 	if ap.Snapshot.Metadata.Index > ci {
// 		ci = ap.Snapshot.Metadata.Index
// 	}
// 	if ci != 0 {
// 		err := r.raftStorage.UpdateCommittedIndex(ci)
// 		if err != nil {
// 			r.Warn("failed to update committed index", zap.Error(err))
// 		}
// 	}
// }

func (r *RaftNode) loopRecv() {

	defer r.Debug("loopRecv stop")
	for {
		select {
		case data, ok := <-r.recvChan:
			if !ok {
				return
			}
			var msg raftpb.Message
			err := msg.Unmarshal(data)
			if err != nil {
				r.Warn("failed to unmarshal raft message", zap.Error(err))
				return
			}
			r.Debug("=========step=======>", zap.String("msg", msg.String()))
			err = r.node.Step(context.Background(), msg)
			if err != nil {
				r.Warn("failed to step raft node", zap.Error(err))
			}
		case <-r.stopped:
			fmt.Println("loopRecv done")
			return
		}
	}

}

func (r *RaftNode) ApplyConfChange(cc raftpb.ConfChange) *raftpb.ConfState {
	confState := r.node.ApplyConfChange(cc)
	err := r.raftStorage.SetConfState(*confState)
	if err != nil {
		r.Error("failed to set conf state", zap.Error(err))
	}
	return confState
}

func (r *RaftNode) AddConfChange(cc raftpb.ConfChange) error {

	var resultMap map[string]interface{}
	err := wkutil.ReadJSONByByte(cc.Context, &resultMap)
	if err != nil {
		return err
	}
	addr := resultMap["addr"].(string)

	r.peersLock.Lock()
	r.peers = append(r.peers, NewPeer(types.ID(cc.NodeID), addr))
	r.peersLock.Unlock()

	err = r.raftStorage.SetPeers(r.peers)
	if err != nil {
		r.Error("failed to set peers", zap.Error(err))
		return err
	}

	err = r.grpcTransporter.AddPeer(types.ID(cc.NodeID), addr)
	if err != nil {
		return err
	}

	return nil
}

func (r *RaftNode) ProposeConfChange(cc raftpb.ConfChange) error {
	return r.node.ProposeConfChange(context.TODO(), cc)
}

func (r *RaftNode) ProposePeer(ctx context.Context, peer *Peer) error {
	cc := raftpb.ConfChange{
		ID:     r.reqIDGen.Next(),
		NodeID: uint64(peer.ID),
		Type:   raftpb.ConfChangeAddNode,
		Context: []byte(wkutil.ToJSON(map[string]interface{}{
			"addr": peer.Addr,
		})),
	}
	if r.cfg.ID != peer.ID {
		err := r.AddPeer(peer)
		if err != nil {
			r.Error("failed to add peer", zap.Error(err))
			return err
		}
	}
	ch := r.w.Register(cc.ID)
	err := r.ProposeConfChange(cc)
	if err != nil {
		r.Error("failed to propose conf change", zap.Error(err))
		r.w.Trigger(cc.ID, nil)
		return err
	}

	select {
	case x := <-ch:
		if x == nil {
			r.Panic("failed to configure")
		}
		resp := x.(*confChangeResponse)
		<-resp.raftAdvanceC
		r.Info(
			"applied a configuration change through raft",
			zap.String("raft-conf-change", cc.Type.String()),
			zap.String("raft-conf-change-node-id", types.ID(cc.NodeID).String()),
		)
		return resp.err
	case <-ctx.Done():
		r.w.Trigger(cc.ID, nil) // GC wait
		return ctx.Err()
	case <-r.stopped:
		return ErrStopped
	}
}

func (r *RaftNode) AppliedTo(applied uint64) error {

	err := r.raftStorage.SetApplied(applied)
	if err != nil {
		return err
	}
	r.applied = applied
	return nil
}

func (r *RaftNode) TriggerProposeConfChange(cc raftpb.ConfChange, raftAdvancedC <-chan struct{}) {
	r.w.Trigger(cc.ID, &confChangeResponse{
		raftAdvanceC: raftAdvancedC,
	})
}

func (r *RaftNode) RemoveConfChange(cc raftpb.ConfChange) error {
	r.peersLock.Lock()
	var newPeers Peers
	for _, peer := range r.peers {
		if peer.ID != types.ID(cc.NodeID) {
			newPeers = append(newPeers, peer)
		}
	}
	r.peers = newPeers
	r.peersLock.Unlock()
	return r.RemovePeer(types.ID(cc.NodeID))
}

func (r *RaftNode) UpdateConfChange(cc raftpb.ConfChange) error {

	var resultMap map[string]interface{}
	err := wkutil.ReadJSONByByte(cc.Context, &resultMap)
	if err != nil {
		return err
	}
	addr := resultMap["addr"].(string)

	r.peersLock.Lock()
	for i, peer := range r.peers {
		if peer.ID == types.ID(cc.NodeID) {
			r.peers[i] = NewPeer(types.ID(cc.NodeID), addr)
		}
	}
	r.peersLock.Unlock()

	return r.grpcTransporter.UpdatePeer(types.ID(cc.NodeID), addr)
}

func (r *RaftNode) AddPeer(peer *Peer) error {
	// fmt.Println("ProposeConfChange-------start")

	return r.grpcTransporter.AddPeer(peer.ID, peer.Addr)
}

func (r *RaftNode) RemovePeer(id types.ID) error {
	return r.grpcTransporter.RemovePeer(id)
}

func (r *RaftNode) GetConfig() *RaftNodeConfig {
	return r.cfg
}

func (r *RaftNode) Propose(ctx context.Context, data []byte) (*wpb.CMDResp, error) {
	req := &wpb.CMDReq{
		Id:   r.reqIDGen.Next(),
		Data: data,
	}
	ch := r.w.Register(req.Id)

	cctx, cancel := context.WithTimeout(ctx, r.cfg.ReqTimeout())
	defer cancel()

	reqData, _ := req.Marshal()
	err := r.node.Propose(ctx, reqData)
	if err != nil {
		r.Error("failed to propose", zap.Error(err))
		r.w.Trigger(req.Id, nil)
		return nil, err
	}

	r.monitor.ProposalsPendingInc()
	defer r.monitor.ProposalsPendingDec()

	select {
	case x := <-ch:
		if x == nil {
			return nil, ErrStopped
		}
		return x.(*wpb.CMDResp), nil
	case <-cctx.Done():
		r.monitor.ProposalsFailedInc()
		r.w.Trigger(req.Id, nil) // GC wait
		return nil, cctx.Err()
	case <-r.stopped:
		r.w.Trigger(req.Id, nil) // GC wait
		return nil, ErrStopped
	}
}

func (r *RaftNode) Trigger(resp *wpb.CMDResp) {
	r.w.Trigger(resp.Id, resp)
}

func (r *RaftNode) processMessages(ms []raftpb.Message) []raftpb.Message {
	sentAppResp := false
	for i := len(ms) - 1; i >= 0; i-- {
		if r.isIDRemoved(ms[i].To) {
			ms[i].To = 0
		}

		//  ---------- MsgAppResp ----------
		if ms[i].Type == raftpb.MsgAppResp {
			if sentAppResp {
				ms[i].To = 0
			} else {
				sentAppResp = true
			}
		}

		//  ---------- MsgSnap ----------
		if ms[i].Type == raftpb.MsgSnap {
			select {
			case r.msgSnapC <- ms[i]:
			default:
				// drop msgSnap if the inflight chan if full.
			}
			ms[i].To = 0
		}

		//  ---------- MsgHeartbeat ----------
		if ms[i].Type == raftpb.MsgHeartbeat {
			ok, exceed := r.td.Observe(ms[i].To)
			if !ok {
				// TODO: limit request rate.
				r.Warn(
					"leader failed to send out heartbeat on time; took too long, leader is overloaded likely from slow disk",
					zap.String("to", fmt.Sprintf("%x", ms[i].To)),
					zap.Duration("heartbeat-interval", r.cfg.Heartbeat),
					zap.Duration("expected-duration", 2*r.cfg.Heartbeat),
					zap.Duration("exceeded-duration", exceed),
				)
				r.monitor.HeartbeatSendFailuresInc()
			}
		}
	}

	return ms
}

func (r *RaftNode) StopChan() chan struct{} {

	return r.stopped
}

func (r *RaftNode) isIDRemoved(id uint64) bool {
	r.removedClusterIDMapLock.Lock()
	defer r.removedClusterIDMapLock.Unlock()
	return r.removedClusterIDMap[types.ID(id)]
}

type Peer struct {
	ID   types.ID
	Addr string // 格式 tcp://xx.xx.xx.xx:xxxx
}

func NewPeer(id types.ID, addr string) *Peer {

	return &Peer{
		ID:   id,
		Addr: addr,
	}
}

func (p *Peer) Marshal() []byte {
	return []byte(wkutil.ToJSON(p))
}

func (p *Peer) Unmarshal(data []byte) error {
	return wkutil.ReadJSONByByte(data, p)
}

type Peers []*Peer

func (p *Peers) Marshal() []byte {
	return []byte(wkutil.ToJSON(p))
}

func (p *Peers) Unmarshal(data []byte) error {
	return wkutil.ReadJSONByByte(data, p)
}

type confChangeResponse struct {
	raftAdvanceC <-chan struct{}
	err          error
}
