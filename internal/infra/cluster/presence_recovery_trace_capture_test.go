package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// The provider uses its real node client/codec and real recorder terminal path;
// actual transport Call-to-wire joining is covered by the transport integration.
type traceCaptureRecoveryNode struct {
	*recoveryCallNode
	recorder           *rpctrace.Recorder
	sequence           uint64
	untracedAfterFirst bool
}

func (n *traceCaptureRecoveryNode) CallRPC(ctx context.Context, owner uint64, service uint8, payload []byte) ([]byte, error) {
	n.sequence++
	if n.untracedAfterFirst && n.sequence > 1 {
		rpctrace.TerminalCaptureFrom(ctx).RecorderDisabled()
		return nil, transport.ErrTimeout
	}
	trace := n.recorder.Start(ctx, rpctrace.SideClient, uint16(service))
	trace.Bind([16]byte{1}, n.sequence)
	defer trace.Finish(rpctrace.ResultTimeout)
	return n.recoveryCallNode.CallRPC(ctx, owner, service, payload)
}

func TestRecoveryProviderFailureJoinsOnlyItsActualPageAndSharedPhysicalCall(t *testing.T) {
	for _, scenario := range []string{"shared_failure", "next_page_untraced", "next_owner_untraced"} {
		t.Run(scenario, func(t *testing.T) {
			laterUntraced := scenario != "shared_failure"
			node, calls := recoveryCallsFixture(1, 2)
			if scenario == "next_owner_untraced" {
				node, calls = recoveryCallsFixture(1, 2, 3)
			}
			recorder, err := rpctrace.NewRecorder(4)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := &traceCaptureRecoveryNode{recoveryCallNode: node, recorder: recorder, untracedAfterFirst: laterUntraced}
			if laterUntraced {
				remote := observationPages{OwnerRecovery: ownedRecoveryReader(t, 2, calls), page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
					next := start + 1
					if scenario == "next_owner_untraced" {
						next = len(c)
					}
					return recoveryCallsPage(2, 22, c, start, next, 0), nil
				}}
				node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}}
			} else {
				node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(context.Context, []byte) ([]byte, error) { return nil, transport.ErrTimeout }}}
			}
			owned, err := NewPresenceRecoveryOwners(wrapped, ownedRecoveryReader(t, 1, calls)).RecoverRouteCallsBounded(context.Background(), calls, presence.NewRecoveryResultBudget(100, 1<<20), nil)
			defer owned.Release()
			if err != nil {
				t.Fatal(err)
			}
			index := 0
			if scenario == "next_page_untraced" {
				index = 1
				if owned.Results[0][0].Err != nil {
					t.Fatal("completed earlier page was rejected")
				}
			}
			data, ok := presence.RecoveryProviderFailure(owned.Results[index][0].Err)
			wantOwner := uint64(2)
			if scenario == "next_owner_untraced" {
				wantOwner = 3
			}
			if !ok || data.Calls != 2 || data.OwnerNodeID != wantOwner {
				t.Fatalf("physical evidence=%+v/%t", data, ok)
			}
			got := data.OwnerRPCTrace
			if laterUntraced {
				if got.State != rpctrace.TerminalTraceRecorderDisabled || got.TraceID != "" {
					t.Fatalf("later untraced failure borrowed earlier page: %+v", got)
				}
			} else {
				if got.State != rpctrace.TerminalTraceBound || got.TraceID != "01000000000000000000000000000000/1" {
					t.Fatalf("failed physical call trace=%+v", got)
				}
				sibling, ok := presence.RecoveryProviderFailure(owned.Results[1][0].Err)
				if !ok || sibling.OwnerRPCTrace != got {
					t.Fatal("shared physical failure became separate logical traces")
				}
			}
		})
	}
}
