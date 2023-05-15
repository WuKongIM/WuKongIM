package wknet

import (
	"errors"
	"os"

	"go.uber.org/atomic"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/socket"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet/netpoll"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type Acceptor struct {
	reactorSubs  []*ReactorSub
	eg           *Engine
	listenPoller *netpoll.Poller
	clientIDGen  atomic.Int64 // 客户端ID生成
	listen       *listener

	wklog.Log
}

func NewAcceptor(eg *Engine) *Acceptor {
	reactorSubs := make([]*ReactorSub, eg.options.SubReactorNum)
	for i := 0; i < eg.options.SubReactorNum; i++ {
		reactorSubs[i] = NewReactorSub(eg, i)
	}
	a := &Acceptor{
		eg:           eg,
		reactorSubs:  reactorSubs,
		listenPoller: netpoll.NewPoller(),
		Log:          wklog.NewWKLog("Acceptor"),
	}

	return a
}

func (a *Acceptor) Start() error {

	go func() {
		err := a.run()
		if err != nil {
			panic(err)
		}
	}()

	return nil
}

func (a *Acceptor) run() error {

	for _, reactorSub := range a.reactorSubs {
		reactorSub.Start()
	}
	a.listen = newListener(a.eg)

	err := a.listen.init()
	if err != nil {
		panic(err)
	}

	if err := a.listenPoller.AddRead(a.listen.fd); err != nil {
		return errors.New("add listener fd to poller failed")
	}

	a.listenPoller.Polling(func(fd int, ev netpoll.PollEvent) error {

		return a.acceptConn(fd)
	})

	return nil
}
func (a *Acceptor) Stop() error {
	err := a.listenPoller.Close()
	if err != nil {
		a.Warn("listenPoller.Close() failed", zap.Error(err))
	}
	for _, reactorSub := range a.reactorSubs {
		reactorSub.Stop()
	}
	return nil
}

func (a *Acceptor) acceptConn(listenFd int) error {
	var (
		conn Conn
		err  error
	)
	connFd, sa, err := unix.Accept(listenFd)
	if err != nil {
		if err == unix.EAGAIN {
			return nil
		}
		a.Error("Accept() failed", zap.Error(err))
		return perrors.ErrAcceptSocket
	}
	if err = os.NewSyscallError("fcntl nonblock", unix.SetNonblock(connFd, true)); err != nil {
		return err
	}
	remoteAddr := socket.SockaddrToTCPOrUnixAddr(sa)
	if a.eg.options.TCPKeepAlive > 0 && a.listen.customNetwork == "tcp" {
		err = socket.SetKeepAlivePeriod(connFd, int(a.eg.options.TCPKeepAlive.Seconds()))
		a.Error("SetKeepAlivePeriod() failed", zap.Error(err))
	}

	subReactor := a.reactorSubByConnFd(connFd)
	if conn, err = a.eg.eventHandler.OnNewConn(a.GenClientID(), connFd, a.listen.readAddr, remoteAddr, a.eg, subReactor); err != nil {
		return err
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

func (a *Acceptor) GenClientID() int64 {

	cid := a.clientIDGen.Load()

	if cid >= 1<<32-1 { // 如果超过或等于 int32最大值 这客户端ID从新从0开始生成，int32有几十亿大 如果从1开始生成再回到1 原来属于1的客户端应该早就销毁了。
		a.clientIDGen.Store(0)
	}
	return a.clientIDGen.Inc()
}
