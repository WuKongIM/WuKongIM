package reactor

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestMsgQueue(t *testing.T) {
	q := newMsgQueue("queue")
	for i := 0; i < 100; i++ {
		q.append(testMessage{
			i: i,
		})
	}

	msgs := q.slice(0, 50)
	assert.Equal(t, 50, len(msgs))

	msgs = q.sliceWithSize(0, 50, 100)
	assert.Equal(t, 10, len(msgs))

	q.truncateTo(50)

	msgs = q.slice(50, 100)
	assert.Equal(t, 50, len(msgs))
}

type testMessage struct {
	i int
}

func (t testMessage) Conn() Conn {
	return nil
}
func (t testMessage) Frame() wkproto.Frame {
	return nil
}
func (t testMessage) Size() uint64 {
	return 10
}
