package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestWriteContextForBatchUsesEarliestDeadlineAndAnyCancellation(t *testing.T) {
	lateDeadline := time.Now().Add(2 * time.Hour)
	earlyDeadline := lateDeadline.Add(-time.Hour)
	late, cancelLate := context.WithDeadline(context.Background(), lateDeadline)
	defer cancelLate()
	early, cancelEarly := context.WithDeadline(context.Background(), earlyDeadline)
	defer cancelEarly()

	got, release := writeContextForBatch([]writeRequest{{ctx: late}, {ctx: early}})
	defer release()
	deadline, ok := got.Deadline()
	if !ok || !deadline.Equal(earlyDeadline) {
		t.Fatalf("batch deadline = %v, %t; want earliest %v", deadline, ok, earlyDeadline)
	}

	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())
	ctx, stop := writeContextForBatch([]writeRequest{{ctx: first}, {ctx: second}})
	cancelSecond()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("batch context error = %v, want %v", ctx.Err(), context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("batch context did not inherit cancellation from one request")
	}
	stop()
	cancelFirst()
}

func TestWriteContextForBatchDeduplicatesCancellationSignals(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	ctx, release := writeContextForBatch([]writeRequest{{ctx: requestCtx}, {ctx: requestCtx}, {ctx: nil}})
	cancelRequest()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("batch context did not inherit its unique request cancellation")
	}
	release()

	ctx, release = writeContextForBatch([]writeRequest{{ctx: nil}, {ctx: context.Background()}})
	if ctx.Done() != nil {
		t.Fatal("uncancelable batch unexpectedly gained a cancellation channel")
	}
	release()

	done := make(chan struct{})
	if got := appendUniqueDone(nil, done); len(got) != 1 {
		t.Fatalf("appendUniqueDone(new) len = %d, want 1", len(got))
	} else if got = appendUniqueDone(got, done); len(got) != 1 {
		t.Fatalf("appendUniqueDone(duplicate) len = %d, want 1", len(got))
	}
}

func TestWaitAnyDoneReturnsForRequestOrBatchStop(t *testing.T) {
	for _, tc := range []struct {
		name       string
		closeInput bool
	}{
		{name: "request cancellation", closeInput: true},
		{name: "batch stop", closeInput: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := make(chan struct{})
			stop := make(chan struct{})
			returned := make(chan struct{})
			go func() {
				waitAnyDone([]<-chan struct{}{input}, stop)
				close(returned)
			}()
			if tc.closeInput {
				close(input)
			} else {
				close(stop)
			}
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("waitAnyDone() did not return for selected signal")
			}
		})
	}
}

func TestWriterSizeAccountingUsesImmutablePreparedPacket(t *testing.T) {
	pkt := &frame.SendPacket{
		Payload:     []byte("prepared"),
		ClientMsgNo: "message-no",
		ChannelID:   "group-1",
		MsgKey:      "key",
		Topic:       "topic",
		StreamNo:    "stream",
	}
	tests := []struct {
		name string
		req  writeRequest
		want int
	}{
		{name: "non-send payload", req: writeRequest{kind: writeKindFrame, frame: &frame.PingPacket{}}, want: 0},
		{name: "prepared payload", req: writeRequest{kind: writeKindSend, pkt: pkt, msg: Message{Payload: []byte("mutable-fallback")}}, want: len(pkt.Payload)},
		{name: "message payload fallback", req: writeRequest{kind: writeKindSend, msg: Message{Payload: []byte("message")}}, want: len("message")},
		{name: "frame payload fallback", req: writeRequest{kind: writeKindSend, frame: &frame.SendPacket{Payload: []byte("frame")}}, want: len("frame")},
		{name: "missing send payload", req: writeRequest{kind: writeKindSend, frame: &frame.PongPacket{}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestPayloadBytes(tt.req); got != tt.want {
				t.Fatalf("requestPayloadBytes() = %d, want %d", got, tt.want)
			}
		})
	}

	preparedHint := writeRequestEncodedSizeHint(writeRequest{kind: writeKindSend, pkt: pkt})
	wantPreparedHint := 64 + len(pkt.Payload) + len(pkt.ClientMsgNo) + len(pkt.ChannelID) + len(pkt.MsgKey) + len(pkt.Topic) + len(pkt.StreamNo)
	if preparedHint != wantPreparedHint {
		t.Fatalf("prepared encoded size hint = %d, want %d", preparedHint, wantPreparedHint)
	}
	msg := Message{Payload: []byte("body"), ClientMsgNo: "m", ChannelID: "c", Topic: "t"}
	if got, want := writeRequestEncodedSizeHint(writeRequest{kind: writeKindSend, msg: msg}), 64+len(msg.Payload)+len(msg.ClientMsgNo)+len(msg.ChannelID)+len(msg.Topic); got != want {
		t.Fatalf("message encoded size hint = %d, want %d", got, want)
	}
	if got := writeRequestEncodedSizeHint(writeRequest{kind: writeKindFrame}); got != 64 {
		t.Fatalf("control encoded size hint = %d, want 64", got)
	}
	if got := writeRequestEncodedSizeHint(writeRequest{kind: writeKindClose}); got != 0 {
		t.Fatalf("close encoded size hint = %d, want 0", got)
	}
}

func TestWriteBatchRejectsInvalidRequestsBeforeSocketWrite(t *testing.T) {
	c := newDisconnectedClientOrFatal(t)
	if bytes, err := c.writeBatch(nil); err != nil || bytes != 0 {
		t.Fatalf("writeBatch(nil) = %d, %v", bytes, err)
	}
	if bytes, err := c.writeBatch([]writeRequest{{kind: writeKindFrame}}); !errors.Is(err, ErrNotConnected) || bytes != 0 {
		t.Fatalf("writeBatch(no connection) = %d, %v; want %v", bytes, err, ErrNotConnected)
	}

	conn := discardConn{}
	tests := []struct {
		name string
		req  writeRequest
		want error
	}{
		{name: "nil control frame", req: writeRequest{kind: writeKindFrame, conn: conn}, want: ErrInvalidMessage},
		{name: "close marker", req: writeRequest{kind: writeKindClose, conn: conn}, want: ErrClosed},
		{name: "invalid unprepared send", req: writeRequest{kind: writeKindSend, conn: conn, msg: Message{ClientSeq: 1}}, want: ErrInvalidMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.writeBatch([]writeRequest{tt.req}); !errors.Is(err, tt.want) {
				t.Fatalf("writeBatch() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFilterCanceledBatchCompletesOnlyCanceledRequests(t *testing.T) {
	c := newDisconnectedClientOrFatal(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	batch := []writeRequest{
		{kind: writeKindFrame, ctx: canceled, result: result},
		{kind: writeKindFrame, ctx: context.Background(), frame: &frame.PingPacket{}},
	}
	ready := c.filterCanceledBatch(batch)
	if len(ready) != 1 || ready[0].frame.GetFrameType() != frame.PING {
		t.Fatalf("ready batch = %#v, want only uncanceled PING", ready)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled write result = %v, want %v", err, context.Canceled)
		}
	default:
		t.Fatal("canceled write request was not completed")
	}
}

func TestEffectiveMaxRecordsNeverReturnsZero(t *testing.T) {
	if got := effectiveMaxRecords(0); got != 1 {
		t.Fatalf("effectiveMaxRecords(0) = %d, want 1", got)
	}
	if got := effectiveMaxRecords(-10); got != 1 {
		t.Fatalf("effectiveMaxRecords(-10) = %d, want 1", got)
	}
	if got := effectiveMaxRecords(17); got != 17 {
		t.Fatalf("effectiveMaxRecords(17) = %d, want 17", got)
	}
}
