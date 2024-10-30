//go:build linux || freebsd || dragonfly || netbsd || openbsd || darwin

package wknet

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/wknet/socket"
)

type listener struct {
	fd int

	customAddr    string
	customNetwork string
	realAddr      net.Addr
	addr          string // 监听地址 格式为 tcp://xxx.xxx.xxx.xxx:xxxx
	opts          *Options
}

func newListener(addr string, opts *Options) *listener {
	return &listener{
		addr: addr,
		opts: opts,
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
	var sockOpts = []socket.Option{
		{SetSockOpt: socket.SetNoDelay, Opt: 1},
		{SetSockOpt: socket.SetReuseAddr, Opt: 1}, // 监听端口重用
	}
	opts := l.opts

	if opts.SocketRecvBuffer > 0 {
		sockOpt := socket.Option{SetSockOpt: socket.SetRecvBuffer, Opt: opts.SocketRecvBuffer}
		sockOpts = append(sockOpts, sockOpt)
	}
	if opts.SocketSendBuffer > 0 {
		sockOpt := socket.Option{SetSockOpt: socket.SetSendBuffer, Opt: opts.SocketSendBuffer}
		sockOpts = append(sockOpts, sockOpt)
	}
	var (
		err error
	)

	switch network {
	case "ws", "wss":
		l.fd, _, err = socket.TCPSocket("tcp", addr, true, sockOpts...)
	case "tcp", "tcp4", "tcp6":
		l.fd, _, err = socket.TCPSocket(network, addr, true, sockOpts...)
	default:
		err = perrors.ErrUnsupportedProtocol
	}
	if err != nil {
		return err
	}

	var realAddr syscall.Sockaddr
	realAddr, err = syscall.Getsockname(l.fd)
	if err != nil {
		return err
	}
	switch addr := realAddr.(type) {
	case *syscall.SockaddrInet4:
		l.realAddr = &net.TCPAddr{IP: addr.Addr[0:], Port: addr.Port}
	case *syscall.SockaddrInet6:
		l.realAddr = &net.TCPAddr{IP: addr.Addr[0:], Port: addr.Port}
	default:
		fmt.Printf("Unknown address type: %T\n", addr)
	}
	return err
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

func (l *listener) Close() error {
	return syscall.Close(l.fd)
}
