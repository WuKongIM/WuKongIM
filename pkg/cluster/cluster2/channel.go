package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

var _ reactor.IHandler = &channel{}

type channel struct {
	channelKey string
}

func newChannel(channelId string, channelType uint8) *channel {
	return &channel{}
}

// --------------------------IHandler-------------------------------

func (c *channel) LastLogIndexAndTerm() (uint64, uint32) {
	return 0, 0
}

func (c *channel) Ready() replica.Ready {
	return replica.Ready{}
}

func (c *channel) GetAndMergeLogs(msg replica.Message) ([]replica.Log, error) {
	return nil, nil
}

func (c *channel) AppendLog(logs []replica.Log) error {
	return nil
}

func (c *channel) ApplyLog(logs []replica.Log) error {
	return nil
}

func (c *channel) SlowDown() {

}

func (c *channel) SetHardState(hd replica.HardState) {

}

func (c *channel) Tick() {

}

func (c *channel) Step(m replica.Message) error {
	return nil
}

func (c *channel) SetLastIndex(index uint64) error {
	return nil
}

func (c *channel) SetAppliedIndex(index uint64) error {
	return nil
}

func (c *channel) Send(m replica.Message) {

}

func (c *channel) IsPrepared() bool {
	return false
}

func (c *channel) Logs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	return nil, nil
}
