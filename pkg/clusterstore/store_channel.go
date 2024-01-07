package clusterstore

// AddSubscribers 添加订阅者
func (s *Store) AddSubscribers(channelID string, channelType uint8, subscribers []string) error {
	data := EncodeSubscribers(subscribers)
	cmd := NewCMD(CMDAddSubscribers, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeMetaToChannel(channelID, channelType, cmdData)
}

// RemoveSubscribers 移除订阅者
func (s *Store) RemoveSubscribers(channelID string, channelType uint8, subscribers []string) error {
	data := EncodeSubscribers(subscribers)
	cmd := NewCMD(CMDRemoveSubscribers, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeMetaToChannel(channelID, channelType, cmdData)
}

func (s *Store) GetSubscribers(channelID string, channelType uint8) ([]string, error) {
	return s.db.GetSubscribers(channelID, channelType)
}
