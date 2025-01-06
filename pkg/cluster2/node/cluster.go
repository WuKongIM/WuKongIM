package node

import "github.com/WuKongIM/WuKongIM/pkg/raft/raft"

type Node struct {
	opts     *Options
	raftNode *raft.Raft
}

func New(opts *Options) *Node {
	n := &Node{
		opts: opts,
	}

	n.raftNode = raft.New(raft.NewOptions(raft.WithNodeId(opts.NodeId)))

	return n
}

func (n *Node) Start() error {
	return n.raftNode.Start()
}

func (n *Node) Stop() {
	n.raftNode.Stop()
}

func (n *Node) AddEvent(e Event) {

}

func (n *Node) Listener(f func(e Event)) {

}
