package wknet

import "sync"

type Engine struct {
	connsUnix     []Conn
	connsUnixLock sync.RWMutex
	options       *Options
	eventHandler  *EventHandler
	reactorMain   *ReactorMain
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
	}
	eg.reactorMain = NewReactorMain(eg)
	return eg
}

func (e *Engine) Start() error {

	return e.reactorMain.Start()
}

func (e *Engine) Stop() error {

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
