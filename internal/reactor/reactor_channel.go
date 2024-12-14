package reactor

type IChannel interface {
}

type ChannelPlus struct {
	ch IChannel
}

func newChannelPlus(ch IChannel) *ChannelPlus {
	return &ChannelPlus{
		ch: ch,
	}
}

func (c *ChannelPlus) AddMessagesToInBound(channelId string, channelType uint8, messages []ChannelMessage) {
}
