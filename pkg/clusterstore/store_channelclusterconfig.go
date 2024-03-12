package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
)

type ChannelClusterConfigStore struct {
	store *Store
}

func NewChannelClusterConfigStore(store *Store) *ChannelClusterConfigStore {
	return &ChannelClusterConfigStore{
		store: store,
	}
}

func (c *ChannelClusterConfigStore) Save(channelId string, channelType uint8, clusterCfg *wkstore.ChannelClusterConfig) error {

	return c.store.db.SaveChannelClusterConfig(channelId, channelType, clusterCfg)
}

func (c *ChannelClusterConfigStore) Delete(channelId string, channelType uint8) error {
	return c.store.db.DeleteChannelClusterConfig(channelId, channelType)
}

func (c *ChannelClusterConfigStore) Get(channelId string, channelType uint8) (*wkstore.ChannelClusterConfig, error) {

	return c.store.db.GetChannelClusterConfig(channelId, channelType)
}

func (c *ChannelClusterConfigStore) GetCountWithSlotId(slotId uint32) (int, error) {
	return c.store.db.GetSlotChannelClusterConfigCount(slotId)
}

func (c *ChannelClusterConfigStore) GetWithSlotId(slotId uint32) ([]*wkstore.ChannelClusterConfig, error) {

	return c.store.db.GetSlotChannelClusterConfig(slotId)
}

func (c *ChannelClusterConfigStore) GetWithAllSlot() ([]*wkstore.ChannelClusterConfig, error) {
	return c.store.db.GetSlotChannelClusterConfigWithAllSlot()
}

func (c *ChannelClusterConfigStore) ProposeSave(channelId string, channelType uint8, clusterCfg *wkstore.ChannelClusterConfig) error {
	cfgData, err := clusterCfg.Marshal()
	if err != nil {
		return err
	}

	data, err := EncodeCMDChannelClusterConfigSave(channelId, channelType, cfgData)
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
