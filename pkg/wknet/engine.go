package wknet

import (
	"net"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/atomic"
)

type Engine struct {
	connMatrix      *connMatrix              // 在线连接
	connsUnixLock   deadlock.RWMutex         // 在线连接锁
	options         *Options                 // 配置
	eventHandler    *EventHandler            // 事件
	reactorMain     *ReactorMain             // 主reactor
	timingWheel     *timingwheel.TimingWheel // Time wheel delay task
	defaultConnPool *sync.Pool               // 默认连接对象池
	clientIDGen     atomic.Int64             // 客户端ID生成器
}

func NewEngine(opts ...Option) *Engine {
	var (
		eg      *Engine
		options = NewOptions()
	)

	for _, opt := range opts {
		opt(options)
	}

	eg = &Engine{
		connMatrix:   newConnMatrix(),
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
	err := e.reactorMain.Stop()
	if err != nil {
		return err
	}
	return nil
}

func (e *Engine) AddConn(conn Conn) {
	e.connsUnixLock.Lock()
	e.connMatrix.addConn(conn)
	e.connsUnixLock.Unlock()
}

func (e *Engine) RemoveConn(conn Conn) {
	e.connsUnixLock.Lock()
	e.connMatrix.delConn(conn)
	e.connsUnixLock.Unlock()
}

func (e *Engine) GetConn(fd int) Conn {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	return e.connMatrix.getConn(fd)
}

func (e *Engine) GetAllConn() []Conn {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	conns := make([]Conn, 0, e.connMatrix.loadCount())
	e.connMatrix.iterate(func(conn Conn) bool {
		conns = append(conns, conn)
		return true
	})
	return conns
}

func (e *Engine) Iterator(f func(conn Conn) bool) {
	e.connsUnixLock.RLock()
	defer e.connsUnixLock.RUnlock()
	e.connMatrix.iterate(f)
}

func (e *Engine) ConnCount() int {
	return int(e.connMatrix.loadCount())
}

// Schedule 延迟任务
func (e *Engine) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return e.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

func (e *Engine) TCPRealListenAddr() net.Addr {
	return e.reactorMain.acceptor.tcpRealAddr()
}

func (e *Engine) WSRealListenAddr() net.Addr {
	return e.reactorMain.acceptor.wsRealAddr()
}
func (e *Engine) WSSRealListenAddr() net.Addr {
	return e.reactorMain.acceptor.wssRealAddr()
}

func (e *Engine) OnConnect(onConnect OnConnect) {
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

func (e *Engine) GenClientID() int64 {

	cid := e.clientIDGen.Load()

	if cid >= 1<<32-1 { // 如果超过或等于 int32最大值 这客户端ID从新从0开始生成，int32有几十亿大 如果从1开始生成再回到1 原来属于1的客户端应该早就销毁了。
		e.clientIDGen.Store(0)
	}
	return e.clientIDGen.Inc()
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
