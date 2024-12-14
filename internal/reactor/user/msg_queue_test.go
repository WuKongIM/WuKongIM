package reactor

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/stretchr/testify/assert"
)

func TestMsgQueue(t *testing.T) {
	q := newMsgQueue("queue")
	for i := 0; i < 100; i++ {
		q.append(&reactor.UserMessage{})
	}

	msgs := q.slice(1, 51)
	assert.Equal(t, 50, len(msgs))

	msgs = q.sliceWithSize(1, 51, 100)
	assert.Equal(t, 10, len(msgs))

	q.truncateTo(50)

	msgs = q.slice(51, 101)
	assert.Equal(t, 50, len(msgs))
}
