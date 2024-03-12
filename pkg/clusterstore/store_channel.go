package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
)

// AddSubscribers 添加订阅者
func (s *Store) AddSubscribers(channelID string, channelType uint8, subscribers []string) error {
	data := EncodeSubscribers(channelID, channelType, subscribers)
	cmd := NewCMD(CMDAddSubscribers, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

// RemoveSubscribers 移除订阅者
func (s *Store) RemoveSubscribers(channelID string, channelType uint8, subscribers []string) error {
	data := EncodeSubscribers(channelID, channelType, subscribers)
	cmd := NewCMD(CMDRemoveSubscribers, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) RemoveAllSubscriber(channelId string, channelType uint8) error {

	data := EncodeChannel(channelId, channelType)
	cmd := NewCMD(CMDRemoveAllSubscriber, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelId, channelType, cmdData)
}

func (s *Store) GetSubscribers(channelID string, channelType uint8) ([]string, error) {
	return s.db.GetSubscribers(channelID, channelType)
}

// AddOrUpdateChannel add or update channel
func (s *Store) AddOrUpdateChannel(channelInfo *wkstore.ChannelInfo) error {
	data := EncodeAddOrUpdateChannel(channelInfo)
	cmd := NewCMD(CMDAddOrUpdateChannel, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelInfo.ChannelID, channelInfo.ChannelType, cmdData)
}

func (s *Store) DeleteChannel(channelId string, channelType uint8) error {
	data := EncodeChannel(channelId, channelType)
	cmd := NewCMD(CMDDeleteChannel, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelId, channelType, cmdData)
}

func (s *Store) GetChannel(channelID string, channelType uint8) (*wkstore.ChannelInfo, error) {
	return s.db.GetChannel(channelID, channelType)
}

func (s *Store) ExistChannel(channelID string, channelType uint8) (bool, error) {
	return s.db.ExistChannel(channelID, channelType)
}

func (s *Store) AddDenylist(channelID string, channelType uint8, uids []string) error {
	data := EncodeSubscribers(channelID, channelType, uids)
	cmd := NewCMD(CMDAddDenylist, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)

}

func (s *Store) GetDenylist(channelID string, channelType uint8) ([]string, error) {
	return s.db.GetDenylist(channelID, channelType)
}

func (s *Store) RemoveAllDenylist(channelID string, channelType uint8) error {
	cmd := NewCMD(CMDRemoveAllDenylist, nil)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) RemoveDenylist(channelID string, channelType uint8, uids []string) error {
	data := EncodeSubscribers(channelID, channelType, uids)
	cmd := NewCMD(CMDRemoveDenylist, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) AddAllowlist(channelID string, channelType uint8, uids []string) error {
	data := EncodeSubscribers(channelID, channelType, uids)
	cmd := NewCMD(CMDAddAllowlist, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) GetAllowlist(channelID string, channelType uint8) ([]string, error) {
	return s.db.GetAllowlist(channelID, channelType)
}

func (s *Store) RemoveAllAllowlist(channelID string, channelType uint8) error {
	cmd := NewCMD(CMDRemoveAllAllowlist, nil)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) RemoveAllowlist(channelID string, channelType uint8, uids []string) error {
	data := EncodeSubscribers(channelID, channelType, uids)
	cmd := NewCMD(CMDRemoveAllowlist, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}

func (s *Store) DeleteChannelClusterConfig(channelID string, channelType uint8) error {
	cmd := NewCMD(CMDChannelClusterConfigDelete, nil)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.opts.Cluster.ProposeChannelMeta(channelID, channelType, cmdData)
}
