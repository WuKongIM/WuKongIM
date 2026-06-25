package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSetNodeDrainModeDelegatesToWriter(t *testing.T) {
	writer := &gatewayDrainWriterStub{summary: NodeRuntimeSummary{
		NodeID: 4, Draining: true, AcceptingNewSessions: false, GatewaySessions: 2, ActiveOnline: 1, PendingActivations: 1,
	}}
	app := New(Options{GatewayDrain: writer})

	resp, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if writer.nodeID != 4 || !writer.draining {
		t.Fatalf("writer node=%d draining=%v, want node 4 draining", writer.nodeID, writer.draining)
	}
	if !resp.Draining || resp.AcceptingNewSessions || resp.GatewaySessions != 2 || resp.ActiveOnline != 1 || resp.PendingActivations != 1 {
		t.Fatalf("response = %#v, want mapped drain summary", resp)
	}
}

func TestSetNodeDrainModeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	if _, err := New(Options{GatewayDrain: &gatewayDrainWriterStub{}}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("SetNodeDrainMode(invalid) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true}); !errors.Is(err, ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(missing writer) error = %v, want ErrNodeScaleInUnavailable", err)
	}
}

type gatewayDrainWriterStub struct {
	nodeID   uint64
	draining bool
	summary  NodeRuntimeSummary
	err      error
}

func (w *gatewayDrainWriterStub) SetNodeDrainMode(_ context.Context, nodeID uint64, draining bool) (NodeRuntimeSummary, error) {
	w.nodeID = nodeID
	w.draining = draining
	return w.summary, w.err
}
