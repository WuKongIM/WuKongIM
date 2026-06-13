package client

import (
	"bytes"
	"context"
	"io"
	"net"
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
	// conn is the TCP stream captured when the request was admitted.
	conn net.Conn
	// pending is the tracker that owns entry for this request's session.
	pending *pendingTracker
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
		if i > 0 && req.conn != first.conn {
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

func (c *Client) runWriterLoop() {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: c.cfg.BatchMaxRecords,
		maxBytes:   c.cfg.BatchMaxBytes,
		maxWait:    c.cfg.BatchMaxWait,
	})

	var backlog []writeRequest
	for {
		if len(backlog) == 0 {
			req := <-c.writeCh
			if req.kind == writeKindClose {
				return
			}
			backlog = append(backlog, req)
		}

		backlog = c.collectWriterBacklog(backlog, collector)
		batch, rest := collector.collect(backlog)
		if len(batch) == 0 {
			backlog = rest
			continue
		}
		if batch[0].kind == writeKindClose {
			return
		}

		started := time.Now()
		bytesWritten, err := c.writeBatch(batch)
		c.finishWriteBatch(batch, err)
		c.observeSendBatch(countSendRequests(batch), bytesWritten, time.Since(started), err)
		backlog = rest
	}
}

func (c *Client) collectWriterBacklog(backlog []writeRequest, collector *writeBatchCollector) []writeRequest {
	if len(backlog) == 0 {
		return backlog
	}
	first := backlog[0]
	if first.kind != writeKindSend {
		return backlog
	}

	maxWait := collector.limits.maxWait
	if maxWait <= 0 {
		for {
			batch, rest := collector.collect(backlog)
			if len(rest) > 0 {
				return backlog
			}
			select {
			case req := <-c.writeCh:
				backlog = append(backlog, req)
			default:
				_ = batch
				return backlog
			}
		}
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		batch, rest := collector.collect(backlog)
		if len(rest) > 0 || len(batch) >= effectiveMaxRecords(collector.limits.maxRecords) {
			return backlog
		}

		select {
		case req := <-c.writeCh:
			backlog = append(backlog, req)
		case <-timer.C:
			return backlog
		}
	}
}

func effectiveMaxRecords(maxRecords int) int {
	if maxRecords <= 0 {
		return 1
	}
	return maxRecords
}

func (c *Client) writeBatch(batch []writeRequest) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}

	conn := batch[0].conn
	if conn == nil {
		var err error
		conn, err = c.currentConn()
		if err != nil {
			return 0, err
		}
	}

	var buf bytes.Buffer
	for _, req := range batch {
		switch req.kind {
		case writeKindSend:
			pkt, err := buildSendPacket(req.msg, req.msg.ClientSeq)
			if err != nil {
				return buf.Len(), err
			}
			sealed, err := c.crypto.sealSend(pkt)
			if err != nil {
				return buf.Len(), err
			}
			data, err := c.proto.EncodeFrame(sealed, frame.LatestVersion)
			if err != nil {
				return buf.Len(), err
			}
			buf.Write(data)
		case writeKindFrame:
			if req.frame == nil {
				return buf.Len(), ErrInvalidMessage
			}
			data, err := c.proto.EncodeFrame(req.frame, frame.LatestVersion)
			if err != nil {
				return buf.Len(), err
			}
			buf.Write(data)
		case writeKindClose:
			return buf.Len(), ErrClosed
		}
	}

	data := buf.Bytes()
	err := c.withDeadline(batchContext(batch), conn.SetWriteDeadline, func() error {
		for len(data) > 0 {
			n, err := conn.Write(data)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			data = data[n:]
		}
		return nil
	})
	if err != nil {
		return buf.Len(), err
	}
	return buf.Len(), nil
}

func batchContext(batch []writeRequest) context.Context {
	if len(batch) == 0 || batch[0].ctx == nil {
		return context.Background()
	}
	return batch[0].ctx
}

func (c *Client) finishWriteBatch(batch []writeRequest, err error) {
	for _, req := range batch {
		if req.kind == writeKindSend && err != nil {
			if req.pending != nil {
				req.pending.fail(req.entry, err)
			}
		}
		if req.result != nil {
			req.result <- err
		}
	}
}

func countSendRequests(batch []writeRequest) int {
	var n int
	for _, req := range batch {
		if req.kind == writeKindSend {
			n++
		}
	}
	return n
}
