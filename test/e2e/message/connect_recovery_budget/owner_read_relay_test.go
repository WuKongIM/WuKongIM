//go:build e2e

package connect_recovery_budget

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
	"github.com/stretchr/testify/require"
)

// These fixed wire fixtures test relay transparency, not a valid product owner DTO.
func TestOwnerReadRelayPreservesTransportBytesAndDelaysOnlyOnce(t *testing.T) {
	uid := "relay-fixture-identity"
	owner := fixturePresence(11, uid)
	register := fixturePresence(1, uid)
	frames := append(fixtureFrame(14, 1, owner, 250), fixtureFrame(14, 2, owner, 250)...)
	frames = append(frames, fixtureFrame(13, 3, register, 0)...)
	metadata := wire.EncodeHeader(wire.Header{Kind: transport.FrameKindRPCRequest, Priority: transport.PriorityControl, ServiceID: wire.CapabilityServiceID, RequestID: 4, BodyLen: uint32(len(wire.CapabilityRequestBudgets))})
	frames = append(frames, metadata[:]...)
	frames = append(frames, []byte(wire.CapabilityRequestBudgets)...)
	cancel := wire.EncodeHeader(wire.Header{Kind: transport.FrameKindRPCCancel, Priority: transport.PriorityControl, ServiceID: 14, RequestID: 1})
	frames = append(frames, cancel[:]...)
	out := &partialRelayWriter{}
	relay := newOwnerReadRelay()
	relay.arm(uid, time.Millisecond)
	require.NoError(t, relay.copy(context.Background(), 2, 1, out, &oneByteRelayReader{Reader: bytes.NewReader(frames)}))
	require.True(t, bytes.Equal(out.Bytes(), frames), "relay changed original header/body bytes")
	s := relay.snapshot()
	require.Equal(t, uint64(2), s.OwnerReadRequests)
	require.Equal(t, uint64(1), s.DelaysStarted)
	require.Equal(t, uint64(1), s.DelayedFramesForwarded)
	require.Equal(t, uint64(1), s.RegisterRequests)
	require.Equal(t, uint64(1), s.CancelFrames)
	require.Equal(t, uint32(250), s.BudgetMillis)
	require.GreaterOrEqual(t, s.HoldNanos, int64(time.Millisecond))
	require.Zero(t, s.ActiveCopies)
}

func TestOwnerReadRelayCancellationDoesNotLeaveDelayedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := fixtureFrame(14, 1, fixturePresence(11, "relay-cancel-identity"), 250)
	reader := &cancelAfterRelayReader{Reader: bytes.NewReader(input), remaining: len(input), cancel: cancel}
	relay := newOwnerReadRelay()
	relay.arm("relay-cancel-identity", 350*time.Millisecond)
	var out bytes.Buffer
	err := relay.copy(ctx, 2, 1, &out, reader)
	require.ErrorIs(t, err, context.Canceled)
	s := relay.snapshot()
	require.Equal(t, uint64(1), s.DelaysStarted)
	require.Equal(t, uint64(1), s.DelaysCanceled)
	require.Zero(t, s.ActiveCopies)
	require.Zero(t, out.Len())
}

func TestOwnerReadRelayRejectsIncompleteOrOversizedFraming(t *testing.T) {
	tooLarge := wire.EncodeHeader(wire.Header{Kind: transport.FrameKindRPCRequest, Priority: transport.PriorityRPC, ServiceID: 14, RequestID: 1, BodyLen: 65537})
	for _, input := range [][]byte{{1, 2, 3}, fixtureFrame(14, 1, fixturePresence(11, "fixture"), 250)[:25], tooLarge[:]} {
		relay := newOwnerReadRelay()
		var out bytes.Buffer
		err := relay.copy(context.Background(), 2, 1, &out, bytes.NewReader(input))
		require.True(t, err != nil, "partial framing unexpectedly succeeded")
		require.NotEqual(t, "", relay.snapshot().FailureKind)
		require.Zero(t, relay.snapshot().ActiveCopies)
	}
}

func fixturePresence(op byte, uid string) []byte {
	body := []byte{'W', 'K', 'V', 'P', 2, op}
	body = binary.AppendUvarint(body, uint64(len(uid)))
	return append(body, uid...)
}

func fixtureFrame(service uint16, id uint64, body []byte, budget uint32) []byte {
	kind := transport.FrameKindRPCRequest
	if budget != 0 {
		kind = transport.FrameKindRPCBudgetRequest
	}
	header := wire.EncodeHeader(wire.Header{Kind: kind, Priority: transport.PriorityRPC, ServiceID: service, RequestID: id, BodyLen: uint32(len(body)), BudgetMillis: budget})
	out := append([]byte(nil), header[:]...)
	return append(out, body...)
}

type oneByteRelayReader struct{ io.Reader }

func (r *oneByteRelayReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

type partialRelayWriter struct{ bytes.Buffer }

func (w *partialRelayWriter) Write(p []byte) (int, error) {
	if len(p) > 2 {
		p = p[:2]
	}
	return w.Buffer.Write(p)
}

type cancelAfterRelayReader struct {
	io.Reader
	remaining int
	cancel    context.CancelFunc
}

func (r *cancelAfterRelayReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.remaining -= n
	if r.remaining == 0 {
		r.cancel()
	}
	return n, err
}
