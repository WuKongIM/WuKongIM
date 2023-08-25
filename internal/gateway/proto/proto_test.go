package proto

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestEncodeAndDecode(t *testing.T) {
	p := New()

	encodeData, err := p.Encode(&Block{
		Seq:        1,
		ConnID:     2,
		UID:        "test",
		DeviceFlag: 1,
		Data:       []byte("hi"),
	})
	assert.NoError(t, err)

	block, size, err := p.Decode(encodeData)
	assert.NoError(t, err)
	assert.Equal(t, len(encodeData), size)
	assert.Equal(t, uint64(1), block.Seq)
	assert.Equal(t, int64(2), block.ConnID)
	assert.Equal(t, "test", block.UID)
	assert.Equal(t, wkproto.DeviceFlag(1), block.DeviceFlag)
	assert.Equal(t, []byte("hi"), block.Data)

}
