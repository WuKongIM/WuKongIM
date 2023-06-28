//go:build windows
// +build windows

package wknet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

type PollEvent int

const (
	PollEventUnknown PollEvent = 1 << iota // 未知事件
	PollEventAccept                        // 接受
	PollEventRead                          // 读事件
	PollEventWrite                         // 写事件
	PollEventClose                         // 错误事件
)

type listener struct {
	customAddr    string
	customNetwork string
	realAddr      net.Addr
	addr          string // 监听地址 格式为 tcp://xxx.xxx.xxx.xxx:xxxx
	opts          *Options
	ln            net.Listener
	wklog.Log
}

func newListener(addr string, opts *Options) *listener {
	return &listener{
		addr: addr,
		opts: opts,
		Log:  wklog.NewWKLog("listener"),
	}
}

func (l *listener) init() error {
	network, addr, err := l.parseAddr(l.addr)
	if err != nil {
		return err
	}
	l.customNetwork = network
	l.customAddr = addr

	if strings.HasPrefix(network, "tcp") || strings.HasPrefix(network, "ws") {
		return l.initTCPListener(network, addr)
	}
	return fmt.Errorf("unsupported network: %s", network)
}

func (l *listener) initTCPListener(network, addr string) error {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {

				if l.opts.SocketRecvBuffer > 0 {
					_ = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_RCVBUF, l.opts.SocketRecvBuffer)
				}
				if l.opts.SocketSendBuffer > 0 {
					_ = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_SNDBUF, l.opts.SocketSendBuffer)
				}
			})
		},
		KeepAlive: l.opts.TCPKeepAlive,
	}
	var err error
	switch network {
	case "ws", "wss":
		l.ln, err = lc.Listen(context.Background(), "tcp", addr)
	case "tcp", "tcp4", "tcp6":
		l.ln, err = lc.Listen(context.Background(), network, addr)
	default:
		err = perrors.ErrUnsupportedProtocol
	}
	if err != nil {
		return err
	}
	l.realAddr = l.ln.Addr()
	return nil
}

// addr format: tcp://xx.xxx.xx.xx:xxxx split
func (l *listener) parseAddr(addr string) (network, address string, err error) {
	if addr == "" {
		return "", "", errors.New("empty address")
	}
	if !strings.Contains(addr, "://") {
		return "", "", errors.New("invalid address")
	}
	parts := strings.SplitN(addr, "://", 2)
	if len(parts) != 2 {
		return "", "", errors.New("invalid address")
	}
	return parts[0], parts[1], nil
}

func (l *listener) Polling(callback func(fd NetFd) error) {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			continue
		}
		nfd := newNetFd(conn)
		err = callback(nfd)
		if err != nil {
			l.Error("polling error: %v", zap.Error(err))
		}
	}
}

func (l *listener) Close() error {
	return nil

}
