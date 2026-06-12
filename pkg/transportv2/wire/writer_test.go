package wire

import (
	"bytes"
	"errors"
	"net"
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

func TestWriteFrameRejectsInvalidKind(t *testing.T) {
	var out bytes.Buffer
	frame := Frame{
		Header: Header{
			Kind:     core.FrameKind(99),
			Priority: core.PriorityBulk,
		},
		Body: core.NewOwnedBuffer([]byte("body"), nil),
	}

	err := WriteFrame(&out, frame, 1024)
	if !errors.Is(err, core.ErrInvalidFrame) {
		t.Fatalf("WriteFrame() error = %v, want ErrInvalidFrame", err)
	}
	if out.Len() != 0 {
		t.Fatalf("written bytes = %d, want 0", out.Len())
	}
}

func TestWriteFrameRejectsInvalidPriority(t *testing.T) {
	var out bytes.Buffer
	frame := Frame{
		Header: Header{
			Kind:     core.FrameKindData,
			Priority: core.Priority(99),
		},
		Body: core.NewOwnedBuffer([]byte("body"), nil),
	}

	err := WriteFrame(&out, frame, 1024)
	if !errors.Is(err, core.ErrInvalidPriority) {
		t.Fatalf("WriteFrame() error = %v, want ErrInvalidPriority", err)
	}
	if out.Len() != 0 {
		t.Fatalf("written bytes = %d, want 0", out.Len())
	}
}

func TestAppendFrameBatchesMultipleFrames(t *testing.T) {
	var buffers net.Buffers
	first := Frame{
		Header: Header{
			Kind:      core.FrameKindData,
			Priority:  core.PriorityRaft,
			ServiceID: 1,
			RequestID: 2,
		},
		Body: core.NewOwnedBuffer([]byte("one"), nil),
	}
	second := Frame{
		Header: Header{
			Kind:      core.FrameKindNotify,
			Priority:  core.PriorityControl,
			ServiceID: 3,
			RequestID: 4,
		},
		Body: core.NewOwnedBuffer([]byte("two"), nil),
	}

	if err := AppendFrame(&buffers, first, 1024); err != nil {
		t.Fatalf("AppendFrame(first) error = %v", err)
	}
	if err := AppendFrame(&buffers, second, 1024); err != nil {
		t.Fatalf("AppendFrame(second) error = %v", err)
	}

	var out bytes.Buffer
	if _, err := buffers.WriteTo(&out); err != nil {
		t.Fatalf("buffers.WriteTo() error = %v", err)
	}

	readFirst, err := ReadFrame(&out, 1024)
	if err != nil {
		t.Fatalf("ReadFrame(first) error = %v", err)
	}
	defer readFirst.Body.Release()
	readSecond, err := ReadFrame(&out, 1024)
	if err != nil {
		t.Fatalf("ReadFrame(second) error = %v", err)
	}
	defer readSecond.Body.Release()

	if string(readFirst.Body.Bytes()) != "one" {
		t.Fatalf("first body = %q, want one", readFirst.Body.Bytes())
	}
	if string(readSecond.Body.Bytes()) != "two" {
		t.Fatalf("second body = %q, want two", readSecond.Body.Bytes())
	}
	if out.Len() != 0 {
		t.Fatalf("remaining bytes = %d, want 0", out.Len())
	}
}

func TestWriteFramesWritesReadableFrames(t *testing.T) {
	var out bytes.Buffer
	frames := []Frame{
		{
			Header: Header{
				Kind:      core.FrameKindData,
				Priority:  core.PriorityRPC,
				ServiceID: 7,
				RequestID: 8,
			},
			Body: core.NewOwnedBuffer([]byte("alpha"), nil),
		},
		{
			Header: Header{
				Kind:      core.FrameKindNotify,
				Priority:  core.PriorityControl,
				ServiceID: 9,
				RequestID: 10,
			},
			Body: core.NewOwnedBuffer([]byte("beta"), nil),
		},
	}

	if err := WriteFrames(&out, frames, 1024); err != nil {
		t.Fatalf("WriteFrames() error = %v", err)
	}

	first, err := ReadFrame(&out, 1024)
	if err != nil {
		t.Fatalf("ReadFrame(first) error = %v", err)
	}
	defer first.Body.Release()
	second, err := ReadFrame(&out, 1024)
	if err != nil {
		t.Fatalf("ReadFrame(second) error = %v", err)
	}
	defer second.Body.Release()

	if string(first.Body.Bytes()) != "alpha" {
		t.Fatalf("first body = %q, want alpha", first.Body.Bytes())
	}
	if string(second.Body.Bytes()) != "beta" {
		t.Fatalf("second body = %q, want beta", second.Body.Bytes())
	}
	if out.Len() != 0 {
		t.Fatalf("remaining bytes = %d, want 0", out.Len())
	}
}

func TestWriteFramesIntoReusesBuffers(t *testing.T) {
	var out bytes.Buffer
	buffers := make(net.Buffers, 0, 8)
	frame := Frame{
		Header: Header{Kind: core.FrameKindData, Priority: core.PriorityRaft},
		Body:   core.NewOwnedBuffer([]byte("abc"), nil),
	}

	if err := WriteFramesInto(&out, &buffers, []Frame{frame}, 1024); err != nil {
		t.Fatalf("WriteFramesInto() error = %v", err)
	}
	got, err := ReadFrame(bytes.NewReader(out.Bytes()), 1024)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer got.Body.Release()
	if string(got.Body.Bytes()) != "abc" {
		t.Fatalf("body = %q, want abc", got.Body.Bytes())
	}
	if cap(buffers) < 8 {
		t.Fatalf("cap(buffers) = %d, want caller capacity preserved (>=8)", cap(buffers))
	}
}

func TestWriteFramesRejectsInvalidFrameBeforeWriting(t *testing.T) {
	var out bytes.Buffer
	frames := []Frame{
		{
			Header: Header{
				Kind:     core.FrameKindData,
				Priority: core.PriorityRPC,
			},
			Body: core.NewOwnedBuffer([]byte("valid"), nil),
		},
		{
			Header: Header{
				Kind:     core.FrameKindData,
				Priority: core.PriorityRPC,
			},
			Body: core.NewOwnedBuffer([]byte("too-large"), nil),
		},
	}

	err := WriteFrames(&out, frames, 4)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("WriteFrames() error = %v, want ErrMsgTooLarge", err)
	}
	if out.Len() != 0 {
		t.Fatalf("written bytes = %d, want 0", out.Len())
	}
}
