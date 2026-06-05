package wire

import (
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
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
	body := make([]byte, bodyLen)
	if len(body) > 0 {
		if _, err := io.ReadFull(r, body); err != nil {
			return Frame{}, err
		}
	}

	return Frame{
		Header: header,
		Body:   core.NewOwnedBuffer(body, nil),
	}, nil
}
