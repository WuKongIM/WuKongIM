package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

// The serving Channel leader can lose its connection to the Slot authority.
// Exercise that nested failure through the registered read RPC and its codec.
func TestForwardedReadPreservesUnavailableTransport(t *testing.T) {
	for _, cause := range []error{
		&net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}},
		io.EOF, context.DeadlineExceeded,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			network := clusternet.NewLocalNetwork()
			client := NewTransportClient(network)
			leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2,
				MetaSource: &errMetaSource{err: fmt.Errorf("metadata read: %w", cause)}, Store: channelstore.NewMemoryFactory()})
			require.NoError(t, err)
			RegisterServiceHandlers(network, 2, leader)
			id := ch.ChannelID{ID: "read-unavailable", Type: 2}
			for _, persisted := range []bool{false, true} {
				response, err := client.ForwardCommittedReads(context.Background(), 2, CommittedReadsRequest{
					Persisted: persisted, Items: []CommittedReadRequest{{CommittedRead: committedReadContract(id), ExpectedLeader: 2}},
				})
				require.NoError(t, err)
				require.Len(t, response.Items, 1)
				require.ErrorIs(t, response.Items[0].Err, ch.ErrNotReady)
				require.Empty(t, response.Items[0].Read.Messages)
				require.Zero(t, response.Items[0].Read.NextSeq)
			}
			heads, err := client.ForwardConversationHeads(context.Background(), 2, ConversationHeadsRequest{
				UID: "u", Items: []ConversationHeadRequest{{ChannelID: id, ExpectedLeader: 2}},
			})
			require.NoError(t, err)
			require.Len(t, heads.Items, 1)
			require.ErrorIs(t, heads.Items[0].Err, ch.ErrNotReady)
		})
	}
}

func TestReadRPCErrorDoesNotGuessOrChangeAppendErrors(t *testing.T) {
	for _, cause := range []error{errors.New("read tcp: connection reset by peer"), errors.New("disk corruption"), context.Canceled, errors.Join(context.Canceled, io.EOF), ch.ErrInvalidConfig,
		&os.PathError{Op: "read", Path: "table.sst", Err: io.ErrUnexpectedEOF},
		errors.Join(db.ErrCorruptValue, io.ErrUnexpectedEOF), errors.Join(db.ErrCorruptState, io.EOF), errors.Join(db.ErrChecksumMismatch, io.EOF),
		transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "read tcp: connection reset by peer"}} {
		wire, err := encodeRPCResult(kindCommittedReadsResponse, nil, cause)
		require.NoError(t, err)
		_, err = decodeCommittedReadsResponse(wire)
		require.Error(t, err)
		require.NotErrorIs(t, err, ch.ErrNotReady)
	}
	wire, err := encodeRPCResult(kindAppendResponse, nil, syscall.ECONNRESET)
	require.NoError(t, err)
	_, err = decodeAppendResponse(wire)
	require.NotErrorIs(t, err, ch.ErrNotReady)
}

func TestReadRPCWholeFailurePreservesTransportAvailability(t *testing.T) {
	for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed,
		syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.EPIPE, context.DeadlineExceeded,
		transport.ErrStopped, transport.ErrTimeout, transport.ErrNodeNotFound,
		transport.ErrQueueFull, transport.ErrDialFailed, transport.ErrBusy} {
		for _, kind := range []uint8{kindLastVisibleResponse, kindConversationHeadsResponse, kindCommittedReadsResponse} {
			wire, err := encodeRPCResult(kind, nil, fmt.Errorf("dependency: %w", cause))
			require.NoError(t, err)
			require.ErrorIs(t, decodeRPCResult(wire, kind, nil), ch.ErrNotReady)
		}
	}
}
