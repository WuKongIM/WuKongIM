package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func BenchmarkPrepareSend(b *testing.B) {
	c, err := New(Config{Addr: "bench"})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	msg := benchmarkMessage(benchmarkPayload(256))

	b.ReportAllocs()
	b.SetBytes(int64(len(msg.Payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.prepareSend(msg); err != nil {
			b.Fatalf("prepareSend() error = %v", err)
		}
	}
}

func BenchmarkPendingTrackerResolve(b *testing.B) {
	tracker := newPendingTracker()
	ack := &frame.SendackPacket{ReasonCode: frame.ReasonSuccess}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint64(i + 1)
		entry, err := tracker.add(pendingKey{ClientSeq: seq}, 0)
		if err != nil {
			b.Fatalf("pending add error = %v", err)
		}
		ack.ClientSeq = seq
		ack.MessageID = int64(seq)
		ack.MessageSeq = seq
		if !tracker.resolve(ack) {
			b.Fatal("pending resolve returned false")
		}
		<-entry.done
	}
}

func BenchmarkWriteBatchEncode(b *testing.B) {
	for _, batchSize := range []int{1, 64, 512} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			benchmarkWriteBatchEncode(b, batchSize)
		})
	}
}

func BenchmarkClientSendBatchRoundTrip(b *testing.B) {
	for _, batchSize := range []int{1, 64} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			benchmarkClientSendBatchRoundTrip(b, batchSize)
		})
	}
}

func benchmarkWriteBatchEncode(b *testing.B, batchSize int) {
	c, err := New(Config{
		Addr:            "discard",
		BatchMaxRecords: batchSize,
		BatchMaxBytes:   32 << 20,
		BatchMaxWait:    -1,
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}

	payload := benchmarkPayload(256)
	conn := discardConn{}
	batch := make([]writeRequest, batchSize)
	for i := range batch {
		seq := uint64(i + 1)
		pkt, err := buildSendPacket(benchmarkMessageWithSeq(payload, seq), seq)
		if err != nil {
			b.Fatalf("buildSendPacket() error = %v", err)
		}
		batch[i] = writeRequest{
			kind: writeKindSend,
			pkt:  pkt,
			ctx:  context.Background(),
			conn: conn,
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * batchSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.writeBatch(batch); err != nil {
			b.Fatalf("writeBatch() error = %v", err)
		}
	}
}

func benchmarkClientSendBatchRoundTrip(b *testing.B, batchSize int) {
	clientConn, serverConn := net.Pipe()
	c, err := New(Config{
		Addr:                   "pipe",
		Dialer:                 fakeDialer{conn: clientConn},
		OperationTimeout:       10 * time.Second,
		AckTimeout:             10 * time.Second,
		SendQueueCapacity:      65536,
		MaxInflight:            65536,
		BatchMaxRecords:        batchSize,
		BatchMaxBytes:          32 << 20,
		BatchMaxWait:           -1,
		InboundFrameBufferSize: 1024,
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveBenchmarkSendacks(serverConn, b.N*batchSize)
	}()
	b.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
		select {
		case <-serverDone:
		default:
		}
	})

	if _, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "bench-uid",
		DeviceID:   "bench-device",
		DeviceFlag: frame.APP,
	}); err != nil {
		b.Fatalf("Connect() error = %v", err)
	}

	payload := benchmarkPayload(256)
	msgs := make([]Message, batchSize)
	for i := range msgs {
		msgs[i] = benchmarkMessage(payload)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * batchSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.SendBatch(context.Background(), msgs); err != nil {
			b.Fatalf("SendBatch() error = %v", err)
		}
	}
	b.StopTimer()

	_ = c.Close()
	_ = serverConn.Close()
	if err := <-serverDone; err != nil {
		b.Fatalf("benchmark server error = %v", err)
	}
}

func serveBenchmarkSendacks(conn net.Conn, totalSends int) error {
	defer conn.Close()
	proto := codec.New()
	f, err := proto.DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return err
	}
	if _, ok := f.(*frame.ConnectPacket); !ok {
		return fmt.Errorf("benchmark server got %T, want *frame.ConnectPacket", f)
	}
	if err := writeBenchmarkFrame(proto, conn, &frame.ConnackPacket{
		Framer: frame.Framer{
			FrameType:        frame.CONNACK,
			HasServerVersion: true,
		},
		ServerVersion: frame.LatestVersion,
		ReasonCode:    frame.ReasonSuccess,
		NodeId:        1,
	}); err != nil {
		return err
	}

	for i := 0; i < totalSends; i++ {
		f, err := proto.DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			return err
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("benchmark server got %T, want *frame.SendPacket", f)
		}
		if err := writeBenchmarkFrame(proto, conn, &frame.SendackPacket{
			Framer:      frame.Framer{FrameType: frame.SENDACK},
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   int64(send.ClientSeq),
			MessageSeq:  send.ClientSeq,
			ReasonCode:  frame.ReasonSuccess,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeBenchmarkFrame(proto *codec.WKProto, conn net.Conn, f frame.Frame) error {
	data, err := proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func benchmarkMessage(payload []byte) Message {
	return Message{
		ChannelID:   "benchmark-channel",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     payload,
	}
}

func benchmarkMessageWithSeq(payload []byte, seq uint64) Message {
	msg := benchmarkMessage(payload)
	msg.ClientSeq = seq
	return msg
}

func benchmarkPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}
	return payload
}

type discardConn struct{}

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return benchmarkAddr("local") }
func (discardConn) RemoteAddr() net.Addr             { return benchmarkAddr("remote") }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

type benchmarkAddr string

func (a benchmarkAddr) Network() string { return "benchmark" }
func (a benchmarkAddr) String() string  { return string(a) }
