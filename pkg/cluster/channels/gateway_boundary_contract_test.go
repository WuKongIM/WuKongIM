package channels

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

func TestServiceGatewayFailsEveryEntryClosedWhileRuntimeIsAbsent(t *testing.T) {
	ctx := context.Background()
	var gateway *ServiceGateway

	if _, err := gateway.Append(ctx, ch.AppendRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("Append() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.AppendBatch(ctx, ch.AppendBatchRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("AppendBatch() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.handleForwardLastVisible(ctx, LastVisibleRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("handleForwardLastVisible() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.handleForwardConversationHeads(ctx, ConversationHeadsRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("handleForwardConversationHeads() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.handleForwardCommittedReads(ctx, CommittedReadsRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("handleForwardCommittedReads() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.HandlePull(ctx, channeltransport.PullRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandlePull() error = %v, want ErrNotReady", err)
	}
	if err := gateway.HandleAck(ctx, channeltransport.AckRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandleAck() error = %v, want ErrNotReady", err)
	}
	if err := gateway.HandlePullHint(ctx, channeltransport.PullHintRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandlePullHint() error = %v, want ErrNotReady", err)
	}
	if err := gateway.HandleNotify(ctx, channeltransport.NotifyRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandleNotify() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.HandlePullBatch(ctx, channeltransport.PullBatchRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandlePullBatch() error = %v, want ErrNotReady", err)
	}
	if _, err := gateway.HandlePullHintBatch(ctx, channeltransport.PullHintBatchRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandlePullHintBatch() error = %v, want ErrNotReady", err)
	}

	gateway = NewServiceGateway(nil)
	if gateway.Server() != gateway {
		t.Fatal("Server() must preserve the stable gateway identity")
	}
	gateway.Clear()
	if _, err := gateway.HandlePull(ctx, channeltransport.PullRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("HandlePull() after Clear error = %v, want ErrNotReady", err)
	}
}

func TestServiceGatewayRoutesReplicationBatchToReplacementRuntime(t *testing.T) {
	firstRuntime := &fakeBatchRuntime{
		pullBatch: channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{{
			Response: channeltransport.PullResponse{LeaderHW: 1},
		}}},
	}
	first, err := NewService(Config{Runtime: firstRuntime})
	if err != nil {
		t.Fatalf("NewService(first) error = %v", err)
	}
	secondRuntime := &fakeBatchRuntime{
		pullBatch: channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{{
			Response: channeltransport.PullResponse{LeaderHW: 2},
		}}},
		pullHintBatch: channeltransport.PullHintBatchResponse{Items: []channeltransport.PullHintBatchItemResult{{}}},
	}
	second, err := NewService(Config{Runtime: secondRuntime})
	if err != nil {
		t.Fatalf("NewService(second) error = %v", err)
	}
	gateway := NewServiceGateway(first)
	gateway.Replace(second)

	pullReq := channeltransport.PullBatchRequest{Items: []channeltransport.PullRequest{{
		ChannelID: ch.ChannelID{ID: "replacement", Type: 1},
	}}}
	pullResp, err := gateway.HandlePullBatch(context.Background(), pullReq)
	if err != nil {
		t.Fatalf("HandlePullBatch() error = %v", err)
	}
	if len(pullResp.Items) != 1 || pullResp.Items[0].Response.LeaderHW != 2 {
		t.Fatalf("HandlePullBatch() = %#v, want replacement result", pullResp)
	}
	if firstRuntime.pullBatchCalls != 0 || secondRuntime.pullBatchCalls != 1 {
		t.Fatalf("pull batch calls first=%d second=%d, want 0/1", firstRuntime.pullBatchCalls, secondRuntime.pullBatchCalls)
	}
	if len(secondRuntime.lastPullBatch.Items) != 1 || secondRuntime.lastPullBatch.Items[0].ChannelID != pullReq.Items[0].ChannelID {
		t.Fatalf("replacement pull request = %#v, want %#v", secondRuntime.lastPullBatch, pullReq)
	}

	hintReq := channeltransport.PullHintBatchRequest{Items: []channeltransport.PullHintRequest{{
		ChannelID: ch.ChannelID{ID: "replacement", Type: 1},
	}}}
	if _, err := gateway.HandlePullHintBatch(context.Background(), hintReq); err != nil {
		t.Fatalf("HandlePullHintBatch() error = %v", err)
	}
	if secondRuntime.pullHintBatchCalls != 1 || len(secondRuntime.lastPullHintBatch.Items) != 1 {
		t.Fatalf("replacement hint calls=%d request=%#v", secondRuntime.pullHintBatchCalls, secondRuntime.lastPullHintBatch)
	}
}

func TestServiceGatewayPreservesReplicationControlRequestIdentity(t *testing.T) {
	runtime := &recordingReplicationControlRuntime{}
	service, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	gateway := NewServiceGateway(service)
	id := ch.ChannelID{ID: "control", Type: 2}
	key := ch.ChannelKeyForID(id)

	if err := gateway.HandleAck(context.Background(), channeltransport.AckRequest{ChannelKey: key, MatchOffset: 11}); err != nil {
		t.Fatalf("HandleAck() error = %v", err)
	}
	if err := gateway.HandlePullHint(context.Background(), channeltransport.PullHintRequest{ChannelID: id}); err != nil {
		t.Fatalf("HandlePullHint() error = %v", err)
	}
	if err := gateway.HandleNotify(context.Background(), channeltransport.NotifyRequest{ChannelID: id}); err != nil {
		t.Fatalf("HandleNotify() error = %v", err)
	}
	if runtime.ackCalls != 1 || runtime.ack.ChannelKey != key || runtime.ack.MatchOffset != 11 {
		t.Fatalf("ack calls=%d request=%#v", runtime.ackCalls, runtime.ack)
	}
	if runtime.hintCalls != 1 || runtime.hint.ChannelID != id {
		t.Fatalf("hint calls=%d request=%#v", runtime.hintCalls, runtime.hint)
	}
	if runtime.notifyCalls != 1 || runtime.notify.ChannelID != id {
		t.Fatalf("notify calls=%d request=%#v", runtime.notifyCalls, runtime.notify)
	}
}

func TestServiceGatewayAttributesAppendObservationToCurrentRuntime(t *testing.T) {
	firstObserver := &appendStageObserver{}
	first, err := NewService(Config{Runtime: &fakeRuntime{}, Observer: firstObserver})
	if err != nil {
		t.Fatalf("NewService(first) error = %v", err)
	}
	secondObserver := &appendStageObserver{}
	second, err := NewService(Config{Runtime: &fakeRuntime{}, Observer: secondObserver})
	if err != nil {
		t.Fatalf("NewService(second) error = %v", err)
	}
	gateway := NewServiceGateway(first)

	gateway.observeAppendStage(appendStageForwardAppendRemote, nil, time.Millisecond)
	gateway.Replace(second)
	wantErr := errors.New("append rejected")
	gateway.observeAppendStage(appendStageForwardAppendRemote, wantErr, 2*time.Millisecond)

	if len(firstObserver.events) != 1 || firstObserver.events[0].result != "ok" {
		t.Fatalf("first observer events = %#v, want one successful event", firstObserver.events)
	}
	if len(secondObserver.events) != 1 || secondObserver.events[0].result != "err" {
		t.Fatalf("second observer events = %#v, want one failed event", secondObserver.events)
	}
}

func TestQuorumExchangeGatewaySwapsAtomicallyAndFailsClosedWhenCleared(t *testing.T) {
	first := &captureQuorumExchangeServer{result: replication.ExchangeBatchResult{Version: 1}}
	second := &captureQuorumExchangeServer{result: replication.ExchangeBatchResult{Version: 2}}
	gateway := NewQuorumExchangeGateway(first)
	batch := replication.ExchangeBatch{Version: replication.ExchangeVersion}

	got, err := gateway.Handle(context.Background(), 3, batch)
	if err != nil || got.Version != 1 || first.from != 3 {
		t.Fatalf("first Handle() = %#v, %v; captured from=%d", got, err, first.from)
	}
	gateway.Replace(second)
	got, err = gateway.Handle(context.Background(), 4, batch)
	if err != nil || got.Version != 2 || second.from != 4 || first.from != 3 {
		t.Fatalf("replacement Handle() = %#v, %v; first=%d second=%d", got, err, first.from, second.from)
	}
	gateway.Clear()
	if _, err := gateway.Handle(context.Background(), 5, batch); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("Handle() after Clear error = %v, want ErrNotReady", err)
	}
	var absent *QuorumExchangeGateway
	if _, err := absent.Handle(context.Background(), 5, batch); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("nil Handle() error = %v, want ErrNotReady", err)
	}
}

type recordingReplicationControlRuntime struct {
	fakeRuntime
	ack         channeltransport.AckRequest
	hint        channeltransport.PullHintRequest
	notify      channeltransport.NotifyRequest
	ackCalls    int
	hintCalls   int
	notifyCalls int
}

func (r *recordingReplicationControlRuntime) HandleAck(_ context.Context, req channeltransport.AckRequest) error {
	r.ackCalls++
	r.ack = req
	return nil
}

func (r *recordingReplicationControlRuntime) HandlePullHint(_ context.Context, req channeltransport.PullHintRequest) error {
	r.hintCalls++
	r.hint = req
	return nil
}

func (r *recordingReplicationControlRuntime) HandleNotify(_ context.Context, req channeltransport.NotifyRequest) error {
	r.notifyCalls++
	r.notify = req
	return nil
}
