package reactor

type IDiffuse interface {
	// AddAction 添加action，返回是否添加成功
	AddAction(action DiffuseAction) bool
	MustAddAction(a DiffuseAction)
	// Advance 推进，让频道立即执行下一个动作
	Advance(workerId int)
}

type DiffusePlus struct {
	diffuse IDiffuse
}

func newDiffusePlus(d IDiffuse) *DiffusePlus {
	return &DiffusePlus{
		diffuse: d,
	}
}

func (d *DiffusePlus) Advance(workerId int) {
	d.diffuse.Advance(workerId)
}

func (d *DiffusePlus) AddMessage(message *ChannelMessage) {
	d.diffuse.MustAddAction(DiffuseAction{
		Type: DiffuseActionInboundAdd,
		Messages: []*ChannelMessage{
			message,
		},
	})
}

func (d *DiffusePlus) AddMessages(messages []*ChannelMessage) {
	d.diffuse.MustAddAction(DiffuseAction{
		Type:     DiffuseActionInboundAdd,
		Messages: messages,
	})
}

func (d *DiffusePlus) AddMessageToOutbound(message *ChannelMessage) {
	d.diffuse.MustAddAction(DiffuseAction{
		Type:     DiffuseActionOutboundAdd,
		Messages: []*ChannelMessage{message},
	})
}
