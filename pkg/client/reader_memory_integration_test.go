//go:build integration

package client

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Exercise the actual reader through the existing transport boundary. TotalAlloc
// counts transient buffers too; GC timing cannot hide the duplicate allocation.
func TestReaderPumpInitialMemoryBudget(t *testing.T) {
	const readers = 128
	const readSize = 64 << 10
	c := newPumpTestClient(t)
	c.cfg.ReadBufferSize = readSize
	defer c.Close()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range readers {
		conn := &pumpReadConn{}
		c.conn = conn
		c.readerLoop(conn, c.pending, nil)
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	// One 64KiB buffer plus 16KiB per connection for existing failure bookkeeping.
	const budget = readers * (80 << 10)
	t.Logf("reader starts=%d allocated_bytes=%d budget_bytes=%d", readers, allocated, budget)
	if allocated > budget {
		t.Fatalf("reader initial allocation = %d bytes; budget %d", allocated, budget)
	}
}

func TestReaderPumpSmallBufferFragmentationAndEOF(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  int
		truncate bool
	}{
		{"below_initial", 8000, false},
		{"cross_initial", 8192, false},
		{"large", 128 << 10, false},
		{"truncated_large", 128 << 10, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newPumpTestClient(t)
			c.cfg.ReadBufferSize = 8 << 10
			defer c.Close()
			packets := []*frame.RecvPacket{pumpTestRecv(1, []byte("retained-first")), pumpTestRecv(2, bytes.Repeat([]byte{0x7b}, test.payload)), pumpTestRecv(3, []byte("retained-last"))}
			var stream []byte
			for i, pkt := range packets {
				encoded, err := c.proto.EncodeFrame(pkt, frame.LatestVersion)
				if err != nil {
					t.Fatal(err)
				}
				if test.truncate && i == 1 {
					stream = append(stream, encoded[:len(encoded)-1]...)
					break
				}
				stream = append(stream, encoded...)
			}
			// An exact full read with an incomplete frame forces buffer growth;
			// subsequent chunks end with bytes and EOF in the same Read result.
			var chunks [][]byte
			for len(stream) > 0 {
				n := min(8<<10, len(stream))
				chunks = append(chunks, stream[:n])
				stream = stream[n:]
			}
			conn := &readerBytesEOFConn{pumpReadConn: pumpReadConn{chunks: chunks}}
			c.conn = conn
			c.recvNotify = make(chan struct{})
			c.recvErr = nil
			queue := c.recvCh
			c.readerLoop(conn, c.pending, nil)
			wantErr := error(io.EOF)
			wantPackets := len(packets)
			if test.truncate {
				wantErr = io.ErrUnexpectedEOF
				wantPackets = 1
			}
			if !errors.Is(c.recvErr, wantErr) {
				t.Fatalf("termination = %v, want %v", c.recvErr, wantErr)
			}
			if len(queue) != wantPackets {
				t.Fatalf("queued = %d, want %d", len(queue), wantPackets)
			}
			for _, want := range packets[:wantPackets] {
				got := <-queue
				if got.MessageID != want.MessageID || !bytes.Equal(got.Payload, want.Payload) {
					t.Fatalf("retained packet%d changed during read reuse", want.MessageID)
				}
			}
		})
	}
}

type readerBytesEOFConn struct{ pumpReadConn }

func (c *readerBytesEOFConn) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, io.ErrShortBuffer
	}
	n, err := c.pumpReadConn.Read(dst)
	if len(c.chunks) == 0 {
		return n, io.EOF
	}
	return n, err
}
