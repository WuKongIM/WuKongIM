package wkserver

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/ants/v2"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.uber.org/atomic"
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
	messagePool *ants.Pool
	reqIDGen    atomic.Uint64
	w           wait.Wait
	connManager *ConnManager
	metrics     *metrics

	timingWheel *timingwheel.TimingWheel

	requestObjPool *sync.Pool
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
		w:           wait.New(),
		connManager: NewConnManager(),
		metrics:     newMetrics(),
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		requestObjPool: &sync.Pool{
			New: func() any {

				return &proto.Request{}
			},
		},
	}
	requestPool, err := ants.NewPool(opts.RequestPoolSize, ants.WithPanicHandler(func(i interface{}) {
		s.Panic("request pool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		s.Panic("new request pool error", zap.Error(err))
	}
	s.requestPool = requestPool

	messagePool, err := ants.NewPool(opts.MessagePoolSize, ants.WithPanicHandler(func(i interface{}) {
		s.Panic("message pool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		s.Panic("new message pool error", zap.Error(err))
	}
	s.messagePool = messagePool

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

	return s
}

func (s *Server) Start() error {
	s.timingWheel.Start()
	s.engine.OnData(s.onData)
	s.engine.OnConnect(s.onConnect)
	s.engine.OnClose(s.onClose)

	s.Schedule(time.Minute*1, func() {
		s.metrics.printMetrics(fmt.Sprintf("Server:%s", s.opts.Addr))
	})
	return s.engine.Start()
}

func (s *Server) Stop() {
	s.timingWheel.Stop()
	err := s.engine.Stop()
	if err != nil {
		s.Warn("stop is error", zap.Error(err))
	}
}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
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

func (s *Server) RequestPoolRunning() int {
	return s.requestPool.Running()
}

func (s *Server) MessagePoolRunning() int {
	return s.messagePool.Running()
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
