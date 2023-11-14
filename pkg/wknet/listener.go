//go:build linux || freebsd || dragonfly || netbsd || openbsd || darwin

package wknet

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/socket"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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

func createSocketOptions(opts *Options) []socket.Option {
    sockOpts := []socket.Option{
        {SetSockOpt: socket.SetNoDelay, Opt: 1},
        {SetSockOpt: socket.SetReuseAddr, Opt: 1},
    }

    // 根据 Options 结构体添加可选的 socket 选项
    if opts.SocketRecvBuffer > 0 {
        sockOpts = append(sockOpts, socket.Option{SetSockOpt: socket.SetRecvBuffer, Opt: opts.SocketRecvBuffer})
    }
    if opts.SocketSendBuffer > 0 {
        sockOpts = append(sockOpts, socket.Option{SetSockOpt: socket.SetSendBuffer, Opt: opts.SocketSendBuffer})
    }

    return sockOpts
}

func createTCPSocket(network, addr string, sockOpts []socket.Option) (int, interface{}, error) {
    if network == ProtocolWS || network == "wss" {
        return socket.TCPSocket(ProtocolTCP, addr, true, sockOpts...)
    }

    return socket.TCPSocket(network, addr, true, sockOpts...)
}

func (l *listener) initTCPListener(network, addr string) error {
    sockOpts := createSocketOptions(l.opts)

    var err error
    l.fd, _, err = createTCPSocket(network, addr, sockOpts)
    if err != nil {
        return err
    }

    realAddr, err := syscall.Getsockname(l.fd)
    if err != nil {
        return err
    }
    l.realAddr = parseSockaddr(realAddr)

    return err
}

func parseSockaddr(sa syscall.Sockaddr) net.Addr {
    switch addr := sa.(type) {
    case *syscall.SockaddrInet4:
        return &net.TCPAddr{IP: addr.Addr[0:], Port: addr.Port}
    case *syscall.SockaddrInet6:
        return &net.TCPAddr{IP: addr.Addr[0:], Port: addr.Port}
    default:
        errMsg := fmt.Sprintf("Unknown address type: %T", addr)
        wklog.Error(errMsg) 

        return nil, errors.New(errMsg)
    }

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

func (l *listener) Close() error {
	return syscall.Close(l.fd)
}
