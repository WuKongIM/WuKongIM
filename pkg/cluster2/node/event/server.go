package event

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/lni/goutils/syncutil"
)

type Server struct {
	handler   *handler
	stopper   *syncutil.Stopper
	advanceC  chan struct{}
	cfgServer *clusterconfig.Server
}

func NewServer(cfgOptions *clusterconfig.Options, cfgServer *clusterconfig.Server) *Server {
	s := &Server{
		stopper:   syncutil.NewStopper(),
		advanceC:  make(chan struct{}, 1),
		cfgServer: cfgServer,
	}
	s.handler = newHandler(s, cfgOptions)
	return s
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.loopEvent)
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
}

func (s *Server) loopEvent() {
	tk := time.NewTicker(time.Millisecond * 200)
	defer tk.Stop()
	for {
		s.handleEvents()
		select {
		case <-tk.C:
		case <-s.advanceC:
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) handleEvents() {

	// 如果没有领导节点，则不处理事件
	if s.cfgServer.LeaderId() == 0 {
		return
	}

	if s.cfgServer.IsLeader() {
		// 分布式初始化
		if !s.cfgServer.IsInitialized() {
			s.handler.handleClusterInit()
			return
		}
	}

}
