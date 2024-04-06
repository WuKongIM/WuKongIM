package wkdb

func (wk *wukongDB) SaveChannelClusterConfig(channelId string, channelType uint8, channelClusterConfig ChannelClusterConfig) error {
	return nil
}

func (wk *wukongDB) GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, error) {
	return EmptyChannelClusterConfig, nil
}

func (wk *wukongDB) DeleteChannelClusterConfig(channelId string, channelType uint8) error {
	return nil
}

func (wk *wukongDB) GetChannelClusterConfigs(offsetId uint64, limit int) ([]ChannelClusterConfig, error) {
	return nil, nil
}

func (wk *wukongDB) GetChannelClusterConfigCountWithSlotId(slotId uint32) (int, error) {
	return 0, nil

}

func (wk *wukongDB) GetChannelClusterConfigWithSlotId(slotId uint32) ([]ChannelClusterConfig, error) {
	return nil, nil
}
