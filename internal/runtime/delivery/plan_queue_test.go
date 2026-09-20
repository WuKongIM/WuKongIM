package delivery

import (
	"context"
	"testing"
)

func TestPlanQueueRotatesChannelsAndReleasesOwnership(t *testing.T) {
	q := newOrderedPlanQueue(3)
	open := make(chan struct{})
	first := runtimePlanForTest(1)
	first.Event.ChannelID = "shared-id"
	first.Event.ChannelType = 1
	second := first
	second.Event.MessageSeq = 2
	other := first
	other.Event.ChannelType = 2 // Channel type is part of ordering identity.
	other.Event.MessageSeq = 3
	if err := q.enqueue(context.Background(), open, first); err != nil {
		t.Fatal(err)
	}
	active, ok := q.pop()
	if !ok {
		t.Fatal("first plan not runnable")
	}
	if err := q.enqueue(context.Background(), open, second); err != nil {
		t.Fatal(err)
	}
	if _, ok := q.pop(); ok {
		t.Fatal("same Channel has two executing plans")
	}
	if err := q.enqueue(context.Background(), open, other); err != nil {
		t.Fatal(err)
	}
	q.complete(active)
	next, ok := q.pop()
	if !ok || next.Event.MessageSeq != 3 {
		t.Fatal("ready sibling did not run before queued Channel continuation")
	}
	q.complete(next)
	next, ok = q.pop()
	if !ok || next.Event.MessageSeq != 2 {
		t.Fatal("Channel continuation lost")
	}
	q.complete(next)
	if q.Depth() != 0 || len(q.channels) != 0 || len(q.slots) != q.Capacity() || q.readyHead != nil || q.readyTail != nil {
		t.Fatalf("queue retained drained ownership: depth=%d channels=%d slots=%d", q.Depth(), len(q.channels), len(q.slots))
	}
}
