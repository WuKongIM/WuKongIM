package cluster

import "time"

type channelGroupManager struct {
	channelGroups  []*channelGroup
	proposeTimeout time.Duration
	opts           *Options
}

func newChannelGroupManager(opts *Options) *channelGroupManager {
	return &channelGroupManager{
		proposeTimeout: 5 * time.Second,
		opts:           opts,
	}
}

func (c *channelGroupManager) proposeMessage(channelID string, channelType uint8, data []byte) (uint64, error) {

	var (
		chGroup = c.channelGroup(channelID, channelType)
		channel = chGroup.channel(channelID, channelType)
		err     error
	)
	if channel == nil {
		channel, err = c.loadOrCreateChannel(channelID, channelType)
		if err != nil {
			return 0, err
		}
		chGroup.add(channel)
	}
	lastIndex, err := channel.proposeAndWaitCommit(data, c.proposeTimeout)
	return lastIndex, err
}

func (c *channelGroupManager) channel(channelID string, channelType uint8) (*channel, error) {
	return nil, nil
}

func (c *channelGroupManager) channelGroup(channelID string, channelType uint8) *channelGroup {
	return nil
}

func (c *channelGroupManager) loadOrCreateChannel(channelID string, channelType uint8) (*channel, error) {
	return nil, nil
}
