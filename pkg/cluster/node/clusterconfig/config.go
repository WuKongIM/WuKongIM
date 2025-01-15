package clusterconfig

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Config struct {
	cfg     *types.Config // 配置文件
	cfgFile *os.File
	opts    *Options
	wklog.Log
	mu sync.RWMutex
	// 是否已初始化
	inited bool
}

func NewConfig(opts *Options) *Config {
	cfg := &Config{
		cfg: &types.Config{
			SlotCount:           opts.SlotCount,
			SlotReplicaCount:    opts.SlotMaxReplicaCount,
			ChannelReplicaCount: opts.ChannelMaxReplicaCount,
		},
		opts: opts,
		Log:  wklog.NewWKLog("cluster.config"),
	}

	err := cfg.loadConfigFromFile()
	if err != nil {
		cfg.Panic("Load cluster config from file failed!", zap.Error(err))
	}

	return cfg
}

func (c *Config) loadConfigFromFile() error {
	clusterCfgPath := c.opts.ConfigPath
	var err error
	c.cfgFile, err = os.OpenFile(clusterCfgPath, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		c.Panic("Open cluster config file failed!", zap.Error(err))
	}

	data, err := io.ReadAll(c.cfgFile)
	if err != nil {
		c.Panic("Read cluster config file failed!", zap.Error(err))
	}
	if len(data) > 0 {
		c.inited = true
		if err := wkutil.ReadJSONByByte(data, c.cfg); err != nil {
			c.Panic("Unmarshal cluster config failed!", zap.Error(err))
		}
	}
	return nil
}

// 是否已初始化
func (c *Config) isInitialized() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inited
}

func (c *Config) id() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Version
}

func (c *Config) version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Version
}
func (c *Config) setVersion(version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Version = version
}

func (c *Config) versionInc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Version++
}

func (c *Config) term() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Term
}

func (c *Config) data() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Marshal()
}

func (c *Config) update(cfg *types.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg.Clone()
}

func (c *Config) nodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Nodes
}

func (c *Config) slots() []*types.Slot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Slots
}

// 判断是否有slot
func (c *Config) addSlot(slot *types.Slot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Slots = append(c.cfg.Slots, slot)
}

// 判断是否有节点
func (c *Config) hasNode(id uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.cfg.Nodes {
		if n.Id == id {
			return true
		}
	}
	return false
}

func (c *Config) addOrUpdateNode(node *types.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, n := range c.cfg.Nodes {
		if n.Id == node.Id {
			c.cfg.Nodes[i] = node
			return
		}
	}
	c.cfg.Nodes = append(c.cfg.Nodes, node)
}

// 添加节点
func (c *Config) addNode(node *types.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Nodes = append(c.cfg.Nodes, node)
}

func (c *Config) updateNode(n *types.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, node := range c.cfg.Nodes {
		if node.Id == n.Id {
			c.cfg.Nodes[i] = n
			return
		}
	}
}

func (c *Config) updateApiServerAddr(nodeId uint64, addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			node.ApiServerAddr = addr
			return
		}
	}
}

func (c *Config) updateNodeOnlineStatus(nodeId uint64, online bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			node.Online = online
			if !online {
				node.OfflineCount++
				node.LastOffline = time.Now().Unix()
			}
			return
		}
	}
}

func (c *Config) updateNodeJoining(nodeId uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			node.Status = types.NodeStatus_NodeStatusJoining
			break
		}
	}
	c.cfg.Learners = wkutil.RemoveUint64(c.cfg.Learners, nodeId)
	if c.cfg.MigrateTo == nodeId {
		c.cfg.MigrateTo = 0
		c.cfg.MigrateFrom = 0
	}
}

func (c *Config) updateNodeJoined(nodeId uint64, slots []*types.Slot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			node.Status = types.NodeStatus_NodeStatusJoined
			break
		}
	}

	for _, slot := range slots {
		exist := false
		for i, s := range c.cfg.Slots {
			if s.Id == slot.Id {
				c.cfg.Slots[i] = slot
				exist = true
				break
			}
		}
		if !exist {
			c.cfg.Slots = append(c.cfg.Slots, slot)
		}
	}
}

func (c *Config) updateSlotMigrate(slotId uint32, fromNodeId, toNodeId uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, slot := range c.cfg.Slots {
		if slot.Id == slotId && slot.MigrateFrom == 0 && slot.MigrateTo == 0 {
			slot.MigrateFrom = fromNodeId
			slot.MigrateTo = toNodeId
			if !wkutil.ArrayContainsUint64(slot.Replicas, toNodeId) { // 如果目标节点不在副本列表里，则要加入学习者列表
				slot.Learners = append(slot.Learners, toNodeId)
			}

			break
		}
	}
}

func (c *Config) updateSlots(slots []*types.Slot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, slot := range slots {
		for i, s := range c.cfg.Slots {
			if s.Id == slot.Id {
				c.cfg.Slots[i] = slot
				break
			}
		}
	}
}

func (c *Config) updateNodeStatus(nodeId uint64, status types.NodeStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			node.Status = status
			return
		}
	}
}

func (c *Config) config() *types.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// 获取slot数量
func (c *Config) slotCount() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.SlotCount
}

func (c *Config) slotReplicaCount() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.SlotReplicaCount
}

// 根据id获取slot信息
func (c *Config) slot(id uint32) *types.Slot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, slot := range c.cfg.Slots {
		if slot.Id == id {
			return slot
		}
	}
	return nil
}

// 判断节点是否在线
func (c *Config) nodeOnline(nodeId uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			return node.Online
		}
	}
	return false
}

// 获取允许投票的节点
func (c *Config) allowVoteNodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var nodes []*types.Node
	for _, node := range c.cfg.Nodes {
		if node.AllowVote {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// 获取允许投票的并且已经加入了的节点集合
func (c *Config) allowVoteAndJoinedNodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var nodes []*types.Node
	for _, node := range c.cfg.Nodes {
		if node.AllowVote && node.Status == types.NodeStatus_NodeStatusJoined {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// 获取允许投票的并且已经加入了的节点数量
func (c *Config) allowVoteAndJoinedNodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var count int
	for _, node := range c.cfg.Nodes {
		if node.AllowVote && node.Status == types.NodeStatus_NodeStatusJoined {
			count++
		}
	}
	return count
}

// 获取允许投票的并且已经加入了的在线节点数量
func (c *Config) allowVoteAndJoinedOnlineNodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var count int
	for _, node := range c.cfg.Nodes {
		if node.AllowVote && node.Status == types.NodeStatus_NodeStatusJoined && node.Online {
			count++
		}
	}
	return count
}

// 获取允许投票的并且已经加入了的在线节点数量
func (c *Config) allowVoteAndJoinedOnlineNodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var nodes []*types.Node
	for _, node := range c.cfg.Nodes {
		if node.AllowVote && node.Status == types.NodeStatus_NodeStatusJoined && node.Online {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// 获取所有在线节点
func (c *Config) onlineNodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var nodes []*types.Node
	for _, node := range c.cfg.Nodes {
		if node.Online {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (c *Config) node(id uint64) *types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.cfg.Nodes {
		if n.Id == id {
			return n
		}
	}
	return nil
}

// 是否有加入中的节点
func (c *Config) hasJoiningNode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.cfg.Nodes {
		if n.Status == types.NodeStatus_NodeStatusJoining {
			return true
		}
	}
	return false
}

func (c *Config) hasWillJoinNode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.cfg.Nodes {
		if n.Status == types.NodeStatus_NodeStatusWillJoin {
			return true
		}
	}
	return false
}

func (c *Config) willJoinNodes() []*types.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var nodes []*types.Node
	for _, n := range c.cfg.Nodes {
		if n.Status == types.NodeStatus_NodeStatusWillJoin {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func (c *Config) saveConfig() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.inited = true

	_, err := c.cfgFile.Seek(0, 0)
	if err != nil {
		return err
	}
	err = c.cfgFile.Truncate(0)
	if err != nil {
		return err
	}
	_, err = c.cfgFile.Write([]byte(wkutil.ToJSON(c.cfg)))
	if err != nil {
		return err
	}
	return nil
}
