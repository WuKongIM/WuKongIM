package wkserver

import (
	"net"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Server struct {
	proto        proto.Protocol
	engine       *wknet.Engine
	opts         *Options
	routeMapLock sync.RWMutex
	routeMap     map[string]Handler
	wklog.Log
	requestPool *ants.Pool
}

func New(addr string) *Server {
	opts := NewOptions()
	opts.Addr = addr

	s := &Server{
		proto:    proto.New(),
		engine:   wknet.NewEngine(wknet.WithAddr(opts.Addr)),
		opts:     opts,
		routeMap: make(map[string]Handler),
		Log:      wklog.NewWKLog("Server"),
	}
	requestPool, err := ants.NewPool(opts.RequestPoolSize)
	if err != nil {
		s.Panic("new request pool error", zap.Error(err))
	}
	s.requestPool = requestPool
	return s
}

func (s *Server) Start() error {
	s.engine.OnData(s.onData)
	s.engine.OnConnect(s.onConnect)
	s.engine.OnClose(s.onClose)
	return s.engine.Start()
}

func (s *Server) Stop() error {
	return s.engine.Stop()
}

func (s *Server) Route(p string, h Handler) {
	s.routeMapLock.Lock()
	defer s.routeMapLock.Unlock()
	s.routeMap[p] = h
}

func (s *Server) Addr() net.Addr {
	return s.engine.TCPRealListenAddr()
}
