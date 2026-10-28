//go:build integration

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

const admissionIsolationWatchdog = 5 * time.Second

// A blocked physical writer must not turn spare SEND admission capacity into a
// socket-completion wait. This is an ordering test, not a wall-time benchmark.
func TestClientAdmissionIsolationFromBlockedWriter(t *testing.T) {
	f := newAdmissionIsolationFixture(t, 2)
	f.conn.arm()
	first := f.awaitAdmission(t, f.trySend(admissionIsolationMessage(1)))
	if first.err != nil || first.future == nil {
		t.Fatalf("initial TrySendAsync = (%v, %v), want an admitted future", first.future, first.err)
	}
	f.awaitWriteEntry(t)

	second := f.awaitAdmission(t, f.trySend(admissionIsolationMessage(2)))
	if second.err != nil || second.future == nil {
		t.Fatalf("TrySendAsync with spare queue/inflight capacity = (%v, %v), want admission before writer release", second.future, second.err)
	}
	f.requireWriteStillBlocked(t)
	f.conn.releaseWrite()
	f.awaitSendack(t, first.future, 1)
	f.awaitSendack(t, second.future, 2)
	f.pingBarrier(t)
	f.requireWireOrder(t, []uint64{1, 2}, 0)
	t.Log("admission_isolation: second SEND admitted before socket release; both exact SENDACKs and final PING observed")
}

// Ping owns sendMu while its physical write is in progress. TrySendAsync must
// reject that contention before socket release and leave the same SEND reusable.
func TestClientAdmissionIsolationFromBlockedPing(t *testing.T) {
	f := newAdmissionIsolationFixture(t, 1)
	f.conn.arm()
	pingDone := make(chan error, 1)
	f.launch(func() { pingDone <- f.client.Ping(context.Background()) })
	f.awaitWriteEntry(t)

	msg := admissionIsolationMessage(1)
	rejected := f.awaitAdmission(t, f.trySend(msg))
	if !errors.Is(rejected.err, ErrSendQueueFull) || rejected.future != nil {
		t.Fatalf("TrySendAsync while Ping is blocked = (%v, %v), want nil future and ErrSendQueueFull", rejected.future, rejected.err)
	}
	f.requireWriteStillBlocked(t)
	f.conn.releaseWrite()
	select {
	case err := <-pingDone:
		if err != nil {
			t.Fatalf("Ping after writer release: %v", err)
		}
	case <-time.After(admissionIsolationWatchdog):
		t.Fatal("Ping did not join after writer release")
	}

	// With one inflight slot, accepting this exact identity checks both leaked
	// capacity and a pending entry left by the rejected call through public APIs.
	retried := f.awaitAdmission(t, f.trySend(msg))
	if retried.err != nil || retried.future == nil {
		t.Fatalf("reusing rejected identity = (%v, %v), want admission with MaxInflight=1", retried.future, retried.err)
	}
	f.awaitSendack(t, retried.future, 1)
	f.pingBarrier(t)
	f.requireWireOrder(t, []uint64{1}, 1)
	t.Log("admission_isolation: Ping contention rejected before socket release; exact identity reused with MaxInflight=1; no rejected SEND on wire before final PING")
}

type admissionIsolationResult struct {
	future *SendFuture
	err    error
}

// admissionIsolationConn gates one post-CONNECT Write. Deadlines deliberately
// do not open the gate: only the test's causal release or cleanup can do so.
type admissionIsolationConn struct {
	net.Conn
	armed       atomic.Bool
	entered     chan struct{}
	released    chan struct{}
	releaseOnce sync.Once
}

func (c *admissionIsolationConn) arm() { c.armed.Store(true) }

func (c *admissionIsolationConn) Write(p []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.released
	}
	return c.Conn.Write(p)
}

func (c *admissionIsolationConn) releaseWrite() {
	c.releaseOnce.Do(func() { close(c.released) })
}

func (c *admissionIsolationConn) Close() error {
	c.releaseWrite()
	return c.Conn.Close()
}

// admissionIsolationFixture owns a real SDK session, its WKProto peer, and
// every test caller. Closing both pipe ends unblocks I/O before joining callers.
type admissionIsolationFixture struct {
	client   *Client
	conn     *admissionIsolationConn
	peer     net.Conn
	wire     chan frame.Frame
	peerDone chan error
	jobs     []<-chan struct{}
}

func newAdmissionIsolationFixture(t *testing.T, maxInflight int) *admissionIsolationFixture {
	t.Helper()
	clientConn, peer := net.Pipe()
	f := &admissionIsolationFixture{
		conn: &admissionIsolationConn{
			Conn:     clientConn,
			entered:  make(chan struct{}),
			released: make(chan struct{}),
		},
		peer:     peer,
		wire:     make(chan frame.Frame, 8),
		peerDone: make(chan error, 1),
	}
	var err error
	f.client, err = New(Config{
		Addr:              "admission-isolation-pipe",
		Dialer:            fakeDialer{conn: f.conn},
		OperationTimeout:  30 * time.Second,
		AckTimeout:        30 * time.Second,
		SendQueueCapacity: 2,
		MaxInflight:       maxInflight,
		BatchMaxRecords:   1,
		BatchMaxWait:      -1,
	})
	if err != nil {
		_ = f.conn.Close()
		_ = peer.Close()
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { f.cleanup(t) })
	go func() { f.peerDone <- f.servePeer() }()
	ctx, cancel := context.WithTimeout(context.Background(), admissionIsolationWatchdog)
	defer cancel()
	if _, err := f.client.Connect(ctx, ConnectOptions{
		UID: "admission-isolation-user", DeviceID: "admission-isolation-device", DeviceFlag: frame.APP,
	}); err != nil {
		t.Fatalf("real CONNECT: %v", err)
	}
	return f
}

func (f *admissionIsolationFixture) launch(fn func()) {
	done := make(chan struct{})
	f.jobs = append(f.jobs, done)
	go func() {
		defer close(done)
		fn()
	}()
}

func (f *admissionIsolationFixture) trySend(msg Message) <-chan admissionIsolationResult {
	done := make(chan admissionIsolationResult, 1)
	f.launch(func() {
		future, err := f.client.TrySendAsync(msg)
		done <- admissionIsolationResult{future: future, err: err}
	})
	return done
}

func (f *admissionIsolationFixture) awaitAdmission(t *testing.T, done <-chan admissionIsolationResult) admissionIsolationResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(admissionIsolationWatchdog):
		t.Fatal("TrySendAsync did not return before the socket release barrier")
		return admissionIsolationResult{}
	}
}

func (f *admissionIsolationFixture) awaitWriteEntry(t *testing.T) {
	t.Helper()
	select {
	case <-f.conn.entered:
	case <-time.After(admissionIsolationWatchdog):
		t.Fatal("writer never reached the physical Write barrier")
	}
	f.requireWriteStillBlocked(t)
}

func (f *admissionIsolationFixture) requireWriteStillBlocked(t *testing.T) {
	t.Helper()
	select {
	case <-f.conn.released:
		t.Fatal("socket release preceded the admission assertion")
	default:
	}
}

func (f *admissionIsolationFixture) awaitSendack(t *testing.T, future *SendFuture, seq uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), admissionIsolationWatchdog)
	defer cancel()
	got, err := future.Wait(ctx)
	want := admissionIsolationMessage(seq)
	if err != nil || got.ClientSeq != want.ClientSeq || got.ClientMsgNo != want.ClientMsgNo || got.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("SENDACK = (%+v, %v), want exact successful identity %d/%s", got, err, want.ClientSeq, want.ClientMsgNo)
	}
}

func (f *admissionIsolationFixture) pingBarrier(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), admissionIsolationWatchdog)
	defer cancel()
	if err := f.client.Ping(ctx); err != nil {
		t.Fatalf("final PING write barrier: %v", err)
	}
}

func (f *admissionIsolationFixture) requireWireOrder(t *testing.T, sends []uint64, initialPings int) {
	t.Helper()
	for i := 0; i < initialPings+len(sends)+1; i++ {
		select {
		case got := <-f.wire:
			if i < initialPings || i == initialPings+len(sends) {
				if _, ok := got.(*frame.PingPacket); !ok {
					t.Fatalf("wire frame %d = %T, want PING", i, got)
				}
				continue
			}
			want := admissionIsolationMessage(sends[i-initialPings])
			send, ok := got.(*frame.SendPacket)
			if !ok || send.ClientSeq != want.ClientSeq || send.ClientMsgNo != want.ClientMsgNo {
				t.Fatalf("wire frame %d = %+v, want exact SEND identity %d/%s", i, got, want.ClientSeq, want.ClientMsgNo)
			}
		case <-time.After(admissionIsolationWatchdog):
			t.Fatalf("wire frame %d was not observed", i)
		}
	}
}

func admissionIsolationMessage(seq uint64) Message {
	return Message{
		ClientSeq: seq, ClientMsgNo: fmt.Sprintf("admission-isolation-%d", seq),
		ChannelID: "admission-isolation-channel", ChannelType: frame.ChannelTypeGroup,
		Payload: []byte("admission-isolation-payload"),
	}
}

func (f *admissionIsolationFixture) servePeer() error {
	defer f.peer.Close()
	proto := codec.New()
	packet, err := proto.DecodePacketWithConn(f.peer, frame.LatestVersion)
	if err != nil {
		return err
	}
	connect, ok := packet.(*frame.ConnectPacket)
	if !ok {
		return fmt.Errorf("peer received %T, want CONNECT", packet)
	}
	keys, publicKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
	if err != nil {
		return err
	}
	writeFrame := func(packet frame.Frame) error {
		data, err := proto.EncodeFrame(packet, frame.LatestVersion)
		if err != nil {
			return err
		}
		_, err = f.peer.Write(data)
		return err
	}
	if err := writeFrame(&frame.ConnackPacket{
		Framer:        frame.Framer{FrameType: frame.CONNACK, HasServerVersion: true},
		ServerVersion: frame.LatestVersion, ReasonCode: frame.ReasonSuccess,
		ServerKey: publicKey, Salt: string(keys.AESIV),
	}); err != nil {
		return err
	}
	for {
		packet, err := proto.DecodePacketWithConn(f.peer, frame.LatestVersion)
		if err != nil {
			return err
		}
		select {
		case f.wire <- packet:
		default:
			return errors.New("unexpected excess peer frames exceeded the bounded test recorder")
		}
		switch packet := packet.(type) {
		case *frame.PingPacket:
		case *frame.SendPacket:
			if err := writeFrame(&frame.SendackPacket{
				Framer: frame.Framer{FrameType: frame.SENDACK}, ClientSeq: packet.ClientSeq,
				ClientMsgNo: packet.ClientMsgNo, MessageID: int64(packet.ClientSeq),
				MessageSeq: packet.ClientSeq, ReasonCode: frame.ReasonSuccess,
			}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("peer received unexpected post-CONNECT frame %T", packet)
		}
	}
}

func (f *admissionIsolationFixture) cleanup(t *testing.T) {
	t.Helper()
	// Release external barriers before touching SDK locks, including on failure.
	f.conn.releaseWrite()
	_ = f.peer.Close()
	_ = f.conn.Close()
	_ = f.client.Close()
	for _, job := range f.jobs {
		admissionIsolationJoin(t, job, "test caller")
	}
	// These channels are cleanup ownership only; behavior uses the public APIs.
	if f.client.readerDone != nil {
		admissionIsolationJoin(t, f.client.readerDone, "SDK reader")
	}
	if f.client.writerDone != nil {
		admissionIsolationJoin(t, f.client.writerDone, "SDK writer")
	}
	select {
	case err := <-f.peerDone:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("peer failed: %v", err)
		}
	case <-time.After(admissionIsolationWatchdog):
		t.Error("peer did not join after both pipe endpoints closed")
	}
}

func admissionIsolationJoin(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(admissionIsolationWatchdog):
		t.Errorf("%s did not join during cleanup", name)
	}
}
