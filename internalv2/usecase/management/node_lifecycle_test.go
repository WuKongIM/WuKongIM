package management

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestJoinNodeValidatesAndDelegates(t *testing.T) {
	writer := &nodeLifecycleWriterStub{
		joinResult: control.JoinNodeResult{
			Created:  true,
			Revision: 12,
			Node: control.Node{
				NodeID:         4,
				Addr:           "10.0.0.4:11110",
				JoinState:      control.NodeJoinStateJoining,
				CapacityWeight: 3,
			},
		},
	}
	app := New(Options{NodeLifecycle: writer})

	response, err := app.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           " 10.0.0.4:11110 ",
		CapacityWeight: 3,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !response.Created || response.NodeID != 4 || response.Addr != "10.0.0.4:11110" || response.JoinState != "joining" || response.Revision != 12 {
		t.Fatalf("JoinNode() = %#v, want created joining node response", response)
	}
	if writer.joinReq.NodeID != 4 || writer.joinReq.Name != "node-4" || writer.joinReq.Addr != "10.0.0.4:11110" || writer.joinReq.CapacityWeight != 3 {
		t.Fatalf("writer join request = %#v, want trimmed request", writer.joinReq)
	}
	if len(writer.joinReq.Roles) != 1 || writer.joinReq.Roles[0] != control.RoleData {
		t.Fatalf("writer roles = %#v, want data role", writer.joinReq.Roles)
	}
}

func TestJoinNodeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{}})
	for _, req := range []JoinNodeRequest{
		{NodeID: 0, Addr: "10.0.0.4:11110"},
		{NodeID: 4, Addr: "   "},
	} {
		if _, err := app.JoinNode(context.Background(), req); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("JoinNode(%#v) error = %v, want ErrInvalidArgument", req, err)
		}
	}
	if _, err := New(Options{}).JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "10.0.0.4:11110"}); !errors.Is(err, ErrNodeLifecycleUnavailable) {
		t.Fatalf("JoinNode() missing writer error = %v, want ErrNodeLifecycleUnavailable", err)
	}
}

func TestJoinNodeMapsControlLifecycleConflict(t *testing.T) {
	app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{joinErr: cv2.ErrNodeLifecycleConflict}})

	_, err := app.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "10.0.0.4:11110"})

	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("JoinNode() error = %v, want ErrNodeLifecycleConflict", err)
	}
}

func TestJoinNodeMapsControlAvailabilityErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "not leader", err: cv2.ErrNotLeader},
		{name: "not started", err: cv2.ErrNotStarted},
		{name: "stopped", err: cv2.ErrStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{joinErr: tt.err}})

			_, err := app.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "10.0.0.4:11110"})

			if !errors.Is(err, ErrNodeLifecycleUnavailable) {
				t.Fatalf("JoinNode() error = %v, want ErrNodeLifecycleUnavailable", err)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("JoinNode() error = %v, want wrapped %v", err, tt.err)
			}
		})
	}
}

func TestActivateNodeDelegates(t *testing.T) {
	writer := &nodeLifecycleWriterStub{
		activateResult: control.ActivateNodeResult{
			Changed:  true,
			Revision: 13,
			Node: control.Node{
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: control.NodeJoinStateActive,
			},
		},
	}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: activationReadinessSnapshot()},
		NodeLifecycle: writer,
		NodeReadiness: fakeNodeReadinessReader{resp: activationReadyReadiness()},
	})

	response, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !response.Changed || response.NodeID != 4 || response.JoinState != "active" || response.Revision != 13 {
		t.Fatalf("ActivateNode() = %#v, want changed active node response", response)
	}
	if writer.activateReq.NodeID != 4 {
		t.Fatalf("writer activate request = %#v, want node 4", writer.activateReq)
	}
}

func TestActivateNodeRejectsWhenReadinessIsUnknown(t *testing.T) {
	writer := &nodeLifecycleWriterStub{}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: activationReadinessSnapshot()},
		NodeLifecycle: writer,
		NodeReadiness: fakeNodeReadinessReader{resp: NodeReadiness{NodeID: 4, Unknown: true}},
	})

	_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})

	if !errors.Is(err, ErrNodeNotReadyForActivation) {
		t.Fatalf("ActivateNode() error = %v, want ErrNodeNotReadyForActivation", err)
	}
	if !strings.Contains(err.Error(), "readiness unknown") {
		t.Fatalf("ActivateNode() error = %v, want readiness detail", err)
	}
	if writer.activateReq.NodeID != 0 {
		t.Fatalf("writer activate request = %#v, want no activation write", writer.activateReq)
	}
}

func TestActivateNodeRequiresMirrorClusterAndRevision(t *testing.T) {
	writer := &nodeLifecycleWriterStub{}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{snapshot: activationReadinessSnapshot()},
		NodeLifecycle: writer,
		NodeReadiness: fakeNodeReadinessReader{resp: NodeReadiness{
			NodeID:            4,
			Reachable:         true,
			MirrorClusterID:   "cluster-a",
			ExpectedClusterID: "cluster-a",
			MirrorRevision:    21,
			TransportReady:    true,
			ControlReady:      true,
			RuntimeReady:      true,
		}},
	})

	_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})

	if !errors.Is(err, ErrNodeNotReadyForActivation) {
		t.Fatalf("ActivateNode() error = %v, want ErrNodeNotReadyForActivation", err)
	}
	if !strings.Contains(err.Error(), "mirror_revision=21 min_revision=22") {
		t.Fatalf("ActivateNode() error = %v, want revision detail", err)
	}
	if writer.activateReq.NodeID != 0 {
		t.Fatalf("writer activate request = %#v, want no activation write", writer.activateReq)
	}
}

func TestActivateNodeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{}})
	if _, err := app.ActivateNode(context.Background(), ActivateNodeRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ActivateNode() invalid error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleUnavailable) {
		t.Fatalf("ActivateNode() missing writer error = %v, want ErrNodeLifecycleUnavailable", err)
	}
}

func TestActivateNodeMapsControlLifecycleErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "missing", err: cv2.ErrNodeLifecycleNotFound, want: ErrNodeLifecycleNotFound},
		{name: "conflict", err: cv2.ErrNodeLifecycleConflict, want: ErrNodeLifecycleConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Options{
				Cluster:       fakeNodeSnapshotReader{snapshot: activationReadinessSnapshot()},
				NodeLifecycle: &nodeLifecycleWriterStub{activateErr: tt.err},
				NodeReadiness: fakeNodeReadinessReader{resp: activationReadyReadiness()},
			})

			_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})

			if !errors.Is(err, tt.want) {
				t.Fatalf("ActivateNode() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestActivateNodeMapsControlAvailabilityErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "not leader", err: cv2.ErrNotLeader},
		{name: "not started", err: cv2.ErrNotStarted},
		{name: "stopped", err: cv2.ErrStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Options{
				Cluster:       fakeNodeSnapshotReader{snapshot: activationReadinessSnapshot()},
				NodeLifecycle: &nodeLifecycleWriterStub{activateErr: tt.err},
				NodeReadiness: fakeNodeReadinessReader{resp: activationReadyReadiness()},
			})

			_, err := app.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})

			if !errors.Is(err, ErrNodeLifecycleUnavailable) {
				t.Fatalf("ActivateNode() error = %v, want ErrNodeLifecycleUnavailable", err)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("ActivateNode() error = %v, want wrapped %v", err, tt.err)
			}
		})
	}
}

func TestMarkNodeLeavingDelegates(t *testing.T) {
	writer := &nodeLifecycleWriterStub{
		leavingResult: control.MarkNodeLeavingResult{
			Changed:  true,
			Revision: 23,
			Node: control.Node{
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: control.NodeJoinStateLeaving,
			},
		},
	}
	app := New(Options{NodeLifecycle: writer})

	response, err := app.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !response.Changed || response.NodeID != 4 || response.JoinState != "leaving" || response.Revision != 23 {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node response", response)
	}
	if writer.leavingReq.NodeID != 4 {
		t.Fatalf("writer leaving request = %#v, want node 4", writer.leavingReq)
	}
}

func TestMarkNodeLeavingRejectsInvalidInputAndMissingWriter(t *testing.T) {
	app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{}})
	if _, err := app.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("MarkNodeLeaving() invalid error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleUnavailable) {
		t.Fatalf("MarkNodeLeaving() missing writer error = %v, want ErrNodeLifecycleUnavailable", err)
	}
}

func TestMarkNodeLeavingMapsControlLifecycleErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "missing", err: cv2.ErrNodeLifecycleNotFound, want: ErrNodeLifecycleNotFound},
		{name: "conflict", err: cv2.ErrNodeLifecycleConflict, want: ErrNodeLifecycleConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{leavingErr: tt.err}})

			_, err := app.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})

			if !errors.Is(err, tt.want) {
				t.Fatalf("MarkNodeLeaving() error = %v, want %v", err, tt.want)
			}
		})
	}
}

type nodeLifecycleWriterStub struct {
	joinReq        control.JoinNodeRequest
	joinResult     control.JoinNodeResult
	joinErr        error
	activateReq    control.ActivateNodeRequest
	activateResult control.ActivateNodeResult
	activateErr    error
	leavingReq     control.MarkNodeLeavingRequest
	leavingResult  control.MarkNodeLeavingResult
	leavingErr     error
	removedReq     control.MarkNodeRemovedRequest
	removedResult  control.MarkNodeRemovedResult
	removedErr     error
}

func (s *nodeLifecycleWriterStub) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	s.joinReq = req
	return s.joinResult, s.joinErr
}

func (s *nodeLifecycleWriterStub) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	s.activateReq = req
	return s.activateResult, s.activateErr
}

func (s *nodeLifecycleWriterStub) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	s.leavingReq = req
	return s.leavingResult, s.leavingErr
}

func (s *nodeLifecycleWriterStub) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	s.removedReq = req
	return s.removedResult, s.removedErr
}

type fakeNodeReadinessReader struct {
	req  uint64
	resp NodeReadiness
	err  error
}

func (f fakeNodeReadinessReader) NodeReadiness(_ context.Context, nodeID uint64) (NodeReadiness, error) {
	f.req = nodeID
	return f.resp, f.err
}

func activationReadinessSnapshot() control.Snapshot {
	return control.Snapshot{
		ClusterID: "cluster-a",
		Revision:  22,
		Nodes: []control.Node{{
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			Roles:     []control.Role{control.RoleData},
			Status:    control.NodeAlive,
			JoinState: control.NodeJoinStateJoining,
		}},
	}
}

func activationReadyReadiness() NodeReadiness {
	return NodeReadiness{
		NodeID:            4,
		ExpectedClusterID: "cluster-a",
		MirrorClusterID:   "cluster-a",
		MirrorRevision:    22,
		Reachable:         true,
		TransportReady:    true,
		ControlReady:      true,
		RuntimeReady:      true,
	}
}
