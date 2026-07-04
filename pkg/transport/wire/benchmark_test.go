package wire

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

var (
	benchmarkHeaderSink  Header
	benchmarkEncodedSink [HeaderSize]byte
	benchmarkBuffersSink int
)

func BenchmarkEncodeHeader(b *testing.B) {
	header := benchmarkHeader(1024)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkEncodedSink = EncodeHeader(header)
	}
}

func BenchmarkDecodeHeader(b *testing.B) {
	encoded := EncodeHeader(benchmarkHeader(1024))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		header, err := DecodeHeader(encoded[:], 1<<20)
		if err != nil {
			b.Fatalf("DecodeHeader() error = %v", err)
		}
		benchmarkHeaderSink = header
	}
}

func BenchmarkAppendFrame1KiB(b *testing.B) {
	payload := bytes.Repeat([]byte("a"), 1<<10)
	frame := benchmarkFrame(payload)
	var buffers net.Buffers

	b.SetBytes(int64(len(payload) + HeaderSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffers = buffers[:0]
		if err := AppendFrame(&buffers, frame, 1<<20); err != nil {
			b.Fatalf("AppendFrame() error = %v", err)
		}
		benchmarkBuffersSink = len(buffers)
	}
}

func BenchmarkWriteFrame1KiB(b *testing.B) {
	payload := bytes.Repeat([]byte("w"), 1<<10)
	frame := benchmarkFrame(payload)

	b.SetBytes(int64(len(payload) + HeaderSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := WriteFrame(io.Discard, frame, 1<<20); err != nil {
			b.Fatalf("WriteFrame() error = %v", err)
		}
	}
}

func BenchmarkReadFrame1KiB(b *testing.B) {
	payload := bytes.Repeat([]byte("r"), 1<<10)
	frame := benchmarkFrame(payload)
	var encoded bytes.Buffer
	if err := WriteFrame(&encoded, frame, 1<<20); err != nil {
		b.Fatalf("WriteFrame() setup error = %v", err)
	}
	raw := encoded.Bytes()

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, err := ReadFrame(bytes.NewReader(raw), 1<<20)
		if err != nil {
			b.Fatalf("ReadFrame() error = %v", err)
		}
		frame.Body.Release()
	}
}

func benchmarkHeader(bodyLen int) Header {
	return Header{
		Kind:      core.FrameKindRPCRequest,
		Priority:  core.PriorityRPC,
		ServiceID: 7,
		RequestID: 42,
		BodyLen:   uint32(bodyLen),
	}
}

func benchmarkFrame(payload []byte) Frame {
	return Frame{
		Header: benchmarkHeader(len(payload)),
		Body:   core.NewOwnedBuffer(payload, nil),
	}
}
