package eventbus

func RegisterPusher(pusher IPusher) {
	Pusher = newPusherPlus(pusher)
}

var Pusher *pusherPlus

type IPusher interface {
	// AddEvent 添加事件
	AddEvent(id int, event *Event)
	// 随机获得一个handler id
	RandHandlerId() int
	// Advance 推进事件
	Advance(id int)
}

type pusherPlus struct {
	push IPusher
}

func newPusherPlus(push IPusher) *pusherPlus {
	return &pusherPlus{
		push: push,
	}
}

func (p *pusherPlus) AddEvents(events []*Event) int {
	id := p.push.RandHandlerId()
	for _, event := range events {
		p.push.AddEvent(id, event)
	}
	return id
}

func (p *pusherPlus) Advance(id int) {
	p.push.Advance(id)
}
