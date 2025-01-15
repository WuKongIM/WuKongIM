package wkserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"github.com/panjf2000/gnet/v2/pkg/errors"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Server struct {
	proto  proto.Protocol
	engine gnet.Engine
	gnet.BuiltinEventEngine
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
	stopper        *syncutil.Stopper
	batchRead      int // 连接进来数据后，每次数据读取批数，超过此次数后下次再读
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
		opts:        opts,
		routeMap:    make(map[string]Handler),
		Log:         wklog.NewWKLog("Server"),
		w:           wait.New(),
		stopper:     syncutil.NewStopper(),
		connManager: NewConnManager(),
		metrics:     newMetrics(),
		batchRead:   100,
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		requestObjPool: &sync.Pool{
			New: func() any {

				return &proto.Request{}
			},
		},
	}

	requestPool, err := ants.NewPool(opts.RequestPoolSize, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		s.Panic("request pool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		s.Panic("new request pool error", zap.Error(err))
	}
	s.requestPool = requestPool

	messagePool, err := ants.NewPool(opts.MessagePoolSize, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		s.Panic("message pool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		s.Panic("new message pool error", zap.Error(err))
	}
	s.messagePool = messagePool

	s.routeMap[opts.ConnPath] = func(ctx *Context) {
		req := ctx.ConnReq()
		ctx.WriteConnack(&proto.Connack{
			Id:     req.Id,
			Status: proto.StatusOK,
		})
	}

	return s
}

func (s *Server) Start() error {

	s.timingWheel.Start()

	s.Schedule(time.Minute*1, func() {
		s.metrics.printMetrics(fmt.Sprintf("Server:%s", s.opts.Addr))
	})

	go func() {
		err := gnet.Run(s, s.opts.Addr, gnet.WithTicker(true), gnet.WithReuseAddr(true))
		if err != nil {
			s.Panic("gnet run error", zap.Error(err))
		}
	}()

	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	s.timingWheel.Stop()
	timeCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	err := s.engine.Stop(timeCtx)
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

func (s *Server) OnMessage(h func(conn gnet.Conn, msg *proto.Message)) {
	s.opts.OnMessage = h
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

func GetUidFromContext(conn gnet.Conn) string {
	if conn.Context() == nil {
		return ""
	}
	ctx := conn.Context().(*connContext)
	return ctx.uid.Load()
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

func ParseProtoAddr(protoAddr string) (string, string, error) {
	protoAddr = strings.ToLower(protoAddr)
	if strings.Count(protoAddr, "://") != 1 {
		return "", "", errors.ErrInvalidNetworkAddress
	}
	pair := strings.SplitN(protoAddr, "://", 2)
	proto, addr := pair[0], pair[1]
	switch proto {
	case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6", "unix":
	default:
		return "", "", errors.ErrUnsupportedProtocol
	}
	if addr == "" {
		return "", "", errors.ErrInvalidNetworkAddress
	}
	return proto, addr, nil
}
