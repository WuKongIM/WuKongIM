package transport

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
)

func TestReadFrameRoundTrip(t *testing.T) {
	var bufs net.Buffers
	writeFrame(&bufs, 1, []byte("hello"))

	msgType, body, release, err := readFrame(bytes.NewReader(bytes.Join(bufs, nil)))
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	defer release()

	if msgType != 1 {
		t.Fatalf("msgType = %d, want 1", msgType)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
}

func TestReadFrameEmptyBody(t *testing.T) {
	var bufs net.Buffers
	writeFrame(&bufs, 42, nil)

	msgType, body, release, err := readFrame(bytes.NewReader(bytes.Join(bufs, nil)))
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	defer release()

	if msgType != 42 {
		t.Fatalf("msgType = %d, want 42", msgType)
	}
	if len(body) != 0 {
		t.Fatalf("body len = %d, want 0", len(body))
	}
}

func TestReadFrameRejectsInvalidMsgType(t *testing.T) {
	frame := []byte{0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	_, _, _, err := readFrame(bytes.NewReader(frame))
	if !errors.Is(err, ErrInvalidMsgType) {
		t.Fatalf("readFrame() error = %v, want %v", err, ErrInvalidMsgType)
	}
}

func TestReadFrameRejectsOversizedBody(t *testing.T) {
	hdr := encodeHeader(1, MaxFrameSize+1)
	_, _, _, err := readFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrMsgTooLarge) {
		t.Fatalf("readFrame() error = %v, want %v", err, ErrMsgTooLarge)
	}
}

func TestReadFrameReleasesBufferOnShortRead(t *testing.T) {
	hdr := encodeHeader(1, 8)
	_, _, release, err := readFrame(io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader([]byte("bad"))))
	if err == nil {
		t.Fatal("readFrame() error = nil, want short read")
	}
	if release != nil {
		t.Fatal("release should be nil on failed read")
	}
}

