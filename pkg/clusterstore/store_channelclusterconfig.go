package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
)

type ChannelClusterConfigStore struct {
	store *Store
}

func NewChannelClusterConfigStore(store *Store) *ChannelClusterConfigStore {
	return &ChannelClusterConfigStore{
		store: store,
	}
}

func (c *ChannelClusterConfigStore) Save(channelId string, channelType uint8, clusterCfg *cluster.ChannelClusterConfig) error {
	data, err := clusterCfg.Marshal()
	if err != nil {
		return err
	}
	return c.store.db.SaveChannelClusterConfig(channelId, channelType, data)
}

func (c *ChannelClusterConfigStore) Delete(channelId string, channelType uint8) error {
	return c.store.db.DeleteChannelClusterConfig(channelId, channelType)
}

func (c *ChannelClusterConfigStore) Get(channelId string, channelType uint8) (*cluster.ChannelClusterConfig, error) {
	cfgData, err := c.store.db.GetChannelClusterConfig(channelId, channelType)
	if err != nil {
		return nil, err
	}
	if cfgData == nil {
		return nil, nil
	}
	cfg := &cluster.ChannelClusterConfig{}
	err = cfg.Unmarshal(cfgData)
	return cfg, err
}

func (c *ChannelClusterConfigStore) GetCountWithSlotId(slotId uint32) (int, error) {
	return c.store.db.GetSlotChannelClusterConfigCount(slotId)
}

func (c *ChannelClusterConfigStore) GetWithSlotId(slotId uint32) ([]*cluster.ChannelClusterConfig, error) {
	cfgs, err := c.store.db.GetSlotChannelClusterConfig(slotId)
	if err != nil {
		return nil, err
	}
	var ret []*cluster.ChannelClusterConfig
	for _, cfgData := range cfgs {
		cfg := &cluster.ChannelClusterConfig{}
		err = cfg.Unmarshal(cfgData)
		if err != nil {
			return nil, err
		}
		ret = append(ret, cfg)
	}
	return ret, nil
}

func (c *ChannelClusterConfigStore) ProposeSave(channelId string, channelType uint8, clusterCfg *cluster.ChannelClusterConfig) error {
	data, err := clusterCfg.Marshal()
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDChannelClusterConfigSave, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return c.store.opts.Cluster.ProposeChannelMeta(channelId, channelType, cmdData)
}
