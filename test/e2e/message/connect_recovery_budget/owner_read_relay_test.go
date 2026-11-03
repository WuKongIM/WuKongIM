//go:build e2e

package connect_recovery_budget

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
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

// Full Begin/Page have no UID; the public exact Hash Slot is the selector.
func TestOwnerReadRelayFullGenerationWithoutUIDDelaysOnlyTargetOnce(t *testing.T) {
	uid := "relay-full-target"
	hash := routing.HashSlotForKey(uid, 256)
	begin := fixtureFullPresence(4, hash, "")
	page := fixtureFullPresence(5, hash, "")
	require.False(t, bytes.Contains(begin, []byte(uid)))
	require.False(t, bytes.Contains(page, []byte(uid)))
	frames := append(fixtureFrame(14, 1, begin, 1000), fixtureFrame(14, 2, page, 1000)...)
	frames = append(frames, fixtureFrame(13, 3, fixtureFullPresence(1, hash, uid), 3000)...)
	relay := newOwnerReadRelay()
	relay.arm(uid, time.Millisecond)
	var out partialRelayWriter
	require.NoError(t, relay.copy(context.Background(), 2, 1, &out, &oneByteRelayReader{Reader: bytes.NewReader(frames)}))
	require.Equal(t, frames, out.Bytes())
	got := relay.snapshot()
	require.Equal(t, uint64(2), got.OwnerReadRequests)
	require.Equal(t, uint64(1), got.DelaysStarted)
	require.Equal(t, uint64(1), got.DelayedFramesForwarded)
	require.Equal(t, uint64(1), got.RegisterRequests)
	require.Equal(t, uint32(1000), got.BudgetMillis)
	require.GreaterOrEqual(t, got.HoldNanos, int64(time.Millisecond))
	require.Zero(t, got.ActiveCopies)
}

func TestOwnerReadRelayFullGenerationRejectsUnrelatedSelectors(t *testing.T) {
	uid := "relay-full-negative"
	hash := routing.HashSlotForKey(uid, 256)
	future := fixtureFullPresence(4, hash, "")
	future[4] = 2
	invalidClaim := fixtureFullPresence(4, hash, "")
	// Twelve claim varints are required; a truncated prefix is not a selector.
	truncated := []byte{'W', 'K', 'V', 'G', 1, 4, 0x80}
	for _, tc := range []struct {
		name     string
		service  uint16
		from, to uint64
		body     []byte
	}{
		{"other_slot", 14, 2, 1, fixtureFullPresence(4, (hash+1)%256, "")},
		{"future_version", 14, 2, 1, future}, {"other_service", 13, 2, 1, fixtureFullPresence(4, hash, "")},
		{"end_not_read", 14, 2, 1, fixtureFullPresence(6, hash, "")}, {"truncated_claim", 14, 2, 1, truncated},
		{"same_node", 14, 1, 1, invalidClaim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := fixtureFrame(tc.service, 1, tc.body, 1000)
			relay := newOwnerReadRelay()
			relay.arm(uid, time.Millisecond)
			var out bytes.Buffer
			require.NoError(t, relay.copy(context.Background(), tc.from, tc.to, &out, bytes.NewReader(input)))
			require.Equal(t, input, out.Bytes())
			require.Zero(t, relay.snapshot().DelaysStarted)
			require.Zero(t, relay.snapshot().RegisterRequests)
			require.Zero(t, relay.snapshot().ActiveCopies)
		})
	}
}

func TestOwnerReadRelayFullGenerationCancellationJoinsDelayedWork(t *testing.T) {
	uid := "relay-full-cancel"
	input := fixtureFrame(14, 1, fixtureFullPresence(4, routing.HashSlotForKey(uid, 256), ""), 1000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelAfterRelayReader{Reader: bytes.NewReader(input), remaining: len(input), cancel: cancel}
	relay := newOwnerReadRelay()
	relay.arm(uid, 350*time.Millisecond)
	var out bytes.Buffer
	require.ErrorIs(t, relay.copy(ctx, 2, 1, &out, reader), context.Canceled)
	got := relay.snapshot()
	require.Equal(t, uint64(1), got.DelaysStarted)
	require.Equal(t, uint64(1), got.DelaysCanceled)
	require.Zero(t, got.ActiveCopies)
	require.Zero(t, out.Len())
}

// Independent wire fixture: F's seven scalars, committed claim's five scalars,
// owner cut, page controls, and the fixed empty DTO tail. Only Register has UID.
// This tests transparent matching; it is not a forged committed product claim.
func fixtureFullPresence(op byte, hash uint16, uid string) []byte {
	body := []byte{'W', 'K', 'V', 'G', 1, op}
	for _, v := range []uint64{uint64(hash), 2, 2, 3, 4, 5, 6, 10, 22, 12, 11, 12, 1, 21, 1, 1, 0, 0, 512, 65536} {
		body = binary.AppendUvarint(body, v)
	}
	body = binary.AppendUvarint(body, uint64(len(uid)))
	body = append(body, uid...)
	// Remaining empty route fields (10), route count (1), identity (4),
	// owner sequence (1), empty token (1), and UID count (1).
	return append(body, make([]byte, 18)...)
}
