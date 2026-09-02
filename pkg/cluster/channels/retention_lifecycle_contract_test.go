package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestServiceRetentionDelegatesExactBoundaryToCapableRuntime(t *testing.T) {
	id := ch.ChannelID{ID: "retained", Type: 2}
	runtime := &retentionRuntimeFake{
		view:   ch.RetentionView{ChannelID: id, RetentionThroughSeq: 40, PhysicalRetentionThroughSeq: 30},
		result: ch.RetentionApplyResult{ChannelID: id, ThroughSeq: 50, DeletedThroughSeq: 35, Deleted: 5, More: true},
	}
	service, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	view, err := service.RetentionView(context.Background(), id)
	if err != nil || view.RetentionThroughSeq != 40 || runtime.viewID != id {
		t.Fatalf("RetentionView() = %#v, %v; captured id=%#v", view, err, runtime.viewID)
	}
	req := ch.RetentionApplyRequest{
		ChannelID:  id,
		ThroughSeq: 50,
		Options:    ch.RetentionApplyOptions{MaxTrimMessages: 100, MaxTrimBytes: 4096},
	}
	result, err := service.ApplyRetentionBoundary(context.Background(), req)
	if err != nil || result.Deleted != 5 || !result.More || runtime.applyReq != req {
		t.Fatalf("ApplyRetentionBoundary() = %#v, %v; captured request=%#v", result, err, runtime.applyReq)
	}
}

func TestServiceRetentionFailsClosedForRuntimeWithoutCapability(t *testing.T) {
	service, err := NewService(Config{Runtime: &fakeRuntime{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.RetentionView(context.Background(), ch.ChannelID{}); !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("RetentionView() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := service.ApplyRetentionBoundary(context.Background(), ch.RetentionApplyRequest{}); !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("ApplyRetentionBoundary() error = %v, want ErrInvalidConfig", err)
	}
}

func TestChannelLifecycleClosesOwnedServiceAndPropagatesFailure(t *testing.T) {
	closeErr := errors.New("close failed")
	runtime := &closingRuntimeFake{closeErr: closeErr}
	service, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	lifecycle := NewLifecycle(service)
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.Stop(context.Background()); !errors.Is(err, closeErr) {
		t.Fatalf("Stop() error = %v, want close failure", err)
	}
	if runtime.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", runtime.closeCalls)
	}
	var absent *Lifecycle
	if err := absent.Stop(context.Background()); err != nil {
		t.Fatalf("nil Stop() error = %v", err)
	}
}

type retentionRuntimeFake struct {
	fakeRuntime
	view     ch.RetentionView
	result   ch.RetentionApplyResult
	viewID   ch.ChannelID
	applyReq ch.RetentionApplyRequest
}

func (f *retentionRuntimeFake) RetentionView(_ context.Context, id ch.ChannelID) (ch.RetentionView, error) {
	f.viewID = id
	return f.view, nil
}

func (f *retentionRuntimeFake) ApplyRetentionBoundary(_ context.Context, req ch.RetentionApplyRequest) (ch.RetentionApplyResult, error) {
	f.applyReq = req
	return f.result, nil
}

type closingRuntimeFake struct {
	fakeRuntime
	closeErr   error
	closeCalls int
}

func (f *closingRuntimeFake) Close() error {
	f.closeCalls++
	return f.closeErr
}
