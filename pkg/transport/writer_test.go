package transport

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

func TestPriorityWriterWaitAnyPrefersRaft(t *testing.T) {
	pw := &priorityWriter{
		stopCh: make(chan struct{}),
	}
	for i := range pw.queues {
		pw.queues[i] = make(chan writeItem, 4)
	}
	pw.queues[PriorityRPC] <- writeItem{msgType: 2, body: []byte("rpc")}
	pw.queues[PriorityRaft] <- writeItem{msgType: 1, body: []byte("raft")}

	item, ok := pw.waitAny()
	if !ok {
		t.Fatal("waitAny() ok = false, want true")
	}
	if item.msgType != 1 {
		t.Fatalf("waitAny() msgType = %d, want 1", item.msgType)
	}
}

func TestPriorityWriterDrainRespectsPriorityOrder(t *testing.T) {
	pw := &priorityWriter{
		stopCh: make(chan struct{}),
	}
	for i := range pw.queues {
		pw.queues[i] = make(chan writeItem, 4)
	}
	pw.queues[PriorityRaft] <- writeItem{msgType: 1}
	pw.queues[PriorityRaft] <- writeItem{msgType: 2}
	pw.queues[PriorityRPC] <- writeItem{msgType: 3}
	pw.queues[PriorityBulk] <- writeItem{msgType: 4}

	batch := make([]writeItem, 0, 4)
	batch = pw.drain(batch, PriorityRaft, 4)
	batch = pw.drain(batch, PriorityRPC, 4-len(batch))
	batch = pw.drain(batch, PriorityBulk, 4-len(batch))

	got := []uint8{batch[0].msgType, batch[1].msgType, batch[2].msgType, batch[3].msgType}
	want := []uint8{1, 2, 3, 4}
	if !bytes.Equal(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
}

func TestPriorityWriterEnqueueReturnsQueueFull(t *testing.T) {
	pw := &priorityWriter{
		stopCh: make(chan struct{}),
	}
	for i := range pw.queues {
		pw.queues[i] = make(chan writeItem, 1)
	}
	pw.queues[PriorityRPC] <- writeItem{msgType: 1}

	err := pw.enqueue(PriorityRPC, writeItem{msgType: 2})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("enqueue() error = %v, want %v", err, ErrQueueFull)
	}
}

func TestPriorityWriterLoopWritesFramesAndSignalsDone(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	pw := newPriorityWriter(client, [numPriorities]int{4, 4, 4}, ObserverHooks{})
	defer pw.stop()

	done := make(chan error, 1)
	if err := pw.enqueue(PriorityRPC, writeItem{
		msgType: 7,
		body:    []byte("hello"),
		done:    done,
	}); err != nil {
		t.Fatalf("enqueue() error = %v", err)
	}

	msgType, body, release, err := readFrame(server)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	release()
	if msgType != 7 {
		t.Fatalf("msgType = %d, want 7", msgType)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("done error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for writer done signal")
	}
}
