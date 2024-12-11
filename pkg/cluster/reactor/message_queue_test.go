package reactor

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

func TestMessageQueueCanBeCreated(t *testing.T) {
	q := NewMessageQueue(8, false, 0, 0)
	if len(q.left) != 8 || len(q.right) != 8 {
		t.Errorf("unexpected size")
	}
}

func TestMessageCanBeAddedAndGet(t *testing.T) {
	q := NewMessageQueue(8, false, 0, 0)
	for i := 0; i < 8; i++ {
		added := q.Add(Message{})
		if !added {
			t.Errorf("failed to add")
		}
	}
	add := q.Add(Message{})
	add2 := q.Add(Message{})
	if add || add2 {
		t.Errorf("failed to drop message")
	}

	if q.idx != 8 {
		t.Errorf("unexpected idx %d", q.idx)
	}
	lr := q.leftInWrite
	q.Get()
	if q.idx != 0 {
		t.Errorf("unexpected idx %d", q.idx)
	}
	if lr == q.leftInWrite {
		t.Errorf("lr flag not updated")
	}
	add = q.Add(Message{})
	add2 = q.Add(Message{})
	if !add || !add2 {
		t.Errorf("failed to add message")
	}
}

func TestAddMessageIsRateLimited(t *testing.T) {
	q := NewMessageQueue(10000, false, 0, 1024)
	for i := 0; i < 10000; i++ {
		m := Message{
			Message: replica.Message{
				Logs: []replica.Log{
					{
						Data: []byte("testtesttesttest"),
					},
				},
			},
		}
		if q.rl.RateLimited() {
			added := q.Add(m)
			if !added {
				return
			}
		} else {
			sz := q.rl.Get()
			added := q.Add(m)
			if added {
				if q.rl.Get() != sz+uint64(m.Size()) {
					t.Errorf("failed to update rate limit")
				}
			}
			if !added {
				t.Errorf("failed to add")
			}
		}
	}
	t.Fatalf("failed to observe any rate limited message")
}

func TestGetWillResetTheRateLimiterSize(t *testing.T) {
	q := NewMessageQueue(10000, false, 0, 1024)
	for i := 0; i < 8; i++ {
		m := Message{
			Message: replica.Message{
				Logs: []replica.Log{
					{
						Data: []byte("testtesttesttest"),
					},
				},
			},
		}
		added := q.Add(m)
		if !added {
			t.Fatalf("failed to add message")
		}
	}
	if q.rl.Get() == 0 {
		t.Errorf("rate limiter size is 0")
	}
	q.Get()
	if q.rl.Get() != 0 {
		t.Fatalf("failed to reset the rate limiter")
	}
}
