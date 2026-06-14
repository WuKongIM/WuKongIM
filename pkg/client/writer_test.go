package client

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestWriterBatchCollectorMaxRecords(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 2,
		maxBytes:   1024,
		maxWait:    time.Millisecond,
	})
	reqs := []writeRequest{
		testSendWriteRequest(3),
		testSendWriteRequest(3),
		testSendWriteRequest(3),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if len(rest) != 1 {
		t.Fatalf("rest len = %d, want 1", len(rest))
	}
}

func TestWriterBatchCollectorMaxBytes(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   10,
		maxWait:    time.Millisecond,
	})
	reqs := []writeRequest{
		testSendWriteRequest(4),
		testSendWriteRequest(5),
		testSendWriteRequest(3),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if len(rest) != 1 {
		t.Fatalf("rest len = %d, want 1", len(rest))
	}
}

func TestWriterBatchCollectorZeroWaitReturnsReadyBatch(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   1024,
		maxWait:    0,
	})
	reqs := []writeRequest{
		testSendWriteRequest(1),
		testSendWriteRequest(1),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if len(rest) != 0 {
		t.Fatalf("rest len = %d, want 0", len(rest))
	}
}

func TestWriterBatchCollectorControlFrameBoundary(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   1024,
		maxWait:    time.Millisecond,
	})
	reqs := []writeRequest{
		testSendWriteRequest(1),
		{kind: writeKindFrame, frame: &frame.PingPacket{}},
		testSendWriteRequest(1),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 1 {
		t.Fatalf("batch len = %d, want 1", len(batch))
	}
	if len(rest) != 2 {
		t.Fatalf("rest len = %d, want 2", len(rest))
	}
	if rest[0].kind != writeKindFrame {
		t.Fatalf("rest[0].kind = %v, want %v", rest[0].kind, writeKindFrame)
	}
}

func TestWriterBatchCollectorFirstOversizedSend(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   5,
		maxWait:    time.Millisecond,
	})
	reqs := []writeRequest{
		testSendWriteRequest(8),
		testSendWriteRequest(1),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 1 {
		t.Fatalf("batch len = %d, want 1", len(batch))
	}
	if len(rest) != 1 {
		t.Fatalf("rest len = %d, want 1", len(rest))
	}
}

func TestWriterBatchCollectorFirstControlFrameIsSingleBatch(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   1024,
		maxWait:    time.Millisecond,
	})
	reqs := []writeRequest{
		{kind: writeKindFrame, frame: &frame.PingPacket{}},
		testSendWriteRequest(1),
	}

	batch, rest := collector.collect(reqs)

	if len(batch) != 1 {
		t.Fatalf("batch len = %d, want 1", len(batch))
	}
	if batch[0].kind != writeKindFrame {
		t.Fatalf("batch[0].kind = %v, want %v", batch[0].kind, writeKindFrame)
	}
	if len(rest) != 1 {
		t.Fatalf("rest len = %d, want 1", len(rest))
	}
}

func TestWriterBatchCollectorEmptyInput(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: 10,
		maxBytes:   1024,
		maxWait:    time.Millisecond,
	})

	batch, rest := collector.collect(nil)

	if batch != nil {
		t.Fatalf("batch = %#v, want nil", batch)
	}
	if rest != nil {
		t.Fatalf("rest = %#v, want nil", rest)
	}
}

func TestFilterCanceledBatchReusesStorage(t *testing.T) {
	c, err := New(Config{Addr: "bench"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	batch := []writeRequest{
		testSendWriteRequest(1),
		testSendWriteRequest(1),
	}

	ready := c.filterCanceledBatch(batch)

	if len(ready) != len(batch) {
		t.Fatalf("ready len = %d, want %d", len(ready), len(batch))
	}
	if &ready[0] != &batch[0] {
		t.Fatal("filterCanceledBatch did not reuse the input slice storage")
	}
}

func TestWriteBatchEncodeAllocationBudget(t *testing.T) {
	c, err := New(Config{
		Addr:            "discard",
		BatchMaxRecords: 64,
		BatchMaxBytes:   32 << 20,
		BatchMaxWait:    -1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	payload := benchmarkPayload(256)
	batch := make([]writeRequest, 64)
	conn := discardConn{}
	for i := range batch {
		seq := uint64(i + 1)
		pkt, err := buildSendPacket(benchmarkMessageWithSeq(payload, seq), seq)
		if err != nil {
			t.Fatalf("buildSendPacket() error = %v", err)
		}
		batch[i] = writeRequest{
			kind: writeKindSend,
			pkt:  pkt,
			ctx:  context.Background(),
			conn: conn,
		}
	}

	allocs := testing.AllocsPerRun(100, func() {
		if _, err := c.writeBatch(batch); err != nil {
			t.Fatalf("writeBatch() error = %v", err)
		}
	})
	if allocs > 500 {
		t.Fatalf("writeBatch allocations = %.0f, want <= 500", allocs)
	}
}

func testSendWriteRequest(payloadSize int) writeRequest {
	return writeRequest{
		kind: writeKindSend,
		msg: Message{
			Payload: make([]byte, payloadSize),
		},
	}
}
