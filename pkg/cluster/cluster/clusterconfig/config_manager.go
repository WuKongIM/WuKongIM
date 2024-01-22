package clusterconfig

import (
	"io"
	"os"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type ConfigManager struct {
	cfg     *pb.Config
	cfgFile *os.File
	opts    *Options
	wklog.Log
}

func NewConfigManager(opts *Options) *ConfigManager {

	cm := &ConfigManager{
		cfg:  &pb.Config{},
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

	return cm
}

func (c *ConfigManager) GetConfig() *pb.Config {
	return c.cfg
}

func (c *ConfigManager) initConfigFromFile() error {
	clusterCfgPath := c.opts.ConfigPath
	var err error
	c.cfgFile, err = os.OpenFile(clusterCfgPath, os.O_RDONLY|os.O_CREATE, os.ModePerm)
	if err != nil {
		c.Panic("Open cluster config file failed!", zap.Error(err))
	}
	defer c.cfgFile.Close()

	data, err := io.ReadAll(c.cfgFile)
	if err != nil {
		c.Panic("Read cluster config file failed!", zap.Error(err))
	}
	if len(data) > 0 {
		if err := c.cfg.Unmarshal(data); err != nil {
			c.Panic("Unmarshal cluster config failed!", zap.Error(err))
		}
	}
	return nil
}
