package limnet

import (
	"errors"
	"fmt"
	"net"
	"strings"

	perrors "github.com/WuKongIM/WuKongIM/pkg/errors"
	"github.com/WuKongIM/WuKongIM/pkg/socket"
)

type listener struct {
	fd int

	eg            *Engine
	customAddr    string
	customNetwork string
	readAddr      net.Addr
}

func newListener(eg *Engine) *listener {
	return &listener{
		eg: eg,
	}
}

func (l *listener) init() error {
	network, addr, err := l.parseAddr(l.eg.options.Addr)
	if err != nil {
		return err
	}
	l.customNetwork = network
	l.customAddr = addr

	if strings.HasPrefix(network, "tcp") {
		return l.initTCPListener(network, addr)
	}
	return fmt.Errorf("unsupported network: %s", network)
}

func (l *listener) initTCPListener(network, addr string) error {
	var sockOpts = []socket.Option{
		{SetSockOpt: socket.SetNoDelay, Opt: 1},
	}
	opts := l.eg.options

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
	case "tcp", "tcp4", "tcp6":
		l.fd, l.readAddr, err = socket.TCPSocket(network, addr, true, sockOpts...)
	default:
		err = perrors.ErrUnsupportedProtocol
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
