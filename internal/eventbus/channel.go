package eventbus

func RegisterChannel(channel IChannel) {
	Channel = newChannelPlus(channel)
}

var Channel *channelPlus

type IChannel interface {
	// AddEvent 添加事件
	AddEvent(channelId string, channelType uint8, event *Event)
	// Advance 推进事件（让事件池不需要等待直接执行下一轮事件）
	Advance(channelId string, channelType uint8)
}

type channelPlus struct {
	channel IChannel
}

func newChannelPlus(channel IChannel) *channelPlus {
	return &channelPlus{
		channel: channel,
	}
}

// AddEvent 添加事件
func (c *channelPlus) AddEvent(channelId string, channelType uint8, event *Event) {
	c.channel.AddEvent(channelId, channelType, event)
}

// AddEvents 添加事件
func (c *channelPlus) AddEvents(channelId string, channelType uint8, events []*Event) {
	for _, event := range events {
		c.channel.AddEvent(channelId, channelType, event)
	}
}

// Advance 无等待快速推进事件
func (c *channelPlus) Advance(channelId string, channelType uint8) {
	c.channel.Advance(channelId, channelType)
}

// SendMessage 发送消息
func (c *channelPlus) SendMessage(channelId string, channelType uint8, event *Event) {
	c.channel.AddEvent(channelId, channelType, event)
}
