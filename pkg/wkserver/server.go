package wkserver

import (
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/ants/v2"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.etcd.io/etcd/pkg/v3/wait"
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
	reqIDGen    *idutil.Generator
	w           wait.Wait
	connManager *ConnManager
}

func New(addr string, ops ...Option) *Server {
	opts := NewOptions()
	opts.Addr = addr
	if len(ops) > 0 {
		for _, op := range ops {
			op(opts)
		}
	}

	s := &Server{
		proto:       proto.New(),
		engine:      wknet.NewEngine(wknet.WithAddr(opts.Addr)),
		opts:        opts,
		routeMap:    make(map[string]Handler),
		Log:         wklog.NewWKLog("Server"),
		reqIDGen:    idutil.NewGenerator(0, time.Now()),
		w:           wait.New(),
		connManager: NewConnManager(),
	}
	requestPool, err := ants.NewPool(opts.RequestPoolSize)
	if err != nil {
		s.Panic("new request pool error", zap.Error(err))
	}
	s.routeMap[opts.ConnPath] = func(ctx *Context) {
		req := ctx.ConnReq()
		if req == nil {
			return
		}
		ctx.WriteConnack(&proto.Connack{
			Id:     req.Id,
			Status: proto.Status_OK,
		})

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

func (s *Server) Stop() {
	err := s.engine.Stop()
	if err != nil {
		s.Warn("stop is error", zap.Error(err))
	}
}

func (s *Server) Route(p string, h Handler) {
	s.routeMapLock.Lock()
	defer s.routeMapLock.Unlock()
	s.routeMap[p] = h
}

func (s *Server) OnMessage(h func(conn wknet.Conn, msg *proto.Message)) {
	s.opts.OnMessage = h
}

func (s *Server) Addr() net.Addr {
	return s.engine.TCPRealListenAddr()
}

func (s *Server) Options() *Options {
	return s.opts
}
