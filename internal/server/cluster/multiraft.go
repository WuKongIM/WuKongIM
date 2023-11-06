package cluster

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/dragonboat/v4"
	"github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/raftio"
	sm "github.com/lni/dragonboat/v4/statemachine"
	"go.uber.org/zap"
)

const PeerShardID uint32 = 1000000 // 将1000000设置为peer之间的shardID，slot不可能大于等于1000000

type MultiRaft struct {
	opts     *MultiRaftOptions
	nodehost *dragonboat.NodeHost
	wklog.Log
	peerShardID    uint32
	initialMembers map[uint64]string
	raftSystemEvent
	slotStartMapLock sync.RWMutex
	slotStartMap     map[uint32]bool
}

func NewMultiRaft(opts *MultiRaftOptions) *MultiRaft {

	initialMembers := make(map[uint64]string)
	for _, peer := range opts.Peers {
		initialMembers[peer.ID] = peer.ServerAddr
	}
	m := &MultiRaft{
		initialMembers: initialMembers,
		opts:           opts,
		Log:            wklog.NewWKLog("MultiRaft"),
		peerShardID:    PeerShardID,
		slotStartMap:   make(map[uint32]bool),
	}
	nhc := config.NodeHostConfig{
		WALDir:              opts.DataDir,
		NodeHostDir:         opts.DataDir,
		RaftAddress:         opts.ServerAddr,
		ListenAddress:       opts.ListenAddr,
		RTTMillisecond:      200,
		SystemEventListener: m,
	}
	nhc.RaftEventListener = m
	nodehost, err := dragonboat.NewNodeHost(nhc)
	if err != nil {
		panic(err)
	}
	m.nodehost = nodehost

	if len(opts.Peers) > 0 {
		peerManager := GetPeerManager()
		for _, peer := range opts.Peers {
			peerManager.AddPeer(peer)
		}
	}

	return m
}
func (m *MultiRaft) Start() error {

	rc := m.getDefaultRaftConfig()
	rc.ShardID = uint64(PeerShardID)
	err := m.nodehost.StartOnDiskReplica(m.initialMembers, false, func(shardID, replicaID uint64) sm.IOnDiskStateMachine {
		return newMultiRaftStateMachine(shardID, replicaID, m.opts)
	}, rc)
	if err != nil {
		m.Panic("StartReplica error", zap.Error(err))
		return err
	}

	return nil
}

func (m *MultiRaft) StartSlot(slot uint32, opts *SlotOptions) error {

	m.slotStartMapLock.RLock()
	started := m.slotStartMap[slot]
	m.slotStartMapLock.RUnlock()
	if started {
		return nil
	}

	rc := m.getDefaultRaftConfig()
	rc.ShardID = uint64(slot)
	initialMembers := make(map[uint64]string)
	fmt.Println("opts.PeerIDs---->", opts.PeerIDs)
	for _, peerID := range opts.PeerIDs {
		for _, v := range m.opts.Peers {
			if v.ID == peerID {
				initialMembers[peerID] = v.ServerAddr
				break
			}
		}
	}
	m.slotStartMapLock.Lock()
	m.slotStartMap[slot] = true
	m.slotStartMapLock.Unlock()
	err := m.nodehost.StartOnDiskReplica(initialMembers, false, func(shardID, replicaID uint64) sm.IOnDiskStateMachine {
		return newMultiRaftStateMachine(shardID, replicaID, m.opts)
	}, rc)
	if err != nil {
		m.slotStartMapLock.Lock()
		m.slotStartMap[slot] = false
		m.slotStartMapLock.Unlock()
		m.Panic("StartReplica error", zap.Uint32("slot", slot), zap.Error(err))
		return err
	}
	return nil
}

func (m *MultiRaft) StopSlot(slot uint32) error {
	m.slotStartMapLock.Lock()
	m.slotStartMap[slot] = false
	m.slotStartMapLock.Unlock()
	return m.nodehost.StopShard(uint64(slot))
}

func (m *MultiRaft) ExistSlot(slot uint32) bool {

	return m.nodehost.HasNodeInfo(uint64(slot), m.opts.PeerID)
}

func (m *MultiRaft) IsStarted(slot uint32) bool {
	m.slotStartMapLock.RLock()
	defer m.slotStartMapLock.RUnlock()
	return m.slotStartMap[slot]
}

func (m *MultiRaft) getDefaultRaftConfig() config.Config {
	return config.Config{
		ReplicaID:              m.opts.PeerID,
		ElectionRTT:            5,
		HeartbeatRTT:           1,
		CheckQuorum:            true,
		DisableAutoCompactions: true,
		CompactionOverhead:     0, // 不压缩
		SnapshotEntries:        0,
	}
}

func (m *MultiRaft) SyncProposeToSlot(slot uint32, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), m.opts.ProposeTimeout)
	session := m.nodehost.GetNoOPSession(uint64(slot))
	result, err := m.nodehost.SyncPropose(ctx, session, data)
	cancel()
	if err != nil {
		m.Error("SyncProposeToSlot error", zap.Error(err))
		return nil, err
	}
	return result.Data, nil

}

func (m *MultiRaft) SyncProposeToPeer(data []byte) error {

	ctx, cancel := context.WithTimeout(context.Background(), m.opts.ProposeTimeout)
	session := m.nodehost.GetNoOPSession(uint64(m.peerShardID))
	_, err := m.nodehost.SyncPropose(ctx, session, data)
	cancel()
	if err != nil {
		m.Error("SyncPropose error", zap.Error(err))
		return err
	}
	return nil

}

func (m *MultiRaft) LeaderUpdated(info raftio.LeaderInfo) {
	if m.opts.OnLeaderChanged != nil {
		m.opts.OnLeaderChanged(uint32(info.ShardID), info.LeaderID)
	}
}

func (m *MultiRaft) MembershipChanged(info raftio.NodeInfo) {
	fmt.Println("MembershipChanged--->")

}

func (m *MultiRaft) Stop() {
	m.nodehost.Close()
}

type MultiRaftOptions struct {
	PeerID          uint64
	DataDir         string
	ServerAddr      string
	ListenAddr      string
	SlotCount       int
	ProposeTimeout  time.Duration
	Peers           []Peer
	OnApplyForPeer  func(entries []sm.Entry) error
	OnApplyForSlot  func(slot uint32, entries []sm.Entry) ([]sm.Entry, error)
	OnLeaderChanged func(slot uint32, leaderID uint64)
}

func NewMultiRaftOptions() *MultiRaftOptions {
	return &MultiRaftOptions{
		SlotCount:      128,
		ProposeTimeout: 3 * time.Second,
	}
}

type multiRaftStateMachine struct {
	ShardID uint64
	PeerID  uint64
	opts    *MultiRaftOptions
}

func newMultiRaftStateMachine(shardID uint64, peerID uint64, opts *MultiRaftOptions) sm.IOnDiskStateMachine {
	return &multiRaftStateMachine{
		ShardID: shardID,
		PeerID:  peerID,
		opts:    opts,
	}
}

func (m *multiRaftStateMachine) Open(stopc <-chan struct{}) (uint64, error) {
	return 0, nil
}

func (m *multiRaftStateMachine) Update(entries []sm.Entry) ([]sm.Entry, error) {
	if m.ShardID == uint64(PeerShardID) {
		err := m.opts.OnApplyForPeer(entries)
		if err != nil {
			return nil, err
		}
	} else {
		fmt.Println("Update---->", len(entries))
		resultEntries, err := m.opts.OnApplyForSlot(uint32(m.ShardID), entries)
		if err != nil {
			return nil, err
		}
		fmt.Println("Update-resultEntries--->", len(resultEntries))
		return resultEntries, nil
	}
	return entries, nil
}

func (m *multiRaftStateMachine) Sync() error {
	return nil
}

func (m *multiRaftStateMachine) Lookup(query interface{}) (interface{}, error) {
	panic("implement me")
}

func (m *multiRaftStateMachine) PrepareSnapshot() (interface{}, error) {
	panic("implement me")
}

func (m *multiRaftStateMachine) SaveSnapshot(interface{}, io.Writer, <-chan struct{}) error {
	panic("implement me")
}

func (m *multiRaftStateMachine) RecoverFromSnapshot(io.Reader, <-chan struct{}) error {
	panic("implement me")
}

func (m *multiRaftStateMachine) Close() error {
	return nil
}

type raftSystemEvent struct {
}

func (r *raftSystemEvent) NodeHostShuttingDown()                            {}
func (r *raftSystemEvent) NodeUnloaded(info raftio.NodeInfo)                {}
func (r *raftSystemEvent) NodeDeleted(info raftio.NodeInfo)                 {}
func (r *raftSystemEvent) NodeReady(info raftio.NodeInfo)                   {}
func (r *raftSystemEvent) MembershipChanged(info raftio.NodeInfo)           {}
func (r *raftSystemEvent) ConnectionEstablished(info raftio.ConnectionInfo) {}
func (r *raftSystemEvent) ConnectionFailed(info raftio.ConnectionInfo)      {}
func (r *raftSystemEvent) SendSnapshotStarted(info raftio.SnapshotInfo)     {}
func (r *raftSystemEvent) SendSnapshotCompleted(info raftio.SnapshotInfo)   {}
func (r *raftSystemEvent) SendSnapshotAborted(info raftio.SnapshotInfo)     {}
func (r *raftSystemEvent) SnapshotReceived(info raftio.SnapshotInfo)        {}
func (r *raftSystemEvent) SnapshotRecovered(info raftio.SnapshotInfo)       {}
func (r *raftSystemEvent) SnapshotCreated(info raftio.SnapshotInfo)         {}
func (r *raftSystemEvent) SnapshotCompacted(info raftio.SnapshotInfo)       {}
func (r *raftSystemEvent) LogCompacted(info raftio.EntryInfo)               {}
func (r *raftSystemEvent) LogDBCompacted(info raftio.EntryInfo)             {}

type Peer struct {
	ID             uint64
	ServerAddr     string
	GRPCServerAddr string
}

func NewPeer(id uint64, serverAddr string) Peer {
	return Peer{
		ID:         id,
		ServerAddr: serverAddr,
	}
}

type PeerManager struct {
	peers map[uint64]Peer
	sync.RWMutex
}

// 单例 PeerManager
var peerManager *PeerManager
var peerManagerOnce sync.Once

func GetPeerManager() *PeerManager {
	peerManagerOnce.Do(func() {
		peerManager = &PeerManager{
			peers: map[uint64]Peer{},
		}
	})
	return peerManager
}

func (p *PeerManager) AddPeer(peer Peer) {
	p.Lock()
	defer p.Unlock()
	p.peers[peer.ID] = peer
}

func (p *PeerManager) GetPeers() []Peer {
	p.RLock()
	defer p.RUnlock()
	peers := make([]Peer, 0, len(p.peers))
	for _, peer := range p.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (p *PeerManager) GetPeer(peerID uint64) Peer {
	p.RLock()
	defer p.RUnlock()
	for _, peer := range p.peers {
		if peer.ID == peerID {
			return peer
		}
	}
	return Peer{}
}

func (p *PeerManager) GetPeerByServerAddr(serverAddr string) Peer {
	p.RLock()
	defer p.RUnlock()
	for _, peer := range p.peers {
		if peer.ServerAddr == serverAddr {
			return peer
		}
	}
	return Peer{}
}

func (p *PeerManager) RemovePeer(peerID uint64) {
	p.Lock()
	defer p.Unlock()
	delete(p.peers, peerID)
}

func (p *PeerManager) GetPeerCount() int {
	p.RLock()
	defer p.RUnlock()
	return len(p.peers)
}
