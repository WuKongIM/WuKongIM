package wire

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestReadFrameReadsBody(t *testing.T) {
	header := Header{
		Kind:      core.FrameKindRPCResponse,
		Priority:  core.PriorityRPC,
		ServiceID: 7,
		RequestID: 11,
		BodyLen:   5,
	}
	encoded := EncodeHeader(header)
	input := append(encoded[:], []byte("hello")...)

	frame, err := ReadFrame(bytes.NewReader(input), 1024)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer frame.Body.Release()

	if frame.Header != header {
		t.Fatalf("frame header = %+v, want %+v", frame.Header, header)
	}
	if string(frame.Body.Bytes()) != "hello" {
		t.Fatalf("frame body = %q, want hello", frame.Body.Bytes())
	}
}

func TestReadFrameRejectsOversizedBodyBeforeRead(t *testing.T) {
	header := Header{
		Kind:     core.FrameKindData,
		Priority: core.PriorityBulk,
		BodyLen:  1025,
	}
	encoded := EncodeHeader(header)
	reader := errAfterHeaderReader{header: encoded[:]}

	_, err := ReadFrame(&reader, 1024)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("ReadFrame() error = %v, want ErrMsgTooLarge", err)
	}
	if reader.bodyReads != 0 {
		t.Fatalf("body reads = %d, want 0", reader.bodyReads)
	}
}

type errAfterHeaderReader struct {
	header    []byte
	bodyReads int
}

func (r *errAfterHeaderReader) Read(p []byte) (int, error) {
	if len(r.header) > 0 {
		n := copy(p, r.header)
		r.header = r.header[n:]
		return n, nil
	}
	r.bodyReads++
	return 0, errors.New("body should not be read")
}
