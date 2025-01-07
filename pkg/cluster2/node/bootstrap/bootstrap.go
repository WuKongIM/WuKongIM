package bootstrap

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/event"
)

type Bootstrap struct {
	// 分布式配置服务
	cfgServer *clusterconfig.Server

	// 分布式事件
	eventServer *event.Server
}

func New(clusterConfigOptions *clusterconfig.Options) *Bootstrap {

	b := &Bootstrap{}

	b.cfgServer = clusterconfig.New(clusterConfigOptions)
	b.eventServer = event.NewServer(clusterConfigOptions, b.cfgServer)

	return b
}

func (b *Bootstrap) Start() error {
	err := b.eventServer.Start()
	if err != nil {
		return err
	}

	err = b.cfgServer.Start()
	if err != nil {
		return err
	}

	return nil
}

func (b *Bootstrap) Stop() {
	b.eventServer.Stop()
	b.cfgServer.Stop()
}

func (b *Bootstrap) GetConfigServer() *clusterconfig.Server {
	return b.cfgServer
}
