package readavailability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestUnavailablePreservesLocalAndRPCFailureIdentity(t *testing.T) {
	for _, cause := range []error{
		proxy.ErrReadStaleRoute, transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: proxy.ErrReadStaleRoute.Error()},
		io.EOF, context.DeadlineExceeded, transport.ErrDialFailed, channel.ErrStaleMeta,
		&net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}},
		transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: multiraft.ErrNotLeader.Error()},
	} {
		for _, err := range []error{cause, fmt.Errorf("read: %w", cause), errors.Join(errors.New("read failed"), cause)} {
			if !Unavailable(err) {
				t.Errorf("expected transient: %T %v", err, err)
			}
		}
	}
}

func TestUnavailableDoesNotGuessFromUnknownOrBusinessErrors(t *testing.T) {
	for _, err := range []error{
		nil, metadb.ErrStaleMeta, context.Canceled, channel.ErrChannelNotFound, channel.ErrInvalidConfig,
		errors.New("EOF"), errors.New("multiraft: not leader"), errors.New("disk corruption"),
		transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "disk corruption: multiraft: not leader"},
		transport.RemoteError{Code: transport.RemoteErrorCodeServiceNotFound, Message: multiraft.ErrNotLeader.Error()},
		errors.Join(context.Canceled, io.EOF),
	} {
		if Unavailable(err) {
			t.Errorf("unexpected retryable classification: %T %v", err, err)
		}
	}
}
