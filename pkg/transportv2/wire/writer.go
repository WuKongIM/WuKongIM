package wire

import (
	"fmt"
	"io"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

// AppendFrame appends a frame header and body to buffers without copying the body.
// The appended net.Buffers borrow frame.Body.Bytes(); callers must keep the payload
// alive and release it only after net.Buffers.WriteTo completes.
func AppendFrame(buffers *net.Buffers, frame Frame, maxBodyBytes int) error {
	body := frame.Body.Bytes()
	if len(body) > maxBodyBytes {
		return fmt.Errorf("%w: body %d > max %d", core.ErrMsgTooLarge, len(body), maxBodyBytes)
	}
	if uint64(len(body)) > uint64(^uint32(0)) {
		return fmt.Errorf("%w: body %d > uint32", core.ErrMsgTooLarge, len(body))
	}

	header := frame.Header
	header.BodyLen = uint32(len(body))
	if err := validateOutboundHeader(header, maxBodyBytes); err != nil {
		return err
	}

	encoded := EncodeHeader(header)
	*buffers = append(*buffers, encoded[:])
	if len(body) > 0 {
		*buffers = append(*buffers, body)
	}
	return nil
}

// WriteFrame writes one frame using net.Buffers. It borrows frame.Body.Bytes() for
// the duration of the call and does not release frame.Body.
func WriteFrame(w io.Writer, frame Frame, maxBodyBytes int) error {
	return WriteFrames(w, []Frame{frame}, maxBodyBytes)
}

// WriteFrames writes frames as one net.Buffers batch. It borrows every
// frame.Body.Bytes() for the duration of the call and does not release bodies.
func WriteFrames(w io.Writer, frames []Frame, maxBodyBytes int) error {
	var buffers net.Buffers
	return WriteFramesInto(w, &buffers, frames, maxBodyBytes)
}

// WriteFramesInto writes frames using the caller-provided net.Buffers as scratch.
// It resets buffers to zero length, appends each frame's header and borrowed body,
// and writes them as one batch. The caller's backing capacity is reused across calls.
// It borrows every frame.Body.Bytes() for the duration of the call and does not
// release bodies.
func WriteFramesInto(w io.Writer, buffers *net.Buffers, frames []Frame, maxBodyBytes int) error {
	*buffers = (*buffers)[:0]
	for _, frame := range frames {
		if err := AppendFrame(buffers, frame, maxBodyBytes); err != nil {
			return err
		}
	}
	writeBuffers := *buffers
	_, err := writeBuffers.WriteTo(w)
	*buffers = (*buffers)[:0]
	return err
}

func validateOutboundHeader(header Header, maxBodyBytes int) error {
	if !header.Kind.Valid() {
		return fmt.Errorf("%w: kind %d", core.ErrInvalidFrame, header.Kind)
	}
	if err := header.Priority.Validate(); err != nil {
		return err
	}
	if bodyExceedsMax(header.BodyLen, maxBodyBytes) {
		return fmt.Errorf("%w: body %d > max %d", core.ErrMsgTooLarge, header.BodyLen, maxBodyBytes)
	}
	return nil
}
