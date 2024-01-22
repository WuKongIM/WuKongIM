package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
)

type Node struct {
	id     uint64
	addr   string
	client *client.Client
}
