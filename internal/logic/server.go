package logic

import (
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
)

type Server struct {
	s    *wkserver.Server
	opts *Options
	wklog.Log
	nodeManager *nodeManager
	logicServer *LogicServer
}

func NewServer(addr string, opt ...Option) *Server {
	opts := NewOptions()
	opts.Addr = addr
	for _, o := range opt {
		o(opts)
	}
	logicServer := NewLogicServer()
	return &Server{
		opts:        opts,
		s:           wkserver.New(addr),
		Log:         wklog.NewWKLog("Logic"),
		nodeManager: newNodeManager(),
		logicServer: logicServer,
	}
}

func (s *Server) Start() error {
	err := s.s.Start()
	if err != nil {
		return err
	}
	s.initHandler()
	return nil
}
func (s *Server) Stop() {
	err := s.s.Stop()
	if err != nil {
		s.Warn(err.Error())
	}
}

func (s *Server) Addr() net.Addr {
	return s.s.Addr()
}
