package message

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSendBatchDelegatesToSubmitter(t *testing.T) {
	submitter := &recordingSubmitter{
		batchResults: []SendBatchItemResult{
			{Result: SendResult{MessageID: 10, MessageSeq: 2, Reason: ReasonSuccess}},
			{Err: ErrChannelBusy},
		},
	}
	app := New(Options{Submitter: submitter})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "b", ChannelType: 2, Payload: []byte("two")}},
	}

	results := app.SendBatch(items)

	if !reflect.DeepEqual(results, submitter.batchResults) {
		t.Fatalf("SendBatch() = %#v, want delegated results %#v", results, submitter.batchResults)
	}
	if len(submitter.batchItems) != 1 || !reflect.DeepEqual(submitter.batchItems[0], items) {
		t.Fatalf("delegated items = %#v, want original item batch", submitter.batchItems)
	}
}

func TestSendDelegatesToSubmitter(t *testing.T) {
	sendErr := errors.New("send failed")
	submitter := &recordingSubmitter{
		sendResult: SendResult{MessageID: 11, MessageSeq: 3, Reason: ReasonSuccess},
		sendErr:    sendErr,
	}
	app := New(Options{Submitter: submitter})
	ctx := context.Background()
	cmd := SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")}

	result, err := app.Send(ctx, cmd)

	if !errors.Is(err, sendErr) {
		t.Fatalf("Send() error = %v, want delegated error", err)
	}
	if result != submitter.sendResult {
		t.Fatalf("Send() result = %#v, want delegated result", result)
	}
	if submitter.sendCtx != ctx || !reflect.DeepEqual(submitter.sendCommand, cmd) {
		t.Fatalf("delegated send = (%v, %#v), want original context and command", submitter.sendCtx, submitter.sendCommand)
	}
}

func TestSendWithoutSubmitterReturnsRouteNotReady(t *testing.T) {
	app := New(Options{})

	_, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("Send() error = %v, want ErrRouteNotReady", err)
	}
	results := app.SendBatch([]SendBatchItem{{Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, ErrRouteNotReady) {
		t.Fatalf("SendBatch() = %#v, want item ErrRouteNotReady", results)
	}
}

type recordingSubmitter struct {
	sendCtx      context.Context
	sendCommand  SendCommand
	sendResult   SendResult
	sendErr      error
	batchItems   [][]SendBatchItem
	batchResults []SendBatchItemResult
}

func (s *recordingSubmitter) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	s.sendCtx = ctx
	s.sendCommand = cmd
	return s.sendResult, s.sendErr
}

func (s *recordingSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.batchItems = append(s.batchItems, append([]SendBatchItem(nil), items...))
	return append([]SendBatchItemResult(nil), s.batchResults...)
}
