package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"

type Server struct {
	opts               *Options
	clusterEventServer *clusterevent.Server
}

func New(opts *Options) *Server {

	s := &Server{
		opts: opts,
	}
	s.clusterEventServer = clusterevent.New(clusterevent.NewOptions(
		clusterevent.WithNodeId(opts.NodeId),
		clusterevent.WithInitNodes(opts.InitNodes),
		clusterevent.WithSlotCount(opts.SlotCount),
		clusterevent.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterevent.WithReady(s.onEvent),
	))
	return s
}

func (s *Server) Start() error {

	return s.clusterEventServer.Start()
}

func (s *Server) Stop() {
	s.clusterEventServer.Stop()
}

func (s *Server) onEvent(msgs []clusterevent.Message) {

}
