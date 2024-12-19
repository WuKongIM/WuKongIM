package reactor

type IPush interface {
	// AddAction 添加action，返回是否添加成功
	AddAction(action PushAction) bool
	MustAddAction(a PushAction)
	// Advance 推进，让频道立即执行下一个动作
	Advance(workerId int)
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
	p.push.MustAddAction(PushAction{
		Type:     PushActionInboundAdd,
		Messages: []*ChannelMessage{message},
	})
}

func (p *PushPlus) PushMessages(messages []*ChannelMessage) {
	p.push.MustAddAction(PushAction{
		Type:     PushActionInboundAdd,
		Messages: messages,
	})
}

func (p *PushPlus) Advance(workerId int) {
	p.push.Advance(workerId)
}
