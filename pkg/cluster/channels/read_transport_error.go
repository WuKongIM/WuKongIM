package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// readRPCTransportError preserves temporary dependency failures across a read
// RPC using the existing not-ready wire code. Classify while causes are typed;
// the receiver must never infer availability from arbitrary error text.
func readRPCTransportError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || rpcErrorCode(err) != rpcErrorUnknown {
		return err
	}
	// File reads and explicit corruption can also wrap EOF; they are not a
	// temporary loss of the authority connection.
	var file *os.PathError
	if errors.As(err, &file) || errors.Is(err, db.ErrCorruptValue) ||
		errors.Is(err, db.ErrCorruptState) || errors.Is(err, db.ErrChecksumMismatch) {
		return err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EPIPE),
		errors.Is(err, transport.ErrStopped), errors.Is(err, transport.ErrTimeout),
		errors.Is(err, transport.ErrNodeNotFound), errors.Is(err, transport.ErrQueueFull),
		errors.Is(err, transport.ErrDialFailed), errors.Is(err, transport.ErrBusy):
		return fmt.Errorf("%w: %w", ch.ErrNotReady, err)
	}
	var network *net.OpError
	if errors.As(err, &network) && network.Timeout() {
		return fmt.Errorf("%w: %w", ch.ErrNotReady, err)
	}
	return err
}
