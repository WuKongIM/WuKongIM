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

func (c *ChannelPlus) WakeIfNeed(channelId string, channelType uint8) {

}

func (c *ChannelPlus) AddMessages(channelId string, channelType uint8, messages []*ChannelMessage) {

}

func (c *ChannelPlus) AddMessage(channelId string, channelType uint8, message *ChannelMessage) {

}
