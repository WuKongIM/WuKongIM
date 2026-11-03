//go:build integration

package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// These bounded synthetic transports exercise the real pumps and codec. They
// isolate transport-buffer ownership, not CONNECT, encryption, or ACK admission;
// the existing real TCP and encrypted-session suites cover those boundaries.
func TestWriterPumpSteadyStateAllocationOverhead(t *testing.T) {
	c := newPumpTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startPumpTestWriter(t, c)
	result := make(chan error, 1)
	req := pumpTestSend(t, 1, discardConn{}, result)
	batch := []writeRequest{req}
	direct := testing.AllocsPerRun(100, func() {
		if _, err := c.writeBatch(batch); err != nil {
			t.Fatal(err)
		}
	})
	pumped := testing.AllocsPerRun(100, func() {
		pumpTestSubmit(t, ctx, c, req)
	})
	t.Logf("direct writeBatch %.2f allocs/op; real writer pump %.2f allocs/op", direct, pumped)
	if overhead := pumped - direct; overhead > 0.5 {
		t.Fatalf("steady writer pump adds %.2f allocations/request; want no per-request backlog allocation", overhead)
	}
}

func TestWriterPumpPreservesCanceledAndControlTail(t *testing.T) {
	c := newPumpTestClient(t)
	c.cfg.BatchMaxRecords = 2
	conn := &pumpWriteConn{}
	results := make([]chan error, 5)
	for i := range results {
		results[i] = make(chan error, 1)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	reqs := []writeRequest{
		pumpTestSend(t, 1, conn, results[0]),
		pumpTestSend(t, 2, conn, results[1]),
		pumpTestSend(t, 3, conn, results[2]),
		{kind: writeKindFrame, frame: &frame.PingPacket{}, conn: conn, result: results[3]},
		pumpTestSend(t, 5, conn, results[4]),
		{kind: writeKindClose},
	}
	reqs[1].ctx = canceled
	for _, req := range reqs {
		c.writeCh <- req
	}
	done := make(chan struct{})
	go func() { defer close(done); c.runWriterLoop() }()
	defer c.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("writer failed to join after forced Close")
		}
		t.Fatal("writer did not consume its bounded close marker")
	}
	for i, ch := range results {
		select {
		case err := <-ch:
			if (i == 1 && !errors.Is(err, context.Canceled)) || (i != 1 && err != nil) {
				t.Fatalf("request %d result = %v", i+1, err)
			}
		default:
			t.Fatalf("request %d missing completion", i+1)
		}
	}
	data := conn.written.Bytes()
	for i, want := range []uint64{1, 3, 0, 5} {
		f, n, err := c.proto.DecodeFrame(data, frame.LatestVersion)
		if err != nil || f == nil || n <= 0 {
			t.Fatalf("wire frame %d: %T/%d/%v", i, f, n, err)
		}
		if want == 0 {
			if _, ok := f.(*frame.PingPacket); !ok {
				t.Fatalf("wire frame %d = %T, want PING", i, f)
			}
		} else if send, ok := f.(*frame.SendPacket); !ok || send.ClientSeq != want || !bytes.Equal(send.Payload, []byte("pump-payload")) {
			t.Fatalf("wire frame %d = %v, want SEND %d with original payload", i, f, want)
		}
		data = data[n:]
	}
	if len(data) != 0 {
		t.Fatalf("unexpected wire tail: %d bytes", len(data))
	}
}

func TestReaderPumpReusesBaseAfterFragmentedLargeFrame(t *testing.T) {
	const baseSize = 256
	c := newPumpTestClient(t)
	c.cfg.ReadBufferSize = baseSize
	packets := []*frame.RecvPacket{
		pumpTestRecv(1, []byte("retained-first")),
		pumpTestRecv(2, bytes.Repeat([]byte{0x7b}, 4096)),
		pumpTestRecv(3, []byte("overwrite-third")),
		pumpTestRecv(4, []byte("coalesced-fourth")),
	}
	encoded := make([][]byte, len(packets))
	for i, pkt := range packets {
		var err error
		encoded[i], err = c.proto.EncodeFrame(pkt, frame.LatestVersion)
		if err != nil {
			t.Fatal(err)
		}
	}
	// A complete first frame followed by one byte of the large frame forces
	// suffix preservation. Later small frames must return to the original base.
	chunks := [][]byte{append(append([]byte(nil), encoded[0]...), encoded[1][0])}
	for rest := encoded[1][1:]; len(rest) > 0; {
		n := min(97, len(rest))
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	chunks = append(chunks, append(append([]byte(nil), encoded[2]...), encoded[3]...))
	conn := &pumpReadConn{chunks: chunks}
	probe := &pumpDecodeProbe{Protocol: c.proto}
	c.proto = probe
	c.conn = conn
	c.recvNotify = make(chan struct{})
	c.recvErr = nil
	queue := c.recvCh
	c.readerLoop(conn, c.pending, nil)
	defer c.Close()
	if !errors.Is(c.recvErr, io.EOF) {
		t.Fatalf("reader termination = %v, want complete-frame EOF", c.recvErr)
	}
	if len(queue) != len(packets) {
		t.Fatalf("queued %d packets, want %d", len(queue), len(packets))
	}
	// All packets remain queued until every subsequent transport read finished.
	for _, want := range packets {
		got := <-queue
		if got.MessageID != want.MessageID || got.ClientMsgNo != want.ClientMsgNo || got.ChannelID != want.ChannelID || got.FromUID != want.FromUID || got.MsgKey != want.MsgKey || got.Topic != want.Topic || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("retained packet %d changed during buffer reuse", want.MessageID)
		}
	}
	if probe.first == nil || probe.afterLarge == nil || probe.incomplete == 0 {
		t.Fatalf("missing decode observations: %+v", probe)
	}
	if probe.first != probe.afterLarge || probe.afterLargeCap != baseSize {
		t.Fatalf("small frame after large frame did not return to fixed %d-byte base: same=%t capacity=%d", baseSize, probe.first == probe.afterLarge, probe.afterLargeCap)
	}
}

func BenchmarkWriterPumpSteadyState(b *testing.B) {
	c := newPumpTestClient(b)
	startPumpTestWriter(b, c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req := pumpTestSend(b, 1, discardConn{}, make(chan error, 1))
	pumpTestSubmit(b, ctx, c, req)
	b.ReportAllocs()
	b.SetBytes(int64(len(req.pkt.Payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pumpTestSubmit(b, ctx, c, req)
	}
	b.StopTimer()
}

func BenchmarkReaderPumpSteadyState(b *testing.B) {
	c := newPumpTestClient(b)
	data, err := c.proto.EncodeFrame(&frame.SendackPacket{ClientSeq: 1, ClientMsgNo: "pump-ack", MessageID: 1, MessageSeq: 1, ReasonCode: frame.ReasonSuccess}, frame.LatestVersion)
	if err != nil {
		b.Fatal(err)
	}
	conn := &pumpReadConn{repeat: data, remaining: b.N}
	c.conn = conn
	c.recvNotify = make(chan struct{})
	c.recvErr = nil
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	c.readerLoop(conn, c.pending, nil)
	b.StopTimer()
	if conn.remaining != 0 || !errors.Is(c.recvErr, io.EOF) {
		b.Fatalf("reader incomplete: remaining=%d error=%v", conn.remaining, c.recvErr)
	}
	_ = c.Close()
}

func newPumpTestClient(tb testing.TB) *Client {
	tb.Helper()
	c, err := New(Config{Addr: "bounded-pump", BatchMaxRecords: 1, BatchMaxWait: -1, SendQueueCapacity: 8, MaxInflight: 8, InboundFrameBufferSize: 4})
	if err != nil {
		tb.Fatal(err)
	}
	return c
}

func pumpTestSend(tb testing.TB, seq uint64, conn net.Conn, result chan error) writeRequest {
	tb.Helper()
	pkt, err := buildSendPacket(Message{ClientSeq: seq, ClientMsgNo: "pump-send", ChannelID: "pump-channel", ChannelType: 2, Payload: []byte("pump-payload")}, seq)
	if err != nil {
		tb.Fatal(err)
	}
	return writeRequest{kind: writeKindSend, pkt: pkt, conn: conn, ctx: context.Background(), result: result}
}

func startPumpTestWriter(tb testing.TB, c *Client) {
	tb.Helper()
	done := make(chan struct{})
	go func() { defer close(done); c.runWriterLoop() }()
	tb.Cleanup(func() {
		_ = c.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			tb.Fatal("writer did not join after Close")
		}
	})
}

func pumpTestSubmit(tb testing.TB, ctx context.Context, c *Client, req writeRequest) {
	tb.Helper()
	select {
	case c.writeCh <- req:
	case <-ctx.Done():
		tb.Fatal(ctx.Err())
	}
	select {
	case err := <-req.result:
		if err != nil {
			tb.Fatal(err)
		}
	case <-ctx.Done():
		tb.Fatal(ctx.Err())
	}
}

func pumpTestRecv(id int64, payload []byte) *frame.RecvPacket {
	return &frame.RecvPacket{Setting: frame.SettingNoEncrypt | frame.SettingTopic, MessageID: id, MessageSeq: uint64(id), ClientMsgNo: "retained-message", ChannelID: "retained-channel", ChannelType: 2, FromUID: "retained-sender", MsgKey: "retained-key", Topic: "retained-topic", Payload: payload}
}

type pumpWriteConn struct {
	discardConn
	written bytes.Buffer
}

func (c *pumpWriteConn) Write(data []byte) (int, error) { return c.written.Write(data) }

// pumpReadConn returns finite scripted chunks, or one fixed frame per read.
// It retains no data supplied by the reader and starts no goroutines.
type pumpReadConn struct {
	discardConn
	chunks    [][]byte
	repeat    []byte
	remaining int
}

func (c *pumpReadConn) Read(dst []byte) (int, error) {
	if c.remaining > 0 {
		if len(c.repeat) > len(dst) {
			return 0, io.ErrShortBuffer
		}
		c.remaining--
		return copy(dst, c.repeat), nil
	}
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := c.chunks[0]
	n := copy(dst, chunk)
	if n == len(chunk) {
		c.chunks = c.chunks[1:]
	} else {
		c.chunks[0] = chunk[n:]
	}
	return n, nil
}

// The wrapper observes backing storage but delegates every decode to WKProto.
// Only fixed tiny-frame addresses are retained, never the large temporary base.
type pumpDecodeProbe struct {
	codec.Protocol
	first         *byte
	afterLarge    *byte
	afterLargeCap int
	incomplete    int
}

func (p *pumpDecodeProbe) DecodeFrame(data []byte, version uint8) (frame.Frame, int, error) {
	f, n, err := p.Protocol.DecodeFrame(data, version)
	if err == nil && f == nil {
		p.incomplete++
	}
	if pkt, ok := f.(*frame.RecvPacket); ok {
		switch pkt.MessageID {
		case 1:
			p.first = &data[0]
		case 3:
			p.afterLarge, p.afterLargeCap = &data[0], cap(data)
		}
	}
	return f, n, err
}
