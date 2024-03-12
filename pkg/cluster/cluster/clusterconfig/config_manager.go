package clusterconfig

import (
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type ConfigManager struct {
	sync.Mutex
	cfg     *pb.Config
	cfgFile *os.File
	opts    *Options
	wklog.Log
}

func NewConfigManager(opts *Options) *ConfigManager {

	cm := &ConfigManager{
		cfg: &pb.Config{
			SlotCount: opts.SlotCount,
		},
		opts: opts,
		Log:  wklog.NewWKLog("ConfigManager"),
	}

	configDir := path.Dir(opts.ConfigPath)
	if configDir != "" {
		err := os.MkdirAll(configDir, os.ModePerm)
		if err != nil {
			cm.Panic("create config dir error", zap.Error(err))
		}
	}
	err := cm.initConfigFromFile()
	if err != nil {
		cm.Panic("init cluster config from file error", zap.Error(err))
	}

	opts.AppliedConfigVersion = cm.cfg.Version

	return cm
}

func (c *ConfigManager) GetConfig() *pb.Config {
	return c.cfg
}

func (c *ConfigManager) Slot(id uint32) *pb.Slot {
	for _, slot := range c.cfg.Slots {
		if slot.Id == id {
			return slot
		}
	}
	return nil
}

// 获取指定节点的最小槽
func (c *ConfigManager) MinSlot(nodeId uint64) *pb.Slot {
	var minSlot *pb.Slot
	for _, slot := range c.cfg.Slots {
		if wkutil.ArrayContainsUint64(slot.Replicas, nodeId) {
			if minSlot == nil || minSlot.Id > slot.Id {
				minSlot = slot
			}
		}
	}
	return minSlot
}

// 获取指定节点的槽
func (c *ConfigManager) SlotsWithNodeId(nodeId uint64) []*pb.Slot {
	var slots []*pb.Slot
	for _, slot := range c.cfg.Slots {
		if wkutil.ArrayContainsUint64(slot.Replicas, nodeId) {
			slots = append(slots, slot)
		}
	}
	return slots
}

func (c *ConfigManager) Node(id uint64) *pb.Node {
	c.Lock()
	defer c.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == id {
			return node
		}
	}
	return nil
}

func (c *ConfigManager) NodeIsOnline(id uint64) bool {
	c.Lock()
	defer c.Unlock()
	for _, node := range c.cfg.Nodes {
		if node.Id == id {
			return node.Online
		}
	}
	return false
}

// 迁移槽
func (c *ConfigManager) SlotMigrate(slotId uint32, from, to uint64) (*pb.Config, error) {
	c.Lock()
	defer c.Unlock()
	cfg := c.cfg

	for _, node := range cfg.Nodes {
		imports := node.Imports
		for i, imp := range imports {
			if imp.Slot == slotId {
				if (imp.From == from && imp.To == to) || (imp.From == to && imp.To == from) {
					node.Imports = append(imports[:i], imports[i+1:]...)
					slot := c.Slot(slotId)
					if slot == nil {
						c.Error("slot not found", zap.Uint32("slotId", slotId))
						return nil, ErrSlotNotFound
					}
					if slot.Leader == from { // 如果迁出的节点是leader，则需要重新选举，所以这里设置为0
						slot.Leader = 0
					}
					if !wkutil.ArrayContainsUint64(slot.Replicas, to) {
						slot.Replicas = append(slot.Replicas, to) // 迁入的节点加入副本
					}
					if wkutil.ArrayContainsUint64(slot.Replicas, from) {
						slot.Replicas = wkutil.RemoveUint64(slot.Replicas, from) // 迁出的节点移除副本
					}
				}
			}
		}
		exports := node.Exports
		for i, exp := range exports {
			if exp.Slot == slotId {
				if (exp.From == from && exp.To == to) || (exp.From == to && exp.To == from) {
					node.Exports = append(exports[:i], exports[i+1:]...)
				}
			}
		}
	}
	cfg.Version++
	return cfg, nil
}

func (c *ConfigManager) AddOrUpdateNodes(nodes []*pb.Node, cfg *pb.Config) {
	c.Lock()
	defer c.Unlock()
	for i, node := range nodes {
		if c.existNodeByCfg(node.Id, cfg) {
			cfg.Nodes[i] = node
			continue
		}
		cfg.Nodes = append(cfg.Nodes, node)
	}
}

func (c *ConfigManager) UpdateApiServerAddr(nodeId uint64, addr string, cfg *pb.Config) {
	c.Lock()
	defer c.Unlock()
	for _, node := range cfg.Nodes {
		if node.Id == nodeId {
			node.ApiServerAddr = addr
			break
		}
	}
}

func (c *ConfigManager) AddOrUpdateSlots(slots []*pb.Slot, cfg *pb.Config) {
	c.Lock()
	defer c.Unlock()
	for i, slot := range slots {
		if c.existSlotByCfg(slot.Id, cfg) {
			cfg.Slots[i] = slot
			continue
		}
		cfg.Slots = append(cfg.Slots, slot)
	}
}

func (c *ConfigManager) SetNodeOnline(nodeId uint64, online bool, cfg *pb.Config) {
	c.Lock()
	defer c.Unlock()
	for _, node := range cfg.Nodes {
		if node.Id == nodeId {
			node.Online = online
			if !online {
				node.OfflineCount++
				node.LastOffline = time.Now().Unix()
			}
			break
		}
	}
}

func (c *ConfigManager) Close() {
	c.cfgFile.Close()
}

func (c *ConfigManager) Version() uint64 {
	c.Lock()
	defer c.Unlock()
	return c.cfg.Version
}

func (c *ConfigManager) existNode(nodeId uint64) bool {
	for _, node := range c.cfg.Nodes {
		if node.Id == nodeId {
			return true
		}
	}
	return false
}

func (c *ConfigManager) existNodeByCfg(nodeId uint64, cfg *pb.Config) bool {
	for _, node := range cfg.Nodes {
		if node.Id == nodeId {
			return true
		}
	}
	return false
}

func (c *ConfigManager) existSlot(slotId uint32) bool {
	for _, slot := range c.cfg.Slots {
		if slot.Id == slotId {
			return true
		}
	}
	return false
}

func (c *ConfigManager) existSlotByCfg(slotId uint32, cfg *pb.Config) bool {
	for _, slot := range cfg.Slots {
		if slot.Id == slotId {
			return true
		}
	}
	return false
}

func (c *ConfigManager) saveConfig() error {
	data := c.getConfigData()
	err := c.cfgFile.Truncate(0)
	if err != nil {
		return err
	}
	if _, err := c.cfgFile.WriteAt(data, 0); err != nil {
		return err
	}
	return nil
}

func (c *ConfigManager) SaveConfig() error {
	c.Lock()
	defer c.Unlock()
	return c.saveConfig()
}

func (c *ConfigManager) UpdateConfig(cfg *pb.Config) error {
	c.Lock()
	defer c.Unlock()
	c.cfg = cfg
	return c.saveConfig()
}

func (c *ConfigManager) GetConfigData() []byte {
	c.Lock()
	defer c.Unlock()
	return c.getConfigData()
}

func (c *ConfigManager) getConfigData() []byte {
	return []byte(wkutil.ToJSON(c.cfg))
}

func (c *ConfigManager) GetConfigDataByCfg(cfg *pb.Config) []byte {
	return []byte(wkutil.ToJSON(cfg))
}

func (c *ConfigManager) UnmarshalConfigData(data []byte, cfg *pb.Config) error {
	c.Lock()
	defer c.Unlock()
	return wkutil.ReadJSONByByte(data, cfg)
}

func (c *ConfigManager) initConfigFromFile() error {
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
		if err := wkutil.ReadJSONByByte(data, c.cfg); err != nil {
			c.Panic("Unmarshal cluster config failed!", zap.Error(err))
		}
	}
	return nil
}
