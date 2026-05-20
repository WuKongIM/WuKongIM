package codec

import (
	"errors"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDecodePacketWithConnReturnsRemainingLengthReadError(t *testing.T) {
	wantErr := errors.New("remaining length read failed")
	reader := &failingByteReader{
		data: []byte{byte(frame.CONNECT << 4), 0x80},
		err:  wantErr,
	}

	_, err := New().DecodePacketWithConn(reader, frame.LatestVersion)

	require.Error(t, err)
	require.True(t, errors.Is(err, wantErr), "err = %v, want %v", err, wantErr)
}

type failingByteReader struct {
	data []byte
	err  error
}

func (r *failingByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data[:1])
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, nil
	}
	return n, nil
}

var _ io.Reader = (*failingByteReader)(nil)
