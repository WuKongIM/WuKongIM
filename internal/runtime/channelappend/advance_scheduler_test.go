package channelappend

import "testing"

func TestWriterAdvanceSchedulerCompactionClearsDequeuedTail(t *testing.T) {
	const writerCount = 2048
	scheduler := &writerAdvanceScheduler{
		queue: make([]*channelWriter, writerCount),
		head:  writerCount / 2,
	}
	for i := range scheduler.queue {
		scheduler.queue[i] = &channelWriter{}
	}
	firstRemaining := scheduler.queue[scheduler.head]
	oldLen := len(scheduler.queue)

	scheduler.compactQueueLocked()

	if scheduler.head != 0 {
		t.Fatalf("compacted head = %d, want 0", scheduler.head)
	}
	if got, want := len(scheduler.queue), writerCount/2; got != want {
		t.Fatalf("compacted queue len = %d, want %d", got, want)
	}
	if scheduler.queue[0] != firstRemaining {
		t.Fatal("compacted queue lost the first live writer")
	}
	backing := scheduler.queue[:oldLen]
	for i := len(scheduler.queue); i < oldLen; i++ {
		if backing[i] != nil {
			t.Fatalf("compacted queue retained dequeued writer at backing index %d", i)
		}
	}
}
