package eventbus

var Pusher *pushPlus

type IPusher interface {
}

type pushPlus struct {
	push IPusher
}

func newPushPlus(push IPusher) *pushPlus {
	return &pushPlus{
		push: push,
	}
}

func (p *pushPlus) AddEvents(events []*Event) {

}
