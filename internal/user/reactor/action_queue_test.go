package reactor

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/stretchr/testify/assert"
)

func TestActionQueueCanBeCreated(t *testing.T) {
	q := newActionQueue(8, false, 0, 0)
	if len(q.left) != 8 || len(q.right) != 8 {
		t.Errorf("unexpected size")
	}
}

func TestMessageCanBeAddedAndGet(t *testing.T) {
	q := newActionQueue(8, false, 0, 0)
	for i := 0; i < 8; i++ {
		added := q.add(reactor.UserAction{})
		if !added {
			t.Errorf("failed to add")
		}
	}
	add := q.add(reactor.UserAction{})
	add2 := q.add(reactor.UserAction{})
	if add || add2 {
		t.Errorf("failed to drop message")
	}

	if q.idx != 8 {
		t.Errorf("unexpected idx %d", q.idx)
	}
	lr := q.leftInWrite
	actions := q.get()
	assert.NotEqual(t, 0, len(actions))
	if q.idx != 0 {
		t.Errorf("unexpected idx %d", q.idx)
	}
	if lr == q.leftInWrite {
		t.Errorf("lr flag not updated")
	}
	add = q.add(reactor.UserAction{})
	add2 = q.add(reactor.UserAction{})
	if !add || !add2 {
		t.Errorf("failed to add message")
	}
}
