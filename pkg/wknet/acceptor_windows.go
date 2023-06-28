package wknet

import (
	"net"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Acceptor struct {
	reactorSubs []*ReactorSub
	eg          *Engine
	wklog.Log
	listen    *listener
	listenWS  *listener // websocket
	listenWSS *listener // websocket
}

func NewAcceptor(eg *Engine) *Acceptor {
	reactorSubs := make([]*ReactorSub, eg.options.SubReactorNum)
	for i := 0; i < eg.options.SubReactorNum; i++ {
		reactorSubs[i] = NewReactorSub(eg, i)
	}
	a := &Acceptor{
		eg:          eg,
		reactorSubs: reactorSubs,
		Log:         wklog.NewWKLog("Acceptor"),
	}
	return a
}

func (a *Acceptor) Start() error {
	return a.start()
}

func (a *Acceptor) Stop() error {
	err := a.listen.Close()
	if err != nil {
		a.Warn("listen.Close() failed", zap.Error(err))
	}
	err = a.listenWS.Close()
	if err != nil {
		a.Warn("listenWS.Close() failed", zap.Error(err))
	}
	err = a.listenWSS.Close()
	if err != nil {
		a.Warn("listenWSS.Close() failed", zap.Error(err))
	}
	for _, reactorSub := range a.reactorSubs {
		reactorSub.Stop()
	}
	return nil
}

func (a *Acceptor) tcpRealAddr() net.Addr {

	return a.listen.realAddr
}

func (a *Acceptor) wsRealAddr() net.Addr {
	return a.listenWS.realAddr
}

func (a *Acceptor) wssRealAddr() net.Addr {
	return a.listenWSS.realAddr
}

func (a *Acceptor) start() error {
	for _, reactorSub := range a.reactorSubs {
		reactorSub.Start()
	}
	var wg = &sync.WaitGroup{}
	wg.Add(1)
	if strings.TrimSpace(a.eg.options.WsAddr) != "" {
		wg.Add(1)
	}
	if strings.TrimSpace(a.eg.options.WssAddr) != "" {
		wg.Add(1)
	}
	go func() {
		err := a.initTCPListener(wg)
		if err != nil {
			panic(err)
		}
	}()

	if strings.TrimSpace(a.eg.options.WsAddr) != "" {
		go func() {
			err := a.initWSListener(wg)
			if err != nil {
				panic(err)
			}
		}()
	}
	if strings.TrimSpace(a.eg.options.WssAddr) != "" {
		go func() {
			err := a.initWSSListener(wg)
			if err != nil {
				panic(err)
			}
		}()
	}

	wg.Wait()
	return nil
}

func (a *Acceptor) initTCPListener(wg *sync.WaitGroup) error {
	// tcp
	a.listen = newListener(a.eg.options.Addr, a.eg.options)
	err := a.listen.init()
	if err != nil {
		return err
	}
	wg.Done()
	a.listen.Polling(func(fd NetFd) error {
		return a.acceptConn(fd, false, false)
	})
	return nil
}

func (a *Acceptor) initWSListener(wg *sync.WaitGroup) error {
	// ws
	a.listenWS = newListener(a.eg.options.WsAddr, a.eg.options)
	err := a.listenWS.init()
	if err != nil {
		return err
	}
	wg.Done()
	a.listenWS.Polling(func(fd NetFd) error {
		return a.acceptConn(fd, true, false)
	})
	return nil
}

func (a *Acceptor) initWSSListener(wg *sync.WaitGroup) error {
	// wss
	a.listenWSS = newListener(a.eg.options.WssAddr, a.eg.options)
	err := a.listenWSS.init()
	if err != nil {
		return err
	}
	wg.Done()
	a.listenWSS.Polling(func(fd NetFd) error {
		return a.acceptConn(fd, false, true)
	})
	return nil
}

func (a *Acceptor) acceptConn(connNetFd NetFd, ws bool, wss bool) error {
	var (
		conn Conn
		err  error
	)
	connFd := connNetFd.fd

	remoteAddr := connNetFd.conn.RemoteAddr()

	subReactor := a.reactorSubByConnFd(connFd)
	if wss {
		if conn, err = a.eg.eventHandler.OnNewWSSConn(a.eg.GenClientID(), connNetFd, a.wssRealAddr(), remoteAddr, a.eg, subReactor); err != nil {
			return err
		}
	} else if ws {
		if conn, err = a.eg.eventHandler.OnNewWSConn(a.eg.GenClientID(), connNetFd, a.wsRealAddr(), remoteAddr, a.eg, subReactor); err != nil {
			return err
		}
	} else {
		if conn, err = a.eg.eventHandler.OnNewConn(a.eg.GenClientID(), connNetFd, a.tcpRealAddr(), remoteAddr, a.eg, subReactor); err != nil {
			return err
		}
	}
	// add conn to sub reactor
	subReactor.AddConn(conn)
	// call on connect
	a.eg.eventHandler.OnConnect(conn)
	return nil
}

func (a *Acceptor) reactorSubByConnFd(connfd int) *ReactorSub {

	return a.reactorSubs[connfd%len(a.reactorSubs)]
}
