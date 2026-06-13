package client

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type writeKind uint8

const (
	writeKindSend writeKind = iota + 1
	writeKindFrame
	writeKindClose
)

// writeRequest is one item accepted by the single socket writer pump.
type writeRequest struct {
	// kind selects whether this request is a SEND, control frame, or close marker.
	kind writeKind
	// msg carries the high-level SEND payload before it is encoded as a frame.
	msg Message
	// frame carries non-SEND control frames such as PING or RECVACK.
	frame frame.Frame
	// entry tracks the pending SENDACK future for SEND requests.
	entry *pendingEntry
	// result receives the terminal write result for synchronous control requests.
	result chan error
	// ctx bounds waiting and socket writes for this request.
	ctx context.Context
}

// batchLimits bounds how many ready writer requests can be coalesced.
type batchLimits struct {
	// maxRecords caps the number of SEND records in one writer batch.
	maxRecords int
	// maxBytes caps the approximate payload bytes in one writer batch.
	maxBytes int
	// maxWait bounds collection time in the writer pump.
	maxWait time.Duration
}

// writeBatchCollector builds contiguous SEND batches without crossing control frames.
type writeBatchCollector struct {
	limits batchLimits
}

func newWriteBatchCollector(limits batchLimits) *writeBatchCollector {
	return &writeBatchCollector{limits: limits}
}

func (c *writeBatchCollector) collect(reqs []writeRequest) ([]writeRequest, []writeRequest) {
	if len(reqs) == 0 {
		return nil, nil
	}

	first := reqs[0]
	if first.kind != writeKindSend {
		return reqs[:1], reqs[1:]
	}

	maxRecords := c.limits.maxRecords
	if maxRecords <= 0 {
		maxRecords = 1
	}

	var totalBytes int
	for i, req := range reqs {
		if req.kind != writeKindSend {
			return reqs[:i], reqs[i:]
		}
		if i >= maxRecords {
			return reqs[:i], reqs[i:]
		}

		nextBytes := requestPayloadBytes(req)
		if c.limits.maxBytes > 0 && i > 0 && totalBytes+nextBytes > c.limits.maxBytes {
			return reqs[:i], reqs[i:]
		}
		totalBytes += nextBytes
	}

	return reqs, nil
}

func requestPayloadBytes(req writeRequest) int {
	if req.kind != writeKindSend {
		return 0
	}
	if len(req.msg.Payload) > 0 {
		return len(req.msg.Payload)
	}
	send, ok := req.frame.(*frame.SendPacket)
	if !ok || send == nil {
		return 0
	}
	return len(send.Payload)
}
