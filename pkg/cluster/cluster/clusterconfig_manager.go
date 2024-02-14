package cluster

import (
	"context"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type clusterconfigManager struct {
	clusterconfigServer *clusterconfig.Server // 分布式配置服务
	opts                *Options
	stopper             *syncutil.Stopper
	onMessage           func(m clusterconfig.Message)
	wklog.Log
}

func newClusterconfigManager(opts *Options) *clusterconfigManager {
	remoteCfgPath := clusterconfig.WithConfigPath(path.Join(opts.DataDir, "clusterconfig.json"))
	c := &clusterconfigManager{
		opts:    opts,
		Log:     wklog.NewWKLog("clusterconfigManager"),
		stopper: syncutil.NewStopper(),
	}
	c.clusterconfigServer = clusterconfig.New(opts.NodeID, clusterconfig.WithTransport(c), clusterconfig.WithReplicas(opts.Replicas()), remoteCfgPath)
	return c
}

func (c *clusterconfigManager) start() error {
	c.stopper.RunWorker(c.loop)
	return c.clusterconfigServer.Start()
}

func (c *clusterconfigManager) stop() {
	c.clusterconfigServer.Stop()
	c.stopper.Stop()
}

func (c *clusterconfigManager) loop() {
	tk := time.NewTicker(time.Millisecond * 200)
	for {
		select {
		case <-tk.C:
			if c.clusterconfigServer.IsLeader() {
				c.checkClusterConfig()
			}
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *clusterconfigManager) checkClusterConfig() {
	clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()

	newCfg := clusterConfig.Clone()
	var hasChange = false
	// 初始化节点
	changed := c.initNodesIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	// 初始化slots
	changed = c.initSlotsIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	// 检查节点在线状态
	changed = c.nodeOnlineChangeIfNeed(newCfg)
	if changed {
		hasChange = changed
	}

	// 初始化slot leader
	changed = c.initSlotLeaderIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	// 选举slot leader
	changed = c.electionSlotLeaderIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	if hasChange {
		newCfg.Version++
		c.Info("propose config change", zap.Uint64("version", newCfg.Version))
		err := c.clusterconfigServer.ProposeConfigChange(newCfg.Version, c.clusterconfigServer.ConfigManager().GetConfigDataByCfg(newCfg))
		if err != nil {
			c.Error("propose config change error", zap.Error(err))
		}
	}
}

func (c *clusterconfigManager) proposeApiServerAddr(nodeId uint64, apiServerAddr string) error {
	newcfg := c.clusterconfigServer.ConfigManager().GetConfig().Clone()
	c.clusterconfigServer.ConfigManager().UpdateApiServerAddr(nodeId, apiServerAddr, newcfg)
	newcfg.Version++
	err := c.clusterconfigServer.ProposeConfigChange(newcfg.Version, c.clusterconfigServer.ConfigManager().GetConfigDataByCfg(newcfg))
	if err != nil {
		return err
	}
	return nil
}

// 初始化节点
func (c *clusterconfigManager) initNodesIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Nodes) == 0 && len(c.opts.InitNodes) > 0 {
		newNodes := make([]*pb.Node, 0, len(c.opts.InitNodes))
		for replicaID, clusterAddr := range c.opts.InitNodes {
			newNodes = append(newNodes, &pb.Node{
				Id:          replicaID,
				ClusterAddr: clusterAddr,
				AllowVote:   true,
				Online:      true,
			})
		}
		c.clusterconfigServer.ConfigManager().AddOrUpdateNodes(newNodes, newCfg)
		return true
	}
	return false
}

// 初始化slots
func (c *clusterconfigManager) initSlotsIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Slots) == 0 {
		newSlots := make([]*pb.Slot, 0, c.opts.SlotCount)
		for i := uint32(0); i < c.opts.SlotCount; i++ {
			newSlots = append(newSlots, &pb.Slot{
				Id:           i,
				ReplicaCount: c.opts.SlotMaxReplicaCount,
			})
		}
		c.clusterconfigServer.ConfigManager().AddOrUpdateSlots(newSlots, newCfg)
		return true
	}
	return false
}

// 节点在线状态变更
func (c *clusterconfigManager) nodeOnlineChangeIfNeed(newCfg *pb.Config) bool {
	if c.opts.nodeOnlineFnc == nil {
		return false
	}
	changed := false
	for _, n := range newCfg.Nodes {
		online, err := c.opts.nodeOnlineFnc(n.Id)
		if err != nil {
			c.Error("check node online error", zap.Error(err))
			continue
		}
		if online != n.Online {
			c.Info("node online change", zap.Uint64("nodeID", n.Id), zap.Bool("online", online))
			c.clusterconfigServer.ConfigManager().SetNodeOnline(n.Id, online, newCfg)
			changed = true
		}
	}
	return changed
}

func (c *clusterconfigManager) isLeader() bool {
	return c.clusterconfigServer.IsLeader()
}

func (c *clusterconfigManager) leaderId() uint64 {
	return c.clusterconfigServer.Leader()
}

func (c *clusterconfigManager) initSlotLeaderIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Slots) == 0 || len(newCfg.Nodes) == 0 {
		return false
	}

	replicas := make([]uint64, 0, len(newCfg.Nodes))
	for _, n := range newCfg.Nodes {
		if !n.AllowVote {
			continue
		}
		replicas = append(replicas, n.Id)
	}
	if len(replicas) == 0 {
		return false
	}
	hasChange := false
	offset := 0

	for _, slot := range newCfg.Slots {
		if len(slot.Replicas) == 0 {
			replicaCount := slot.ReplicaCount
			if len(replicas) <= int(slot.ReplicaCount) {
				slot.Replicas = replicas

			} else {
				slot.Replicas = make([]uint64, 0, replicaCount)
				for i := uint32(0); i < replicaCount; i++ {
					idx := (offset + int(i)) % len(replicas)
					slot.Replicas = append(slot.Replicas, replicas[idx])
				}
			}
			offset++
		}
		if slot.Leader == 0 { // 选举leader
			randomIndex := globalRand.Intn(len(slot.Replicas))
			slot.Term = 1
			slot.Leader = slot.Replicas[randomIndex]
			hasChange = true
		}

	}
	return hasChange
}

// 选举槽领导
func (c *clusterconfigManager) electionSlotLeaderIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Slots) == 0 || len(newCfg.Nodes) == 0 {
		return false
	}
	hasChange := false

	// 获取等待选举的槽
	waitElectionSlots := map[uint64][]uint32{}              // 等待选举的槽
	electionSlotIds := make([]uint32, 0, len(newCfg.Slots)) // 需要进行选举的槽id集合
	for _, slot := range newCfg.Slots {
		if slot.Leader == 0 {
			continue
		}
		if c.nodeIsOnline(slot.Leader) {
			continue
		}
		for _, replicaId := range slot.Replicas {
			if replicaId != c.opts.NodeID && !c.nodeIsOnline(replicaId) {
				continue
			}
			waitElectionSlots[replicaId] = append(waitElectionSlots[replicaId], slot.Id)
		}
		electionSlotIds = append(electionSlotIds, slot.Id)
	}
	if len(waitElectionSlots) == 0 {
		return false
	}

	// 获取槽在各个副本上的日志高度
	slotInfoResps, err := c.requestSlotInfos(waitElectionSlots)
	if err != nil {
		c.Error("request slot infos error", zap.Error(err))
		return false
	}
	if len(slotInfoResps) == 0 {
		return false
	}

	replicaLastLogInfoMap, err := c.collectSlotLogInfo(slotInfoResps)
	if err != nil {
		c.Error("collect slot log info error", zap.Error(err))
		return false
	}

	// 计算每个槽的领导节点
	slotLeaderMap := c.calculateSlotLeader(replicaLastLogInfoMap, electionSlotIds)
	if len(slotLeaderMap) == 0 {
		c.Warn("没有选举出任何槽领导者！！！", zap.Uint32s("slotIDs", electionSlotIds))
		return false
	}

	getSlot := func(slotId uint32) *pb.Slot {
		for _, slot := range newCfg.Slots {
			if slot.Id == slotId {
				return slot
			}
		}
		return nil
	}

	for slotID, leaderNodeID := range slotLeaderMap {
		slot := getSlot(slotID)
		if slot.Leader != leaderNodeID {
			slot.Leader = leaderNodeID
			slot.Term++
			hasChange = true
		}
	}

	// 获取槽在各个副本上的日志高度
	return hasChange
}

// 收集slot的各个副本的日志高度
func (c *clusterconfigManager) collectSlotLogInfo(slotInfoResps []*SlotLogInfoResp) (map[uint64]map[uint32]uint64, error) {
	slotLogInfos := make(map[uint64]map[uint32]uint64, len(slotInfoResps))
	for _, resp := range slotInfoResps {
		slotLogInfos[resp.NodeId] = make(map[uint32]uint64, len(resp.Slots))
		for _, slotInfo := range resp.Slots {
			slotLogInfos[resp.NodeId][slotInfo.SlotId] = slotInfo.LogIndex
		}
	}
	return slotLogInfos, nil
}

// 计算每个槽的领导节点
func (c *clusterconfigManager) calculateSlotLeader(slotLogInfos map[uint64]map[uint32]uint64, slotIds []uint32) map[uint32]uint64 {
	slotLeaderMap := make(map[uint32]uint64, len(slotIds))
	for _, slotId := range slotIds {
		slotLeaderMap[slotId] = c.calculateSlotLeaderBySlot(slotLogInfos, slotId)
	}
	return slotLeaderMap
}

// 计算槽的领导节点
func (c *clusterconfigManager) calculateSlotLeaderBySlot(slotLogInfos map[uint64]map[uint32]uint64, slotId uint32) uint64 {
	var leader uint64
	var maxLogIndex uint64
	for replicaId, logIndexMap := range slotLogInfos {
		if logIndexMap[slotId] > maxLogIndex {
			maxLogIndex = logIndexMap[slotId]
			leader = replicaId
		}
	}
	return leader
}

// 请求槽的日志高度
func (c *clusterconfigManager) requestSlotInfos(waitElectionSlots map[uint64][]uint32) ([]*SlotLogInfoResp, error) {
	slotInfoResps := make([]*SlotLogInfoResp, 0, len(waitElectionSlots))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for nodeId, slotIds := range waitElectionSlots {
		if nodeId == c.opts.NodeID { // 本节点
			slotInfos, err := c.slotInfos(slotIds)
			if err != nil {
				c.Error("get slot infos error", zap.Error(err))
				continue
			}
			slotInfoResps = append(slotInfoResps, &SlotLogInfoResp{
				NodeId: nodeId,
				Slots:  slotInfos,
			})
			continue
		} else {
			requestGroup.Go(func(nID uint64, sids []uint32) func() error {
				return func() error {
					resp, err := c.requestSlotLogInfo(nID, sids)
					if err != nil {
						c.Warn("request slot log info error", zap.Error(err))
						return nil
					}
					slotInfoResps = append(slotInfoResps, resp)
					return nil
				}
			}(nodeId, slotIds))
		}
	}
	_ = requestGroup.Wait()
	return slotInfoResps, nil

}

func (c *clusterconfigManager) requestSlotLogInfo(nodeId uint64, slotIds []uint32) (*SlotLogInfoResp, error) {
	req := &SlotLogInfoReq{
		SlotIds: slotIds,
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.ReqTimeout)
	defer cancel()
	resp, err := c.opts.requestSlotLogInfo(timeoutCtx, nodeId, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *clusterconfigManager) slotInfos(slotIds []uint32) ([]SlotInfo, error) {
	slotInfos := make([]SlotInfo, 0, len(slotIds))
	for _, slotId := range slotIds {
		shardNo := GetSlotShardNo(slotId)
		lastLogIndex, err := c.opts.ShardLogStorage.LastIndex(shardNo)
		if err != nil {
			return nil, err
		}
		slotInfos = append(slotInfos, SlotInfo{
			SlotId:   slotId,
			LogIndex: lastLogIndex,
		})
	}
	return slotInfos, nil
}

// 获取初始节点
func (c *clusterconfigManager) getInitNodes(cfg *pb.Config) []*pb.Node {
	initNodes := make([]*pb.Node, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		if !n.Join {
			initNodes = append(initNodes, n)
		}
	}
	return initNodes
}

// 获取允许投票的节点
func (c *clusterconfigManager) allowVoteNodes() []*pb.Node {
	cfg := c.clusterconfigServer.ConfigManager().GetConfig()
	nodes := make([]*pb.Node, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		if n.AllowVote {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func (c *clusterconfigManager) handleMessage(m clusterconfig.Message) {
	if c.onMessage != nil {
		c.onMessage(m)
	}
}

func (c *clusterconfigManager) Send(m clusterconfig.Message) error {
	return c.opts.Transport.Send(m.To, &proto.Message{
		MsgType: MsgClusterConfigMsg,
		Content: m.Marshal(),
	}, func() {

	})
}

func (c *clusterconfigManager) OnMessage(f func(m clusterconfig.Message)) {
	c.onMessage = f
}

func (c *clusterconfigManager) getConfigVersion() uint64 {
	return c.clusterconfigServer.ConfigManager().GetConfig().GetVersion()
}

func (c *clusterconfigManager) cloneConfig() *pb.Config {
	return c.clusterconfigServer.ConfigManager().GetConfig().Clone()
}

func (c *clusterconfigManager) slot(id uint32) *pb.Slot {
	return c.clusterconfigServer.ConfigManager().Slot(id)
}

func (c *clusterconfigManager) node(id uint64) *pb.Node {
	return c.clusterconfigServer.ConfigManager().Node(id)
}

func (c *clusterconfigManager) nodeIsOnline(id uint64) bool {
	return c.clusterconfigServer.ConfigManager().NodeIsOnline(id)
}

// 等待配置中的节点数量达到指定数量
func (c *clusterconfigManager) waitConfigNodeCount(count int, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()
			if len(clusterConfig.Nodes) >= count {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}

func (c *clusterconfigManager) waitConfigSlotCount(count uint32, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()
			if len(clusterConfig.Slots) >= int(count) {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}

func (c *clusterconfigManager) waitNodeLeader(timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			if c.clusterconfigServer.Leader() != 0 {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}
