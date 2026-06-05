package wire

import (
	"fmt"
	"io"
	"math"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

// AppendFrame appends a frame header and body to buffers without copying the body.
func AppendFrame(buffers *net.Buffers, frame Frame, maxBodyBytes int) error {
	body := frame.Body.Bytes()
	if len(body) > maxBodyBytes {
		return fmt.Errorf("%w: body %d > max %d", core.ErrMsgTooLarge, len(body), maxBodyBytes)
	}
	if len(body) > math.MaxUint32 {
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

// WriteFrame writes one frame using net.Buffers.
func WriteFrame(w io.Writer, frame Frame, maxBodyBytes int) error {
	var buffers net.Buffers
	if err := AppendFrame(&buffers, frame, maxBodyBytes); err != nil {
		return err
	}
	_, err := buffers.WriteTo(w)
	return err
}

func validateOutboundHeader(header Header, maxBodyBytes int) error {
	if !header.Kind.Valid() {
		return fmt.Errorf("%w: kind %d", core.ErrInvalidFrame, header.Kind)
	}
	if err := header.Priority.Validate(); err != nil {
		return err
	}
	if int(header.BodyLen) > maxBodyBytes {
		return fmt.Errorf("%w: body %d > max %d", core.ErrMsgTooLarge, header.BodyLen, maxBodyBytes)
	}
	return nil
}
