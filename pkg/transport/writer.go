package transport

import (
	"net"
	"time"
)

type Priority uint8

const (
	PriorityRaft Priority = iota
	PriorityRPC
	PriorityBulk

	numPriorities = 3
	maxWriteBatch = 64
)

type writeItem struct {
	msgType uint8
	body    []byte
	done    chan error
}

type priorityWriter struct {
	conn     net.Conn
	queues   [numPriorities]chan writeItem
	observer ObserverHooks
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func newPriorityWriter(conn net.Conn, queueSizes [numPriorities]int, observer ObserverHooks) *priorityWriter {
	pw := &priorityWriter{
		conn:     conn,
		observer: observer,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	for i := range pw.queues {
		size := queueSizes[i]
		if size <= 0 {
			size = 64
		}
		pw.queues[i] = make(chan writeItem, size)
	}
	go pw.loop()
	return pw
}

func (pw *priorityWriter) enqueue(p Priority, item writeItem) error {
	select {
	case pw.queues[p] <- item:
		return nil
	case <-pw.stopCh:
		return ErrStopped
	default:
	}

	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case pw.queues[p] <- item:
		return nil
	case <-pw.stopCh:
		return ErrStopped
	case <-timer.C:
		return ErrQueueFull
	}
}

func (pw *priorityWriter) stop() {
	select {
	case <-pw.doneCh:
		return
	default:
	}
	select {
	case <-pw.stopCh:
	default:
		close(pw.stopCh)
	}
	<-pw.doneCh
}

func (pw *priorityWriter) loop() {
	defer close(pw.doneCh)

	batch := make([]writeItem, 0, maxWriteBatch)
	var bufs net.Buffers

	for {
		item, ok := pw.waitAny()
		if !ok {
			return
		}

		batch = append(batch[:0], item)
		batch = pw.drain(batch, PriorityRaft, maxWriteBatch-len(batch))
		batch = pw.drain(batch, PriorityRPC, maxWriteBatch-len(batch))
		batch = pw.drain(batch, PriorityBulk, maxWriteBatch-len(batch))

		bufs = bufs[:0]
		for i := range batch {
			writeFrame(&bufs, batch[i].msgType, batch[i].body)
		}

		_, err := bufs.WriteTo(pw.conn)
		for i := range batch {
			if batch[i].done != nil {
				batch[i].done <- err
			}
		}
		if err != nil {
			return
		}
		if hook := pw.observer.OnSend; hook != nil {
			for i := range batch {
				hook(batch[i].msgType, len(batch[i].body))
			}
		}
	}
}

func (pw *priorityWriter) waitAny() (writeItem, bool) {
	select {
	case item := <-pw.queues[PriorityRaft]:
		return item, true
	default:
	}

	select {
	case item := <-pw.queues[PriorityRaft]:
		return item, true
	case item := <-pw.queues[PriorityRPC]:
		return item, true
	case item := <-pw.queues[PriorityBulk]:
		return item, true
	case <-pw.stopCh:
		return writeItem{}, false
	}
}

func (pw *priorityWriter) drain(batch []writeItem, p Priority, max int) []writeItem {
	for len(batch) < cap(batch) && max > 0 {
		select {
		case item := <-pw.queues[p]:
			batch = append(batch, item)
			max--
		default:
			return batch
		}
	}
	return batch
}
