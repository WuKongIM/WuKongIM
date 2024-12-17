package reactor

type IPush interface {
}

type PushPlus struct {
	push IPush
}

func newPushPlus(p IPush) *PushPlus {
	return &PushPlus{
		push: p,
	}
}

// 推送消息
func (p *PushPlus) PushMessage(message *ChannelMessage) {
}

// 推送离线消息
func (p *PushPlus) PushOfflineMessages(messages []*ChannelMessage) {

}
