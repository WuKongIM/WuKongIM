package proto

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestEncodeAndDecode(t *testing.T) {
	p := New()

	encodeData, err := p.Encode(&Block{
		Seq: 1,
		Conn: &pb.Conn{
			Id:         2,
			Uid:        "test",
			DeviceFlag: 1,
		},
		Data: []byte("hi"),
	})
	assert.NoError(t, err)

	block, size, err := p.Decode(encodeData)
	assert.NoError(t, err)
	assert.Equal(t, len(encodeData), size)
	assert.Equal(t, uint64(1), block.Seq)
	assert.Equal(t, int64(2), block.Conn.Id)
	assert.Equal(t, "test", block.Conn.Uid)
	assert.Equal(t, wkproto.DeviceFlag(1), wkproto.DeviceFlag(block.Conn.DeviceFlag))
	assert.Equal(t, []byte("hi"), block.Data)

}
