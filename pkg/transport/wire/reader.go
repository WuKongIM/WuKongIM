package wire

import (
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/buffer"
)

// ReadFrame reads one full frame, validating the header before allocating the body.
func ReadFrame(r io.Reader, maxBodyBytes int) (Frame, error) {
	var encoded [HeaderSize]byte
	if _, err := io.ReadFull(r, encoded[:]); err != nil {
		return Frame{}, err
	}

	header, err := DecodeHeader(encoded[:], maxBodyBytes)
	if err != nil {
		return Frame{}, err
	}

	bodyLen, err := bodyLenToInt(header.BodyLen)
	if err != nil {
		return Frame{}, err
	}
	body := buffer.DefaultSlabPool.Get(bodyLen)
	if body.Len() > 0 {
		if _, err := io.ReadFull(r, body.Bytes()); err != nil {
			body.Release()
			return Frame{}, err
		}
	}

	return Frame{
		Header: header,
		Body:   body,
	}, nil
}
