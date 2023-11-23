package cluster

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ClusterManager struct {
	cluster    *pb.Cluster
	configPath string
	wklog.Log
	tickChan chan struct{}
	stopChan chan struct{}
	sync.RWMutex
	readyChan             chan ClusterReady
	opts                  *ClusterManagerOptions
	leaderID              atomic.Uint64
	slotLeaderRelationSet *pb.SlotLeaderRelationSet
}

func NewClusterManager(opts *ClusterManagerOptions) *ClusterManager {

	c := &ClusterManager{
		cluster:    &pb.Cluster{},
		configPath: opts.ConfigPath,
		Log:        wklog.NewWKLog("ClusterManager"),
		tickChan:   make(chan struct{}),
		stopChan:   make(chan struct{}),
		readyChan:  make(chan ClusterReady),
		opts:       opts,
		slotLeaderRelationSet: &pb.SlotLeaderRelationSet{
			SlotLeaderRelations: make([]*pb.SlotLeaderRelation, 0),
		},
	}

	clusterConfig, err := c.load()
	if err != nil {
		c.Panic("load cluster config is error", zap.Error(err))
	}
	if clusterConfig != nil {
		c.cluster = clusterConfig
	} else {
		c.cluster = &pb.Cluster{}
	}
	return c
}

func (c *ClusterManager) Start() error {

	go c.loop()
	go c.loopTick()
	return nil
}

func (c *ClusterManager) Stop() {
	close(c.stopChan)
}

func (c *ClusterManager) loop() {
	for {
		select {
		case <-c.tickChan:
			c.tick()
		case <-c.stopChan:
			return

		}
	}
}

func (c *ClusterManager) tick() {

	ready := c.checkClusterConfig()
	if IsEmptyClusterReady(ready) {
		return
	}
	c.readyChan <- ready
}

func (c *ClusterManager) loopTick() {
	tick := time.NewTicker(time.Second)
	for {
		select {
		case <-c.stopChan:
			return
		case <-tick.C:
			c.tickChan <- struct{}{}
		}
	}
}

func (c *ClusterManager) checkClusterConfig() ClusterReady {

	clusterReady := c.checkPeers()
	if !IsEmptyClusterReady(clusterReady) {
		return clusterReady
	}

	clusterReady = c.checkAllocSlots()
	if !IsEmptyClusterReady(clusterReady) {
		return clusterReady
	}
	clusterReady = c.checkSlotStates()
	if !IsEmptyClusterReady(clusterReady) {
		return clusterReady
	}
	clusterReady = c.checkSlotLeaders()
	if !IsEmptyClusterReady(clusterReady) {
		return clusterReady
	}
	return EmptyClusterReady

}

func (c *ClusterManager) checkSlotLeaders() ClusterReady {
	if c.leaderID.Load() == 0 {
		return EmptyClusterReady
	}
	c.Lock()
	defer c.Unlock()
	if len(c.slotLeaderRelationSet.SlotLeaderRelations) == 0 {
		return EmptyClusterReady
	}
	var slotLeaderRelations []*pb.SlotLeaderRelation
	for _, slotLeaderRelation := range c.slotLeaderRelationSet.SlotLeaderRelations {
		if slotLeaderRelation.NeedUpdate && slotLeaderRelation.Leader == c.opts.PeerID && slotLeaderRelation.Leader != 0 {
			slotLeaderRelations = append(slotLeaderRelations, slotLeaderRelation)
		}
	}
	if len(slotLeaderRelations) > 0 {
		return ClusterReady{
			SlotLeaderRelationSet: &pb.SlotLeaderRelationSet{
				SlotLeaderRelations: slotLeaderRelations,
			},
		}
	}
	return EmptyClusterReady
}

func (c *ClusterManager) checkPeers() ClusterReady {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 && strings.TrimSpace(c.opts.Join) == "" {
		return EmptyClusterReady
	}

	if strings.TrimSpace(c.opts.Join) != "" {
		if len(c.cluster.Peers) == 0 {
			peerStrs := strings.Split(c.opts.Join, "@")
			if len(peerStrs) != 2 {
				return EmptyClusterReady
			}
			toPeerID, _ := strconv.ParseInt(peerStrs[0], 10, 64)
			if toPeerID <= 0 {
				return EmptyClusterReady
			}
			return ClusterReady{
				JoinReq: &pb.JoinReq{
					ToPeerID: uint64(toPeerID),
					JoinPeer: &pb.Peer{
						PeerID:         c.opts.PeerID,
						ServerAddr:     c.opts.ServerAddr,
						GrpcServerAddr: c.opts.GRPCServerAddr,
						Join:           true,
					},
				},
			}
		}
	}

	// 如果领导节点发生变化则更新
	if c.leaderID.Load() != 0 && c.cluster.Leader != c.leaderID.Load() {
		c.cluster.Leader = c.leaderID.Load()
		if err := c.save(); err != nil {
			c.Error("set leader id error", zap.Error(err))
		}
	}

	// 判断集群配置里的自己的节点的信息是否正确，如果不正确则发起更新提案
	for _, peer := range c.cluster.Peers {
		if c.isLeader() {
			if peer.Join && peer.PeerID != c.opts.PeerID {
				if peer.JoinState == pb.JoinState_JoinStateWill {
					return ClusterReady{
						JoinAction: &JoinAction{
							PeerID:     peer.PeerID,
							ActionType: JoinActionTypeJoin,
						},
					}
				} else if peer.JoinState == pb.JoinState_JoinStateDoing {
					return ClusterReady{
						JoinAction: &JoinAction{
							PeerID:     peer.PeerID,
							ActionType: JoinActionTypeUpdateClusterConfig,
						},
					}

				}
			}
		}
		if peer.PeerID == c.opts.PeerID {
			if peer.Join {
				if peer.JoinState == pb.JoinState_JoinStateDoing {
					return ClusterReady{
						JoinAction: &JoinAction{
							PeerID:     peer.PeerID,
							ActionType: JoinActionStart,
						},
					}
				}
			}

			updatePeer := peer.GrpcServerAddr != c.opts.GRPCServerAddr || peer.ApiServerAddr != c.opts.APIServerAddr
			if peer.Join && peer.JoinState != pb.JoinState_JoinStateDone { // 如果是新加入的节点，当节点还没有加入完成时，不更新节点信息
				updatePeer = false
			}

			if updatePeer {
				peerClone := peer.Clone()
				peerClone.GrpcServerAddr = c.opts.GRPCServerAddr
				peerClone.ApiServerAddr = c.opts.APIServerAddr
				return ClusterReady{
					UpdatePeer: peerClone,
				}
			}
		}
	}

	return EmptyClusterReady
}

// 检查是否需要分配slot
func (c *ClusterManager) checkAllocSlots() ClusterReady {
	c.Lock()
	defer c.Unlock()

	if !c.isLeader() {
		return EmptyClusterReady
	}
	allocateSlots := make([]*pb.AllocateSlot, 0)
	if len(c.cluster.Slots) == c.opts.SlotCount {
		return EmptyClusterReady
	}
	if len(c.cluster.Peers) == 0 {
		return EmptyClusterReady
	}
	fakeCluster := c.cluster.Clone()
	for i := 0; i < int(c.cluster.SlotCount); i++ {
		slotID := uint32(i)
		hasSlot := false
		for _, slot := range c.cluster.Slots {
			if slot.Slot == slotID {
				hasSlot = true
				break
			}
		}
		if !hasSlot {
			peerIDs := c.getLeastSlotPeers(fakeCluster)
			allocateSlots = append(allocateSlots, &pb.AllocateSlot{
				Slot:  slotID,
				Peers: peerIDs,
			})
			c.fakeAllocSlot(slotID, peerIDs, fakeCluster)
		}
	}
	if len(allocateSlots) > 0 {
		return ClusterReady{
			AllocateSlotSet: &pb.AllocateSlotSet{
				AllocateSlots: allocateSlots,
			},
		}
	}
	return EmptyClusterReady
}

func (c *ClusterManager) checkSlotStates() ClusterReady {
	c.Lock()
	defer c.Unlock()

	if c.leaderID.Load() == 0 {
		return EmptyClusterReady
	}

	if c.opts.GetSlotState == nil {
		return EmptyClusterReady
	}

	actions := make([]*SlotAction, 0)
	for _, slot := range c.cluster.Slots {
		slotState := c.opts.GetSlotState(slot.Slot)
		contain := false
		for _, peeID := range slot.Peers {
			if peeID == c.opts.PeerID {
				contain = true
				break
			}
		}
		if !contain {
			continue
		}
		if slotState == SlotStateNotStart {
			actions = append(actions, &SlotAction{
				SlotID: slot.Slot,
				Action: SlotActionStart,
			})

		}
	}
	if len(actions) > 0 {
		return ClusterReady{
			SlotActions: actions,
		}
	}
	return EmptyClusterReady
}

// 检查加入的节点是否分配了slot
func (c *ClusterManager) checkJoinPeerSlotAlloc() ClusterReady {
	c.Lock()
	defer c.Unlock()

	if c.leaderID.Load() == 0 {
		return EmptyClusterReady
	}
	var clusterClone *pb.Cluster
	for _, peer := range c.cluster.Peers {
		if peer.Join && peer.JoinState == pb.JoinState_JoinStateDone {
			for _, slot := range c.cluster.Slots {
				if len(slot.WillJoin) == 0 {
					if clusterClone == nil {
						clusterClone = c.cluster.Clone()
					}
					peers := c.getLeastSlotPeers(clusterClone)
					slot.WillJoin = peers
				}
			}
		}
	}

	return EmptyClusterReady
}

func (c *ClusterManager) isLeader() bool {
	return c.leaderID.Load() == c.opts.PeerID
}

func (c *ClusterManager) GetLeaderPeerID() uint64 {
	return c.leaderID.Load()
}

// 获取指定数量的最少slot的节点
func (c *ClusterManager) getLeastSlotPeers(clusterClone *pb.Cluster) []uint64 {
	var leastSlotPeers []uint64

	peers := append([]*pb.Peer{}, clusterClone.Peers...)
	sort.Slice(peers, func(i, j int) bool {
		return c.getSlotCount(clusterClone, peers[i].PeerID) < c.getSlotCount(clusterClone, peers[j].PeerID)
	})

	for i := 0; i < int(clusterClone.ReplicaCount) && i < len(peers); i++ {
		leastSlotPeers = append(leastSlotPeers, peers[i].PeerID)
	}

	return leastSlotPeers
}

func (c *ClusterManager) fakeAllocSlot(slot uint32, peers []uint64, fakeCluster *pb.Cluster) {
	exist := false
	for _, slotObj := range fakeCluster.Slots {
		if slotObj.Slot == slot {
			exist = true
			slotObj.Peers = peers
			break
		}
	}
	if !exist {
		fakeCluster.Slots = append(fakeCluster.Slots, &pb.Slot{
			Slot:  slot,
			Peers: peers,
		})
	}
}

func (c *ClusterManager) getSlotCount(fakeCluster *pb.Cluster, peerID uint64) int {
	var count = 0
	for _, slot := range fakeCluster.Slots {
		for _, pID := range slot.Peers {
			if pID == peerID {
				count++
			}
		}
	}
	return count
}

// // 获取slot最少的并且不存在指定slot的节点
// func (c *ClusterManager) getLeastSlotAndNotExistNode(slot uint32) *pb.Node {
// 	var node *pb.Node
// 	var count = 0
// 	for _, n := range c.cluster.Nodes {
// 		if c.existSlot(n.NodeID, slot) {
// 			continue
// 		}
// 		if count == 0 {
// 			node = n
// 			count = len(n.Slots)
// 		} else {
// 			if len(n.Slots) < count {
// 				node = n
// 				count = len(n.Slots)
// 			}
// 		}
// 	}
// 	return node
// }

// // slotReplicaCount 计算slot的副本数量
// func (c *ClusterManager) slotReplicaCount(slot uint32) int {
// 	var count = 0
// 	for _, node := range c.cluster.Nodes {
// 		for _, slotObj := range node.Slots {
// 			if slotObj.Slot == slot {
// 				count++
// 			}
// 		}
// 	}
// 	return count
// }

func (c *ClusterManager) load() (*pb.Cluster, error) {
	err := os.MkdirAll(path.Dir(c.configPath), 0755)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(c.configPath, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	configData, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(configData) == 0 {
		return nil, nil
	}
	clusterconfig := &pb.Cluster{}
	err = wkutil.ReadJSONByByte(configData, clusterconfig)
	if err != nil {
		return nil, err
	}
	return clusterconfig, nil
}

func (c *ClusterManager) save() error {
	configPathTmp := c.configPath + ".tmp"
	f, err := os.Create(configPathTmp)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(wkutil.ToJSON(c.cluster))
	if err != nil {
		return err
	}
	return os.Rename(configPathTmp, c.configPath)
}

func (c *ClusterManager) UpdateClusterConfig(cluster *pb.Cluster) {
	c.Lock()
	defer c.Unlock()
	c.cluster = cluster
	err := c.save()
	if err != nil {
		c.Error("update cluster config error", zap.Error(err))
	}
}

func (c *ClusterManager) UpdatePeerConfig(peer *pb.Peer) {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		return
	}
	for idx, p := range c.cluster.Peers {
		if p.PeerID == peer.PeerID {
			c.cluster.Peers[idx] = peer
			break
		}
	}
	err := c.save()
	if err != nil {
		c.Error("update peer config error", zap.Error(err))
	}
}

func (c *ClusterManager) AddOrUpdatePeerConfig(peer *pb.Peer) {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		c.cluster.Peers = make([]*pb.Peer, 0)
	}
	exist := false
	for idx, p := range c.cluster.Peers {
		if p.PeerID == peer.PeerID {
			c.cluster.Peers[idx] = peer
			exist = true
			break
		}
	}
	if !exist {
		c.cluster.Peers = append(c.cluster.Peers, peer)
	}
	err := c.save()
	if err != nil {
		c.Error("update peer config error", zap.Error(err))
	}
}

func (c *ClusterManager) Save() error {
	c.Lock()
	defer c.Unlock()
	return c.save()
}

// func (c *ClusterManager) AddNewPeer(peerID uint64, addr string) error {
// 	c.Lock()
// 	defer c.Unlock()

// 	if c.existPeer(peerID) {
// 		return nil
// 	}
// 	newPeer := &pb.Peer{
// 		PeerID:     peerID,
// 		ServerAddr: addr,
// 		State:      pb.PeerState_PeerStateInitial,
// 	}
// 	if c.cluster.Peers == nil {
// 		c.cluster.Peers = make([]*pb.Peer, 0)
// 	}
// 	c.cluster.Peers = append(c.cluster.Peers, newPeer)

// 	return c.save()
// }

func (c *ClusterManager) GetPeers() []*pb.Peer {
	c.Lock()
	defer c.Unlock()
	return c.cluster.Peers
}

// 设置节点角色
func (c *ClusterManager) SetPeerRole(peerID uint64, role pb.Role) error {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		return nil
	}
	for _, peer := range c.cluster.Peers {
		if peer.PeerID == peerID {
			peer.Role = role
			break
		}
	}
	return c.save()
}

func (c *ClusterManager) SetLeaderID(leaderID uint64) {
	c.Lock()
	defer c.Unlock()
	fmt.Println("leaderID---------->SetLeaderID", leaderID)
	c.leaderID.Store(leaderID)
	c.cluster.Leader = leaderID
	if err := c.save(); err != nil {
		c.Error("set leader id error", zap.Error(err))
	}
}

func (c *ClusterManager) GetPeer(peerID uint64) *pb.Peer {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		return nil
	}
	return c.getPeer(peerID)
}

func (c *ClusterManager) getPeer(peerID uint64) *pb.Peer {
	if len(c.cluster.Peers) == 0 {
		return nil
	}
	for _, peer := range c.cluster.Peers {
		if peer.PeerID == peerID {
			return peer
		}
	}
	return nil
}

func (c *ClusterManager) GetLeaderPeer(slotID uint32) *pb.Peer {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		return nil
	}
	slot := c.getSlot(slotID)
	if slot == nil {
		return nil
	}
	return c.getPeer(slot.Leader)
}

func (c *ClusterManager) GetOnePeerBySlotID(slotID uint32) *pb.Peer {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Peers) == 0 {
		return nil
	}
	slot := c.getSlot(slotID)
	if slot == nil || len(slot.Peers) == 0 {
		return nil
	}

	randIndex := rand.Intn(len(slot.Peers))
	peerID := slot.Peers[randIndex]

	return c.getPeer(peerID)
}

func (c *ClusterManager) GetSlot(slotID uint32) *pb.Slot {
	c.Lock()
	defer c.Unlock()

	return c.getSlot(slotID)
}
func (c *ClusterManager) getSlot(slotID uint32) *pb.Slot {
	if len(c.cluster.Slots) == 0 {
		return nil
	}
	for _, slot := range c.cluster.Slots {
		if slot.Slot == slotID {
			return slot
		}
	}
	return nil
}

func (c *ClusterManager) AddSlot(s *pb.Slot) {
	c.Lock()
	defer c.Unlock()
	for _, slot := range c.cluster.Slots {
		if slot.Slot == s.Slot { // 存在
			return
		}
	}
	c.cluster.Slots = append(c.cluster.Slots, s)
}

func (c *ClusterManager) SetSlotLeader(slot uint32, leaderID uint64) {
	c.Lock()
	defer c.Unlock()
	exist := false
	for _, s := range c.cluster.Slots {
		if s.Slot == slot {
			s.Leader = leaderID
			exist = true
			break
		}
	}
	existRelation := false
	for _, v := range c.slotLeaderRelationSet.SlotLeaderRelations {
		if v.Slot == slot {
			v.Leader = leaderID
			if leaderID != v.Leader {
				v.NeedUpdate = true
			}
			existRelation = true
			break
		}
	}
	if !existRelation {
		c.slotLeaderRelationSet.SlotLeaderRelations = append(c.slotLeaderRelationSet.SlotLeaderRelations, &pb.SlotLeaderRelation{
			Slot:       slot,
			Leader:     leaderID,
			NeedUpdate: true,
		})
	}
	if exist {
		err := c.save()
		if err != nil {
			c.Warn("set slot leader error", zap.Error(err))
		}
	}
}

func (c *ClusterManager) UpdatedSlotLeaderRelations(slotLeaderRelationSet *pb.SlotLeaderRelationSet) {
	c.Lock()
	defer c.Unlock()

	for _, slotLeaderRelation := range slotLeaderRelationSet.SlotLeaderRelations {
		for _, v := range c.slotLeaderRelationSet.SlotLeaderRelations {
			if v.Slot == slotLeaderRelation.Slot && v.Leader == slotLeaderRelation.Leader {
				v.NeedUpdate = false
				break
			}
		}
	}

}

func (c *ClusterManager) GetSlotCount() uint32 {
	c.Lock()
	defer c.Unlock()
	return c.cluster.SlotCount
}

func (c *ClusterManager) GetClusterConfig() *pb.Cluster {
	c.Lock()
	defer c.Unlock()
	return c.cluster
}

// 是否将新加入的节点
func (c *ClusterManager) IsWillJoin() bool {
	c.Lock()
	defer c.Unlock()
	join := strings.TrimSpace(c.opts.Join) != ""
	if !join {
		return false
	}
	peers := c.cluster.Peers
	if len(peers) == 0 {
		return true
	}
	currPeer := c.getPeer(c.opts.PeerID)
	if currPeer == nil && currPeer.JoinState != pb.JoinState_JoinStateDone {
		return true
	}
	return false
}

var EmptyClusterReady = ClusterReady{}

type ClusterReady struct {
	AllocateSlotSet       *pb.AllocateSlotSet       // 分配slot
	SlotActions           []*SlotAction             // slot行为，比如开始slot的raft
	UpdatePeer            *pb.Peer                  // 需要更新的节点信息
	SlotLeaderRelationSet *pb.SlotLeaderRelationSet // 需要更新的slot和leader的关系
	JoinReq               *pb.JoinReq               // 新加入集群的节点
	JoinAction            *JoinAction               // 新加入集群的节点
	UpdateClusterConfig   *UpdateClusterConfigReq   // 更新集群配置
}

func IsEmptyClusterReady(c ClusterReady) bool {
	return c.AllocateSlotSet == nil && c.SlotActions == nil &&
		c.UpdatePeer == nil && c.SlotLeaderRelationSet == nil &&
		c.JoinReq == nil && c.UpdateClusterConfig == nil &&
		c.JoinAction == nil
}

type SlotState int

const (
	SlotStateNotStart SlotState = iota
	SlotStateStarted
)

type SlotActionType int

const (
	SlotActionStart SlotActionType = iota
)

type JoinActionType int

const (
	JoinActionTypeJoin                JoinActionType = iota
	JoinActionTypeUpdateClusterConfig                // 新加入的节点更新集群配置
	JoinActionStart                                  // 新加入的节点开始启动
)

type SlotAction struct {
	SlotID uint32
	Action SlotActionType
}

type UpdateClusterConfigReq struct {
	FromPeerID uint64
}

type JoinAction struct {
	PeerID     uint64
	ActionType JoinActionType
}
