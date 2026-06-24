package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
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
	app := New(Options{NodeLifecycle: writer})

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

func TestActivateNodeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	app := New(Options{NodeLifecycle: &nodeLifecycleWriterStub{}})
	if _, err := app.ActivateNode(context.Background(), ActivateNodeRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ActivateNode() invalid error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleUnavailable) {
		t.Fatalf("ActivateNode() missing writer error = %v, want ErrNodeLifecycleUnavailable", err)
	}
}

type nodeLifecycleWriterStub struct {
	joinReq        control.JoinNodeRequest
	joinResult     control.JoinNodeResult
	joinErr        error
	activateReq    control.ActivateNodeRequest
	activateResult control.ActivateNodeResult
	activateErr    error
}

func (s *nodeLifecycleWriterStub) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	s.joinReq = req
	return s.joinResult, s.joinErr
}

func (s *nodeLifecycleWriterStub) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	s.activateReq = req
	return s.activateResult, s.activateErr
}
