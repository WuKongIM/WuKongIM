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
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/schedule"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
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
	fsm FSM

	sched schedule.Scheduler

	// removedClusterIDMap contains the ids of removed members in the cluster.
	removedClusterIDMap     map[uint64]bool
	removedClusterIDMapLock sync.RWMutex

	leadID atomic.Uint64

	grpcTransporter *GRPCTransporter

	OnLead func(lead uint64)

	reqIDGen *idutil.Generator

	w wait.Wait

	recvChan chan transporter.Ready

	applied uint64

	runing atomic.Bool

	ClusterConfigManager *ClusterConfigManager
	clusterFSMManager    *ClusterFSMManager

	startRequestGetClusterConfig atomic.Bool
}

func NewRaftNode(fsm FSM, cfg *RaftNodeConfig) *RaftNode {

	r := &RaftNode{
		Log:                  wklog.NewWKLog(fmt.Sprintf("raftNode[%d]", cfg.ID)),
		tickMu:               new(sync.Mutex),
		stopped:              make(chan struct{}, 10),
		done:                 make(chan struct{}),
		cfg:                  cfg,
		td:                   NewTimeoutDetector(2 * cfg.Heartbeat),
		readStateC:           make(chan raft.ReadState, 1),
		applyc:               make(chan ToApply),
		raftStorage:          NewWALStorage(cfg.LogWALPath, cfg.MetaDBPath),
		removedClusterIDMap:  make(map[uint64]bool),
		msgSnapC:             make(chan raftpb.Message, cfg.MaxInFlightMsgSnap),
		fsm:                  fsm,
		sched:                schedule.NewFIFOScheduler(wklog.NewWKLog("Scheduler")),
		reqIDGen:             idutil.NewGenerator(uint16(cfg.ID), time.Now()),
		w:                    wait.New(),
		recvChan:             make(chan transporter.Ready, 10),
		ClusterConfigManager: NewClusterConfigManager(cfg.ClusterStorePath),
	}
	// r.applyCancelContext, r.applyCancelFnc = context.WithCancel(context.Background())
	r.grpcTransporter = NewGRPCTransporter(r.recvChan, cfg, r.onNodeMessage)
	if cfg.Transport == nil {
		cfg.Transport = r.grpcTransporter
	}
	if cfg.Heartbeat == 0 {
		r.ticker = &time.Ticker{}
	} else {
		r.ticker = time.NewTicker(cfg.Heartbeat)
	}
	r.monitor = cfg.Monitor()
	r.clusterFSMManager = NewClusterFSMManager(r.cfg.ID, r.ClusterConfigManager)
	r.clusterFSMManager.IntervalDuration = cfg.ClusterFSMInterval
	return r
}

func (r *RaftNode) start() error {

	r.ClusterConfigManager.Start()

	r.clusterFSMManager.Start()

	go r.listenClusterConfigChange() // listen cluster config change

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
	peers := r.ClusterConfigManager.GetPeers()
	for _, peer := range peers {
		if peer.Id != r.cfg.ID {
			err := r.grpcTransporter.AddPeer(peer.Id, peer.Addr)
			if err != nil {
				return err
			}
		}
	}

	selfPeer := r.ClusterConfigManager.GetPeer(r.cfg.ID)
	if selfPeer == nil {
		r.ClusterConfigManager.AddOrUpdatePeer(wpb.NewPeer(uint64(r.cfg.ID), r.cfg.Addr))
	}

	if restartNode {
		r.node = r.restartClusterNode(raftCfg)
	} else if r.cfg.Peers != nil && len(r.cfg.Peers) == 0 {
		r.node = r.startSingleNode(raftCfg)
	} else {
		r.node = r.startNewClusterNode(raftCfg)
	}

	err = r.startJoinCluster()
	if err != nil {
		r.Panic("startJoinCluster", zap.Error(err))
	}

	go r.handleRaft()
	go r.run()

	return nil
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

	r.ClusterConfigManager.Stop()

	r.clusterFSMManager.Stop()

	r.Debug("onStop---->1")
	r.ticker.Stop()
	r.Debug("onStop---->2")
	close(r.done)
	r.sched.Stop()
	r.Debug("onStop---->3")

}

func (r *RaftNode) stop() {
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

			// r.Debug("================================ready=====================================")
			// if rd.SoftState != nil {
			// 	r.Debug("SoftState", zap.Uint64("Lead", rd.SoftState.Lead), zap.String("RaftState", rd.SoftState.RaftState.String()))
			// }
			// r.Debug("HardState", zap.String("hardState", rd.HardState.String()))
			// for _, entry := range rd.Entries {
			// 	r.Debug("entry", zap.String("entry", entry.String()))
			// }
			// for _, entry := range rd.CommittedEntries {
			// 	r.Debug("committedEntry", zap.String("committedEntry", entry.String()))
			// }
			// for _, message := range rd.Messages {
			// 	r.Debug("message", zap.String("message", message.String()))
			// }
			// r.Debug("=====================================================================")

			if rd.SoftState != nil {

				r.ClusterConfigManager.UpdateSoftState(uint64(r.cfg.ID), rd.SoftState)

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
				r.ClusterConfigManager.UpdateHardState(uint64(r.cfg.ID), rd.HardState)
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
			confChanged := false
			for _, ent := range rd.CommittedEntries {
				if ent.Type == raftpb.EntryConfChange {
					confChanged = true
					break
				}
			}
			if !islead {
				msgs := r.processMessages(rd.Messages)
				select {
				case notifyc <- struct{}{}:
				case <-r.stopped:
					return
				}
				if confChanged {
					select {
					case notifyc <- struct{}{}:
					case <-r.stopped:
						return
					}
				}
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
			r.node.Advance()

			if confChanged {
				select {
				case raftAdvancedC <- struct{}{}:
				case <-r.stopped:
					return
				}
			}

		case <-r.stopped:
			return

		}
	}
}

func (r *RaftNode) apply(ap ToApply) error {
	for _, e := range ap.Entries {
		switch e.Type {
		case raftpb.EntryConfChange:

			// req := &CMDReq{}
			// err := req.Unmarshal(e.Data)
			// if err != nil {
			// 	r.Panic("UnmarshalCMDReq", zap.Error(err))
			// }
			// fmt.Println("req.Param-->", req.Param)
			// fmt.Println("req.id-->", req.Id)
			var cc raftpb.ConfChange
			err := cc.Unmarshal(e.Data)
			if err != nil {
				r.Panic("unmarshal confChange", zap.Error(err))
			}

			r.Debug("Apply Config", zap.String("config", cc.String()))

			_ = r.ApplyConfChange(cc)

			switch cc.Type {
			case raftpb.ConfChangeAddNode:
				err = r.addPeerByConfChange(cc)
			case raftpb.ConfChangeRemoveNode:
				err = r.removePeerByConfChange(cc)
			case raftpb.ConfChangeUpdateNode:
				err = r.updatePeerByConfChange(cc)
			}
			if err != nil {
				r.Panic("Apply Config", zap.Error(err))
			}
			r.triggerProposeConfChange(cc, ap.RaftAdvancedC)

		case raftpb.EntryNormal:
			r.Debug("Apply Normal", zap.String("data", string(e.Data)))

			if len(e.Data) > 0 {
				req := &transporter.CMDReq{}
				err := req.Unmarshal(e.Data)
				if err != nil {
					r.Panic("UnmarshalCMDReq", zap.Error(err))
				}

				resp, err := r.fsm.Apply(req)
				if err != nil {
					r.Error("Apply", zap.Error(err))
					r.triggerWithError(req.Id, err)
					continue
				}
				if resp == nil {
					r.triggerWithNil(req.Id)
				} else {
					resp.Id = req.Id
					r.trigger(resp)
				}

			}
			err := r.AppliedTo(e.Index)
			if err != nil {
				r.Warn("AppliedTo", zap.Error(err))
			}
		}
	}
	select {
	case <-ap.Notifyc:
	case <-r.StopChan():
		r.Debug("Apply----stop-->", zap.Any("ap", ap.Entries))
		return nil
	}
	return nil
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
				err := r.apply(ap)
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

func (r *RaftNode) applyConfChange(cc raftpb.ConfChange) *raftpb.ConfState {
	confState := r.node.ApplyConfChange(cc)
	err := r.raftStorage.SetConfState(*confState)
	if err != nil {
		r.Error("failed to set conf state", zap.Error(err))
	}
	return confState
}

func (r *RaftNode) addPeerByConfChange(cc raftpb.ConfChange) error {

	var resultMap map[string]interface{}
	err := wkutil.ReadJSONByByte(cc.Context, &resultMap)
	if err != nil {
		return err
	}
	addr := resultMap["addr"].(string)

	peer := r.ClusterConfigManager.GetPeer(cc.NodeID)
	if peer == nil {
		peer = &wpb.Peer{
			Id:     cc.NodeID,
			Addr:   addr,
			Status: wpb.Status_Joined,
		}
	} else {
		peer.Status = wpb.Status_Joined
		peer.Addr = addr
	}

	r.ClusterConfigManager.AddOrUpdatePeer(peer)
	err = r.ClusterConfigManager.Save()
	if err != nil {
		return err
	}

	err = r.grpcTransporter.AddPeer(cc.NodeID, addr)
	if err != nil {
		return err
	}

	return nil
}

func (r *RaftNode) removePeerByConfChange(cc raftpb.ConfChange) error {
	r.ClusterConfigManager.RemovePeer(cc.NodeID)
	_ = r.ClusterConfigManager.Save()
	return r.grpcTransporter.RemovePeer(cc.NodeID)
}

func (r *RaftNode) updatePeerByConfChange(cc raftpb.ConfChange) error {

	var resultMap map[string]interface{}
	err := wkutil.ReadJSONByByte(cc.Context, &resultMap)
	if err != nil {
		return err
	}
	addr := resultMap["addr"].(string)

	peer := r.ClusterConfigManager.GetPeer(cc.NodeID)
	peer.Addr = addr
	r.ClusterConfigManager.AddOrUpdatePeer(peer)
	_ = r.ClusterConfigManager.Save()

	return r.grpcTransporter.UpdatePeer(cc.NodeID, addr)
}

func (r *RaftNode) proposeConfChange(cc raftpb.ConfChange) error {
	return r.node.ProposeConfChange(context.TODO(), cc)
}

func (r *RaftNode) proposePeer(ctx context.Context, peer *wpb.Peer) error {
	cc := raftpb.ConfChange{
		ID:     r.reqIDGen.Next(),
		NodeID: uint64(peer.Id),
		Type:   raftpb.ConfChangeAddNode,
		Context: []byte(wkutil.ToJSON(map[string]interface{}{
			"addr": peer.Addr,
		})),
	}
	if r.cfg.ID != peer.Id {
		err := r.grpcTransporter.AddPeer(peer.Id, peer.Addr)
		if err != nil {
			r.Error("failed to add peer", zap.Error(err))
			return err
		}
	}
	ch := r.w.Register(cc.ID)
	err := r.proposeConfChange(cc)
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

func (r *RaftNode) appliedTo(applied uint64) error {

	err := r.raftStorage.SetApplied(applied)
	if err != nil {
		return err
	}
	r.applied = applied
	return nil
}

func (r *RaftNode) triggerProposeConfChange(cc raftpb.ConfChange, raftAdvancedC <-chan struct{}) {
	r.w.Trigger(cc.ID, &confChangeResponse{
		raftAdvanceC: raftAdvancedC,
	})
}

func (r *RaftNode) GetConfig() *RaftNodeConfig {
	return r.cfg
}

func (r *RaftNode) sendCMD(req *transporter.CMDReq) (*transporter.CMDResp, error) {
	if req.To == 0 {
		return nil, nil
	}
	if req.Id == 0 {
		req.Id = r.reqIDGen.Next()
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.cfg.ReqTimeout())
	defer cancel()

	ch := r.w.Register(req.Id)
	err := r.grpcTransporter.SendCMD(req)
	if err != nil {
		r.Error("failed to send cmd", zap.Error(err))
		r.w.Trigger(req.Id, nil)
		return nil, err
	}
	select {
	case x := <-ch:
		if x == nil {
			return nil, ErrStopped
		}
		resp, ok := x.(*transporter.CMDResp)
		if ok {
			return resp, nil
		}
		err, ok := x.(error)
		if ok {
			return nil, err
		}
		return nil, nil
	case <-timeoutCtx.Done():
		r.w.Trigger(req.Id, nil) // GC wait
		return nil, timeoutCtx.Err()
	case <-r.stopped:
		return nil, ErrStopped
	}

}

func (r *RaftNode) sendCMDTo(addr string, req *transporter.CMDReq) (*transporter.CMDResp, error) {

	if req.Id == 0 {
		req.Id = r.reqIDGen.Next()
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.cfg.ReqTimeout())
	defer cancel()
	fmt.Println("register-id-->", req.Id)
	ch := r.w.Register(req.Id)
	cli, err := r.grpcTransporter.SendCMDTo(addr, req)
	if err != nil {
		r.Error("failed to send cmd", zap.Error(err))
		r.w.Trigger(req.Id, nil)
		return nil, err
	}
	defer func() {
		_ = cli.Disconnect()
	}()

	cli.OnMessage(func(resp *wksdk.Message) {
		fmt.Println("resp-message->", resp)

		cmdResp := &transporter.CMDResp{}
		err = cmdResp.Unmarshal(resp.Payload)
		if err != nil {
			r.Error("failed to unmarshal cmd resp", zap.Error(err))
			r.w.Trigger(req.Id, nil)
			return
		}
		r.trigger(cmdResp)
	})

	select {
	case x := <-ch:
		if x == nil {
			return nil, ErrStopped
		}
		resp, ok := x.(*transporter.CMDResp)
		if ok {
			return resp, nil
		}
		err, ok := x.(error)
		if ok {
			return nil, err
		}
		return nil, nil
	case <-timeoutCtx.Done():
		r.w.Trigger(req.Id, nil) // GC wait
		return nil, timeoutCtx.Err()
	case <-r.stopped:
		return nil, ErrStopped
	}
}

func (r *RaftNode) propose(ctx context.Context, cmd *transporter.CMDReq) (*transporter.CMDResp, error) {

	ch := r.w.Register(cmd.Id)

	cctx, cancel := context.WithTimeout(ctx, r.cfg.ReqTimeout())
	defer cancel()

	reqData, _ := cmd.Marshal()
	err := r.node.Propose(ctx, reqData)
	if err != nil {
		r.Error("failed to propose", zap.Error(err))
		r.w.Trigger(cmd.Id, nil)
		return nil, err
	}

	r.monitor.ProposalsPendingInc()
	defer r.monitor.ProposalsPendingDec()

	select {
	case x := <-ch:
		if x == nil {
			return nil, ErrStopped
		}
		resp, ok := x.(*transporter.CMDResp)
		if ok {
			return resp, nil
		}
		err, ok := x.(error)
		if ok {
			return nil, err
		}
		return nil, nil
	case <-cctx.Done():
		r.monitor.ProposalsFailedInc()
		r.w.Trigger(cmd.Id, nil) // GC wait
		return nil, cctx.Err()
	case <-r.stopped:
		r.w.Trigger(cmd.Id, nil) // GC wait
		return nil, ErrStopped
	}
}

func (r *RaftNode) trigger(resp *transporter.CMDResp) {
	r.w.Trigger(resp.Id, resp)
}

func (r *RaftNode) triggerWithError(id uint64, err error) {
	r.w.Trigger(id, err)
}

func (r *RaftNode) triggerWithNil(id uint64) {
	r.w.Trigger(id, Nil{})
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
	return r.removedClusterIDMap[id]
}

func (r *RaftNode) startJoinCluster() error {
	if len(r.cfg.Join) == 0 {
		return nil
	}
	joinList := r.cfg.Join
	clusterConfig := r.ClusterConfigManager.GetClusterConfig()

	newJoinList := make([]string, 0, len(r.cfg.Join))
	if len(joinList) > 0 {
		for _, addr := range joinList {
			exist := false
			for _, peer := range clusterConfig.Peers {
				if peer.Addr == addr {
					exist = true
					break
				}
			}
			if !exist {
				newJoinList = append(newJoinList, addr)
			}
		}
	}
	fmt.Println("newJoinList----->", newJoinList)
	if len(newJoinList) == 0 {
		return nil
	}

	return r.join(newJoinList)
}

func (r *RaftNode) join(addrs []string) error {
	if len(addrs) == 0 {
		return nil
	}

	for _, addr := range addrs {
		clusterConfig, err := r.GetClusterConfigFrom(addr)
		if err != nil {
			r.Error("failed to get clusterconfig", zap.Error(err))
			return err
		}
		newAddrs := make([]string, 0, len(addrs))
		if len(clusterConfig.Peers) > 0 {
			for _, peer := range clusterConfig.Peers {
				if peer.Id == r.cfg.ID {
					continue
				}
				exist := false
				for _, addr := range addrs {
					if peer.Addr == addr {
						exist = true
						break
					}
				}
				if !exist {
					newAddrs = append(newAddrs, peer.Addr)
				}
				err = r.JoinTo(peer)
				if err != nil {
					r.Error("failed to join", zap.Error(err), zap.String("peer", peer.String()))
					return err
				}
			}
		}
		if len(newAddrs) > 0 {
			return r.join(newAddrs)
		}
	}
	return nil

}

type Peer struct {
	ID   uint64
	Addr string // 格式 tcp://xx.xx.xx.xx:xxxx
}

func NewPeer(id uint64, addr string) *Peer {

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
