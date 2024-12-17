package reactor

type IDiffuse interface {
}

type DiffusePlus struct {
	diffuse IDiffuse
}

func newDiffusePlus(d IDiffuse) *DiffusePlus {
	return &DiffusePlus{
		diffuse: d,
	}
}

func (d *DiffusePlus) Advance(fakeChannelId string, channelType uint8) {
}

func (d *DiffusePlus) AddMessage(message *ChannelMessage) {
}

func (d *DiffusePlus) AddMessages(messages []*ChannelMessage) {
}

func (d *DiffusePlus) AddMessageToOutbound(toNode uint64, message *ChannelMessage) {
}
