package wire

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestWriteFrameWritesHeaderAndBody(t *testing.T) {
	var out bytes.Buffer
	frame := Frame{
		Header: Header{
			Kind:      core.FrameKindNotify,
			Priority:  core.PriorityControl,
			ServiceID: 3,
			RequestID: 4,
		},
		Body: core.NewOwnedBuffer([]byte("body"), nil),
	}

	if err := WriteFrame(&out, frame, 1024); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}

	got, err := ReadFrame(bytes.NewReader(out.Bytes()), 1024)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer got.Body.Release()

	wantHeader := frame.Header
	wantHeader.BodyLen = 4
	if got.Header != wantHeader {
		t.Fatalf("written header = %+v, want %+v", got.Header, wantHeader)
	}
	if string(got.Body.Bytes()) != "body" {
		t.Fatalf("written body = %q, want body", got.Body.Bytes())
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	var out bytes.Buffer
	frame := Frame{
		Header: Header{
			Kind:     core.FrameKindData,
			Priority: core.PriorityBulk,
		},
		Body: core.NewOwnedBuffer([]byte("toolarge"), nil),
	}

	err := WriteFrame(&out, frame, 4)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("WriteFrame() error = %v, want ErrMsgTooLarge", err)
	}
	if out.Len() != 0 {
		t.Fatalf("written bytes = %d, want 0", out.Len())
	}
}
