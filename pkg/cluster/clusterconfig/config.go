package clusterconfig

import (
	"io"
	"os"
	"sync"

	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/cpb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Config struct {
	cfg        *pb.Config // 未应用的配置
	appliedCfg *pb.Config // 已应用的配置（已经存盘了）
	cfgFile    *os.File
	opts       *Options
	wklog.Log
	mu sync.RWMutex
}

func NewConfig(opts *Options) *Config {
	cfg := &Config{
		cfg: &pb.Config{
			SlotCount: opts.SlotCount,
		},
		opts: opts,
		Log:  wklog.NewWKLog("cluster.config"),
	}

	err := cfg.loadConfigFromFile()
	if err != nil {
		cfg.Panic("Load cluster config from file failed!", zap.Error(err))
	}
	cfg.appliedCfg = cfg.cfg.Clone()

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
		if err := wkutil.ReadJSONByByte(data, c.cfg); err != nil {
			c.Panic("Unmarshal cluster config failed!", zap.Error(err))
		}
	}
	return nil
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

func (c *Config) apply(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	newCfg := &pb.Config{}
	err := newCfg.Unmarshal(data)
	if err != nil {
		return err
	}

	if newCfg.Version <= c.appliedCfg.Version {
		c.Warn("apply config version <= applied config version", zap.Uint64("version", newCfg.Version), zap.Uint64("appliedVersion", c.appliedCfg.Version))
		return nil
	}

	err = c.cfgFile.Truncate(0)
	if err != nil {
		return err
	}

	if _, err := c.cfgFile.WriteAt([]byte(wkutil.ToJSON(newCfg)), 0); err != nil {
		return err
	}
	c.appliedCfg = newCfg.Clone()
	if newCfg.Version > c.cfg.Version {
		c.cfg = newCfg.Clone()
	}
	return nil
}

func (c *Config) nodes() []*pb.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Nodes
}

func (c *Config) slots() []*pb.Slot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Slots
}

func (c *Config) addSlot(slot *pb.Slot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Slots = append(c.cfg.Slots, slot)
}

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

func (c *Config) addNode(node *pb.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Nodes = append(c.cfg.Nodes, node)
}

func (c *Config) appliedConfig() *pb.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.appliedCfg
}
