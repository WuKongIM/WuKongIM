package cluster

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
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
	doneChan chan struct{}
	sync.RWMutex
	readyChan chan ClusterReady
	opts      *ClusterManagerOptions
	leaderID  atomic.Uint64
}

func NewClusterManager(opts *ClusterManagerOptions) *ClusterManager {

	c := &ClusterManager{
		cluster:    &pb.Cluster{},
		configPath: opts.ConfigPath,
		Log:        wklog.NewWKLog("ClusterManager"),
		tickChan:   make(chan struct{}),
		stopChan:   make(chan struct{}),
		readyChan:  make(chan ClusterReady),
		doneChan:   make(chan struct{}),
		opts:       opts,
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
	fmt.Println("ClusterManager--stop...")
	close(c.stopChan)
	<-c.doneChan
}

func (c *ClusterManager) onStop() {
	close(c.doneChan)
}

func (c *ClusterManager) loop() {
	defer c.onStop()
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

	c.Lock()
	defer c.Unlock()
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
	if !c.isLeader() {
		return EmptyClusterReady
	}
	clusterReady := c.checkSlots()
	if !IsEmptyClusterReady(clusterReady) {
		return clusterReady
	}
	return EmptyClusterReady

}

func (c *ClusterManager) checkSlots() ClusterReady {
	fmt.Println("checkSlots----->")
	allocateSlots := make([]*pb.AllocateSlot, 0)
	if len(c.cluster.Slots) == c.opts.SlotCount {
		return EmptyClusterReady
	}
	fakeCluster := c.cluster.Clonse()
	for i := 0; i < c.opts.SlotCount; i++ {
		slotID := uint32(i)
		hasSlot := false
		for _, slot := range c.cluster.Slots {
			if slot.Slot == slotID {
				hasSlot = true
				break
			}
		}
		if !hasSlot {
			nodeIDs := c.getLeastSlotNodes(c.opts.ReplicaCount, fakeCluster)
			allocateSlots = append(allocateSlots, &pb.AllocateSlot{
				Slot:  slotID,
				Nodes: nodeIDs,
			})
			c.fakeAllocSlot(slotID, nodeIDs, fakeCluster)
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

func (c *ClusterManager) hasNewNode() *pb.Node {
	if len(c.cluster.Nodes) == 0 {
		return nil
	}
	for _, node := range c.cluster.Nodes {
		if node.State == pb.NodeState_NodeStateInitial {
			return node
		}
	}
	return nil
}

func (c *ClusterManager) isLeader() bool {
	return c.leaderID.Load() == c.opts.NodeID
}

// 获取指定数量的最少slot的节点
func (c *ClusterManager) getLeastSlotNodes(count int, fakeCluster *pb.Cluster) []uint64 {
	var leastSlotNodes []uint64

	nodes := append([]*pb.Node{}, fakeCluster.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return c.getSlotCount(nodes[i].NodeID) < c.getSlotCount(nodes[j].NodeID)
	})

	for i := 0; i < count && i < len(nodes); i++ {
		leastSlotNodes = append(leastSlotNodes, nodes[i].NodeID)
	}

	return leastSlotNodes
}

func (c *ClusterManager) fakeAllocSlot(slot uint32, nodes []uint64, fakeCluster *pb.Cluster) {
	exist := false
	for _, slotObj := range fakeCluster.Slots {
		if slotObj.Slot == slot {
			exist = true
			slotObj.Nodes = nodes
			break
		}
	}
	if !exist {
		fakeCluster.Slots = append(fakeCluster.Slots, &pb.Slot{
			Slot:  slot,
			Nodes: nodes,
		})
	}
}

func (c *ClusterManager) getSlotCount(nodeID uint64) int {
	var count = 0
	for _, slot := range c.cluster.Slots {
		for _, nID := range slot.Nodes {
			if nID == nodeID {
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

func (c *ClusterManager) AddNewNode(nodeID uint64, addr string) error {
	c.Lock()
	defer c.Unlock()

	if c.existNode(nodeID) {
		return nil
	}
	newNode := &pb.Node{
		NodeID:     nodeID,
		ServerAddr: addr,
		State:      pb.NodeState_NodeStateInitial,
	}
	if c.cluster.Nodes == nil {
		c.cluster.Nodes = make([]*pb.Node, 0)
	}
	c.cluster.Nodes = append(c.cluster.Nodes, newNode)

	return c.save()
}

// 设置节点角色
func (c *ClusterManager) SetNodeRole(nodeID uint64, role pb.Role) error {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Nodes) == 0 {
		return nil
	}
	for _, node := range c.cluster.Nodes {
		if node.NodeID == nodeID {
			node.Role = role
			break
		}
	}
	return c.save()
}

func (c *ClusterManager) SetLeaderID(leaderID uint64) {
	fmt.Println("leaderID---------->SetLeaderID", leaderID)
	c.leaderID.Store(leaderID)
}

func (c *ClusterManager) GetNode(nodeID uint64) *pb.Node {
	c.Lock()
	defer c.Unlock()
	if len(c.cluster.Nodes) == 0 {
		return nil
	}
	for _, node := range c.cluster.Nodes {
		if node.NodeID == nodeID {
			return node
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
	if exist {
		err := c.save()
		if err != nil {
			c.Warn("set slot leader error", zap.Error(err))
		}
	}
}

func (c *ClusterManager) existNode(nodeID uint64) bool {
	if len(c.cluster.Nodes) == 0 {
		return false
	}
	for _, node := range c.cluster.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

var EmptyClusterReady = ClusterReady{}

type ClusterReady struct {
	AddNode         *pb.Node
	AllocateSlotSet *pb.AllocateSlotSet
}

func IsEmptyClusterReady(c ClusterReady) bool {
	return c.AddNode == nil && c.AllocateSlotSet == nil
}

type SlotState int

const (
	SlotStateNotExist SlotState = iota
	SlotStateNormal
	SlotStateElecting
)
