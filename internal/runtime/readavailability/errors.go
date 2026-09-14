// Package readavailability classifies transient failures of authority-routed reads.
package readavailability

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// unavailableCauses is a closed set; absence, invalid input and unknown storage
// errors must not acquire retryability merely from their error text.
var unavailableCauses = []error{
	context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, net.ErrClosed,
	syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.EPIPE,
	transport.ErrStopped, transport.ErrTimeout, transport.ErrNodeNotFound,
	transport.ErrQueueFull, transport.ErrDialFailed, transport.ErrBusy,
	channel.ErrNotLeader, channel.ErrNotReady, channel.ErrStaleMeta,
	channel.ErrWriteFenced, channel.ErrNotReplica, channel.ErrBackpressured, channel.ErrClosed,
	cluster.ErrNotStarted, cluster.ErrStopping, cluster.ErrRouteNotReady,
	cluster.ErrNoSlotLeader, cluster.ErrNotLeader, cluster.ErrBackpressured,
	proxy.ErrNoLeader, proxy.ErrNotLeader, proxy.ErrReadStaleRoute,
	multiraft.ErrNotLeader, multiraft.ErrSlotClosed, multiraft.ErrRuntimeClosed,
	multiraft.ErrSlotBusy, multiraft.ErrProposalBackpressure, multiraft.ErrApplyBacklogHigh,
}

// Unavailable preserves typed local causes and recognizes exact known generic
// RPC messages. It does not parse arbitrary text or make retry/scheduling decisions.
func Unavailable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	for _, cause := range unavailableCauses {
		if errors.Is(err, cause) {
			return true
		}
	}
	var network *net.OpError
	if errors.As(err, &network) && network.Timeout() {
		return true
	}
	var remote transport.RemoteError
	if errors.As(err, &remote) && remote.Code == transport.RemoteErrorCodeGeneric {
		for _, cause := range unavailableCauses {
			if remote.Message == cause.Error() {
				return true
			}
		}
	}
	return false
}
