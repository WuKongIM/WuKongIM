package clusterset

import (
	"io"
	"os"
	"path"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type ClusterSet struct {
	clusterConfigSet *pb.ClusterConfigSet
	configPath       string
	wklog.Log
	opts      *options.Options
	readyChan chan *ClusterReady
	stopChan  chan struct{}
	doneChan  chan struct{}

	partitionManagers map[uint64]*partitionManager

	partitionIDGen atomic.Uint64
	configVersion  atomic.Uint64

	tickChan chan struct{}

	sync.RWMutex

	nodeConfigVersionMap map[uint64]uint64
}

func newClusterSet(opts *options.Options) *ClusterSet {
	d := &ClusterSet{
		configPath:           path.Join(opts.DataDir, "clusterset.json"),
		opts:                 opts,
		readyChan:            make(chan *ClusterReady),
		stopChan:             make(chan struct{}),
		doneChan:             make(chan struct{}),
		partitionManagers:    make(map[uint64]*partitionManager),
		tickChan:             make(chan struct{}),
		nodeConfigVersionMap: make(map[uint64]uint64),
		Log:                  wklog.NewWKLog("clusterset"),
	}
	clusterSetConfig, err := d.load()
	if err != nil {
		d.Panic("load cluster config is error", zap.Error(err))
		return nil
	}
	if clusterSetConfig == nil {
		d.clusterConfigSet = &pb.ClusterConfigSet{
			Version:   d.configVersion.Add(1),
			SlotCount: opts.Cluster.SlotCount,
		}
		slotMap := wkutil.NewSlotBitMap(opts.Cluster.SlotCount)
		slotMap.SetSlotForRange(0, opts.Cluster.SlotCount-uint32(1), true)
		d.clusterConfigSet.Partitions = append(d.clusterConfigSet.Partitions, &pb.PartitionConfig{
			PartitionID: 0,
			Replica:     opts.Cluster.Replica,
			Slots:       slotMap.GetBits(),
			LeaderID:    opts.Cluster.NodeID,
			Nodes: []*pb.NodeConfig{
				{
					NodeID:      opts.Cluster.NodeID,
					Status:      pb.NodeStatus_New,
					ServerAddr:  opts.Cluster.Addr,
					Role:        pb.Role_Follower,
					PartitionID: 0,
				},
			},
		})
	} else {
		d.configVersion.Store(clusterSetConfig.Version)
		d.clusterConfigSet = clusterSetConfig
		sort.Slice(d.clusterConfigSet.Partitions, func(i, j int) bool {
			return d.clusterConfigSet.Partitions[i].PartitionID < d.clusterConfigSet.Partitions[j].PartitionID
		})
	}

	for _, partition := range d.clusterConfigSet.Partitions {
		d.partitionManagers[partition.PartitionID] = newPartitionManager(partition)
	}
	d.save()

	return d
}

func (d *ClusterSet) Start() error {

	go d.loop()
	go d.loopTick()
	return nil
}

func (d *ClusterSet) Stop() {
	close(d.stopChan)
	<-d.doneChan
}

func (c *ClusterSet) loop() {
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

func (c *ClusterSet) loopTick() {
	tick := time.NewTimer(time.Second)
	for {
		select {
		case <-c.stopChan:
			return
		case <-tick.C:
			c.tickChan <- struct{}{}
		}
	}
}

func (c *ClusterSet) tick() {

	c.Lock()
	defer c.Unlock()
	ready := c.checkClusterConfigSet()
	if ready == nil {
		return
	}
	c.readyChan <- ready
}

func (c *ClusterSet) isLeader() bool {
	return c.clusterConfigSet.LeaderID == c.opts.Cluster.NodeID
}

func (c *ClusterSet) leaderID() uint64 {
	return c.clusterConfigSet.LeaderID
}

// 获取当前节点属于的分区
func (c *ClusterSet) getCurrentPartition() *pb.PartitionConfig {
	for _, partition := range c.clusterConfigSet.Partitions {
		for _, n := range partition.Nodes {
			if n.NodeID == c.opts.Cluster.NodeID {
				return partition
			}
		}
	}
	return nil
}

func (c *ClusterSet) onStop() {
	close(c.doneChan)
}

func (c *ClusterSet) checkClusterConfigSet() *ClusterReady {
	if len(c.clusterConfigSet.Partitions) == 0 {
		return nil
	}
	// ========== 检查是否有领导者，如果没有则选举 ==========

	// ========== 检查节点配置都已经到最新 ==========
	ready := c.checkNeedSyncNodes()
	if ready != nil {
		return ready
	}
	// ========== 根据是否需要，重新分配slots ==========
	allocate := c.reallocateSlotIfNeed()
	if allocate {
		return nil
	}
	return nil
}

func (c *ClusterSet) electionIfNeed() {
	if c.clusterConfigSet.LeaderID != 0 { // 有领导就不需要选举
		return
	}
}

// 需要同步配置的节点ID
func (c *ClusterSet) checkNeedSyncNodes() *ClusterReady {
	if !c.isLeader() {
		return nil
	}

	lastVersion := c.clusterConfigSet.Version

	updateNodes := make([]uint64, 0)

	for _, partition := range c.clusterConfigSet.Partitions {
		nodes := partition.Nodes
		for _, node := range nodes {
			v := c.nodeConfigVersionMap[node.NodeID]
			if v >= lastVersion {
				continue
			}
			updateNodes = append(updateNodes, node.NodeID)

		}
	}
	if len(updateNodes) > 0 {
		return &ClusterReady{
			updateNodes: updateNodes,
		}
	}
	return nil
}

// 重新分配槽位
func (c *ClusterSet) reallocateSlotIfNeed() bool {
	if !c.isLeader() {
		return false
	}
	hasAlloc := c.hasPartitionWaitAlloc()
	if !hasAlloc {
		return false
	}
	partitionCount := len(c.clusterConfigSet.Partitions)                        // 分区数量
	slotNumOfPartition := c.clusterConfigSet.SlotCount / uint32(partitionCount) // 每个分区的槽位数量
	remingSlotNum := c.clusterConfigSet.SlotCount % uint32(partitionCount)      // 还剩余的slot数量

	importSlotBitMap := wkutil.NewSlotBitMap(c.clusterConfigSet.SlotCount)
	for _, partition := range c.clusterConfigSet.Partitions {
		if partition.Status == pb.PartitionStatus_WaitAlloc {
			continue
		}
		slotMapBit := wkutil.NewSlotBitMapWithBits(partition.Slots)
		vaildSlotNum := slotMapBit.GetVaildSlotNum()
		shoudSlotNum := slotNumOfPartition
		if vaildSlotNum-int(slotNumOfPartition) < 0 {
			continue
		}
		if remingSlotNum > 0 {
			remingSlotNum--
			shoudSlotNum++
		}
		exportSlots := c.mockExportSlots(int(shoudSlotNum), partition.Slots)

		partition.Exports = append(partition.Exports, &pb.MigrateExport{
			ExportPartitionID: partition.PartitionID,
			ExportWill:        exportSlots,
			Status:            pb.SlotStatus_SlotMigration,
		})
		importSlotBitMap.MergeSlots(exportSlots)
	}
	for _, partition := range c.clusterConfigSet.Partitions {
		if partition.Status == pb.PartitionStatus_WaitAlloc {
			partition.Import = &pb.MigrateImport{
				ImportWill: importSlotBitMap.GetBits(),
				Status:     pb.SlotStatus_SlotMigration,
			}
			partition.Status = pb.PartitionStatus_Allocated
			break
		}
	}
	c.versionInc()
	return true
}

// mock导出slot，不真导出，只是模拟导出，当前集群服务的slot不发生变化
func (c *ClusterSet) mockExportSlots(num int, slots []byte) []byte {
	cloneSlotBitMap := wkutil.NewSlotBitMapWithBits(slots)
	return cloneSlotBitMap.ExportSlots(num)
}

// 是否有待分配的分区
func (c *ClusterSet) hasPartitionWaitAlloc() bool {
	for _, partition := range c.clusterConfigSet.Partitions {
		if partition.Status == pb.PartitionStatus_WaitAlloc {
			return true
		}
	}
	return false
}

func (c *ClusterSet) load() (*pb.ClusterConfigSet, error) {
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
	clusterconfigSet := &pb.ClusterConfigSet{}
	err = wkutil.ReadJSONByByte(configData, clusterconfigSet)
	if err != nil {
		return nil, err
	}
	return clusterconfigSet, nil

}

func (c *ClusterSet) addNewNode(nodecfg *pb.NodeConfig) error {
	c.Lock()
	defer c.Unlock()
	if c.existNode(nodecfg.NodeID) {
		return nil
	}
	if len(c.clusterConfigSet.Partitions) < c.opts.Cluster.MaxPartitionCount { // 如果分区数量小于最大分区数量，则开启一个分区
		newPartitionCfg := &pb.PartitionConfig{
			PartitionID: c.partitionIDGen.Add(1),
			Replica:     c.opts.Cluster.Replica,
			Status:      pb.PartitionStatus_WaitAlloc,
		}
		nodecfg.PartitionID = newPartitionCfg.PartitionID
		nodecfg.Status = pb.NodeStatus_New
		partitionManager := c.addPartition(newPartitionCfg)
		partitionManager.AddNode(nodecfg)

		c.versionInc() // 版本增加
		err := c.save()
		if err != nil {
			return nil
		}
		go func() {
			c.tickChan <- struct{}{}
		}()
	}

	return nil
}

func (c *ClusterSet) versionInc() {
	c.clusterConfigSet.Version = c.configVersion.Add(1)
}

func (c *ClusterSet) version() uint64 {
	return c.clusterConfigSet.Version
}

func (c *ClusterSet) existNode(nodeID uint64) bool {
	for _, partition := range c.clusterConfigSet.Partitions {
		for _, node := range partition.Nodes {
			if node.NodeID == nodeID {
				return true
			}
		}
	}
	return false
}

func (c *ClusterSet) addPartition(newPartitionCfg *pb.PartitionConfig) *partitionManager {
	partitionManager := newPartitionManager(newPartitionCfg)
	c.partitionManagers[newPartitionCfg.PartitionID] = partitionManager

	c.clusterConfigSet.Partitions = append(c.clusterConfigSet.Partitions, newPartitionCfg)

	return partitionManager
}

func (c *ClusterSet) updateClustersetConfig(cfg *pb.ClusterConfigSet) error {
	c.Lock()
	defer c.Unlock()
	c.clusterConfigSet = cfg
	return c.save()
}

func (c *ClusterSet) save() error {
	configPathTmp := c.configPath + ".tmp"
	f, err := os.Create(configPathTmp)
	if err != nil {
		return err
	}
	defer f.Close()

	c.clusterConfigSet.Partitions = c.clusterConfigSet.Partitions[:0]
	for _, partitionManager := range c.partitionManagers {
		c.clusterConfigSet.Partitions = append(c.clusterConfigSet.Partitions, partitionManager.getParitionConfigs())
	}
	_, err = f.WriteString(wkutil.ToJSON(c.clusterConfigSet))
	if err != nil {
		return err
	}
	return os.Rename(configPathTmp, c.configPath)
}

type ClusterReady struct {
	updateNodes []uint64
}
