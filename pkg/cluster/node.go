package cluster

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
)

type node struct {
	id     uint64
	addr   string
	client *client.Client
}

func newNode(id uint64, addr string) *node {
	cli := client.New(addr, client.WithUID(strconv.FormatUint(id, 10)))
	return &node{
		id:     id,
		addr:   addr,
		client: cli,
	}
}

func (n *node) start() {
	n.client.Start()
}

func (n *node) stop() {
	n.client.Close()
}
