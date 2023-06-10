package wknet

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
)

type Engine struct {
	connsUnix     []Conn
	connsUnixLock sync.RWMutex
	options       *Options
	eventHandler  *EventHandler
	reactorMain   *ReactorMain
	timingWheel   *timingwheel.TimingWheel // Time wheel delay task

	defaultConnPool *sync.Pool
}

func NewEngine(opts ...Option) *Engine {
	var (
		eg      *Engine
		options = NewOptions()
	)
	if len(opts) > 0 {
		for _, opt := range opts {
			opt(options)
		}
	}

	eg = &Engine{
		connsUnix:    make([]Conn, options.MaxOpenFiles),
		options:      options,
		eventHandler: NewEventHandler(),
		timingWheel:  timingwheel.NewTimingWheel(time.Millisecond*10, 1000),
		defaultConnPool: &sync.Pool{
			New: func() any {
				return &DefaultConn{}
			},
		},
	}
	eg.reactorMain = NewReactorMain(eg)
	return eg
}

func (e *Engine) Start() error {
	e.timingWheel.Start()
	return e.reactorMain.Start()
}

func (e *Engine) Stop() error {
	e.timingWheel.Stop()
	return e.reactorMain.Stop()
}

func (e *Engine) AddConn(conn Conn) {
	e.connsUnixLock.Lock()
	e.connsUnix[conn.Fd()] = conn
	e.connsUnixLock.Unlock()
}

func (e *Engine) RemoveConn(conn Conn) {
	e.connsUnixLock.Lock()
	e.connsUnix[conn.Fd()] = nil
	e.connsUnixLock.Unlock()
}

func (e *Engine) GetConn(fd int) Conn {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	return e.connsUnix[fd]
}

func (e *Engine) GetAllConn() []Conn {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	conns := make([]Conn, 0, len(e.connsUnix))

	for _, conn := range e.connsUnix {
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (e *Engine) ConnCount() int {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	var count int
	for _, conn := range e.connsUnix {
		if conn != nil {
			count++
		}
	}
	return count
}

// Schedule 延迟任务
func (e *Engine) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return e.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

func (e *Engine) TCPRealListenAddr() net.Addr {
	return e.reactorMain.acceptor.listen.readAddr
}

func (e *Engine) WSRealListenAddrt() net.Addr {
	return e.reactorMain.acceptor.listenWS.readAddr
}

func (e *Engine) OnConnect(onConnect OnConnect) {
	fmt.Println("OnConnect.....")
	e.eventHandler.OnConnect = onConnect
}
func (e *Engine) OnData(onData OnData) {
	e.eventHandler.OnData = onData
}

func (e *Engine) OnClose(onClose OnClose) {
	e.eventHandler.OnClose = onClose
}

func (e *Engine) OnNewConn(onNewConn OnNewConn) {
	e.eventHandler.OnNewConn = onNewConn
}

func (e *Engine) OnNewInboundConn(onNewInboundConn OnNewInboundConn) {
	e.eventHandler.OnNewInboundConn = onNewInboundConn
}

func (e *Engine) OnNewOutboundConn(onNewOutboundConn OnNewOutboundConn) {
	e.eventHandler.OnNewOutboundConn = onNewOutboundConn
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
