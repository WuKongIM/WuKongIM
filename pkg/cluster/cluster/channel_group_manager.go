package cluster

import "time"

type ChannelGroupManager struct {
	channelGroups  []*ChannelGroup
	proposeTimeout time.Duration
}

func NewChannelGroupManager() *ChannelGroupManager {
	return &ChannelGroupManager{
		proposeTimeout: 5 * time.Second,
	}
}

func (c *ChannelGroupManager) ProposeMessage(channelID string, channelType uint8, data []byte) (uint64, error) {

	var (
		chGroup = c.channelGroup(channelID, channelType)
		channel = chGroup.Channel(channelID, channelType)
		err     error
	)
	if channel == nil {
		channel, err = c.loadOrCreateChannel(channelID, channelType)
		if err != nil {
			return 0, err
		}
		chGroup.Add(channel)
	}
	lastIndex, err := channel.ProposeAndWaitCommit(data, c.proposeTimeout)
	return lastIndex, err
}

func (c *ChannelGroupManager) Channel(channelID string, channelType uint8) (*Channel, error) {
	return nil, nil
}

func (c *ChannelGroupManager) channelGroup(channelID string, channelType uint8) *ChannelGroup {
	return nil
}

func (c *ChannelGroupManager) loadOrCreateChannel(channelID string, channelType uint8) (*Channel, error) {
	return nil, nil
}
