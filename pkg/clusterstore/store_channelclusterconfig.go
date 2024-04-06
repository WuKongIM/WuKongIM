package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

type ChannelClusterConfigStore struct {
	store *Store
}

func NewChannelClusterConfigStore(store *Store) *ChannelClusterConfigStore {
	return &ChannelClusterConfigStore{
		store: store,
	}
}

func (c *ChannelClusterConfigStore) Save(clusterCfg wkdb.ChannelClusterConfig) error {

	return c.store.wdb.SaveChannelClusterConfig(clusterCfg)
}

func (c *ChannelClusterConfigStore) Delete(channelId string, channelType uint8) error {
	return c.store.wdb.DeleteChannelClusterConfig(channelId, channelType)
}

func (c *ChannelClusterConfigStore) Get(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	return c.store.wdb.GetChannelClusterConfig(channelId, channelType)
}

func (c *ChannelClusterConfigStore) GetCountWithSlotId(slotId uint32) (int, error) {
	return c.store.wdb.GetChannelClusterConfigCountWithSlotId(slotId)
}

func (c *ChannelClusterConfigStore) GetAll(offsetId uint64, limit int) ([]wkdb.ChannelClusterConfig, error) {
	return c.store.wdb.GetChannelClusterConfigs(offsetId, limit)
}

func (c *ChannelClusterConfigStore) GetWithSlotId(slotId uint32) ([]wkdb.ChannelClusterConfig, error) {

	return c.store.wdb.GetChannelClusterConfigWithSlotId(slotId)
}
