package limnet

import (
	"errors"
	"fmt"
	"os"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/limlog"
	"github.com/WuKongIM/WuKongIM/pkg/limnet/netpoll"
	"github.com/WuKongIM/WuKongIM/pkg/socket"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type Acceptor struct {
	reactorSubs  []*ReactorSub
	eg           *Engine
	listenPoller *netpoll.Poller

	listen *listener

	limlog.Log
}

func NewAcceptor(eg *Engine) *Acceptor {
	fmt.Println("NewAcceptor....", eg.options.SubReactorNum)
	reactorSubs := make([]*ReactorSub, eg.options.SubReactorNum)
	for i := 0; i < eg.options.SubReactorNum; i++ {
		reactorSubs[i] = NewReactorSub(eg, i)
	}
	a := &Acceptor{
		eg:           eg,
		reactorSubs:  reactorSubs,
		listenPoller: netpoll.NewPoller(),
		Log:          limlog.NewLIMLog("Acceptor"),
	}

	return a
}

func (a *Acceptor) Start() error {

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
	if conn, err = a.eg.eventHandler.OnNewConn(connFd, a.listen.readAddr, remoteAddr, a.eg, subReactor); err != nil {
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
