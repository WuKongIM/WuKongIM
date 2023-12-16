package cluster

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
)

type Node struct {
	opts *Options
	wklog.Log
	stopper *syncutil.Stopper
}

func NewNode(id NodeID, addr string, optList ...Option) *Node {

	lg := wklog.NewWKLog(fmt.Sprintf("Node[%d]", id))

	defaultOpts := NewOptions()
	defaultOpts.ID = id
	defaultOpts.Addr = addr
	if len(optList) > 0 {
		for _, opt := range optList {
			opt(defaultOpts)
		}
	}

	return &Node{
		opts:    defaultOpts,
		Log:     lg,
		stopper: syncutil.NewStopper(),
	}
}

func (n *Node) Start(opts ...NodeStartOption) error {

	return nil
}

func (n *Node) Stop() {
}

func (n *Node) AddReplica(nodeID NodeID, raftAddr string) error {

	return nil
}

func (n *Node) RemoveReplica(nodeID NodeID) error {

	return nil
}

func (n *Node) AddSlot(slotID SlotID) (*Slot, error) {

	return nil, nil
}

func (n *Node) WaitLeader(timeout time.Duration) error {

	return nil
}
func (n *Node) MustWaitLeader(timeout time.Duration) {

}
