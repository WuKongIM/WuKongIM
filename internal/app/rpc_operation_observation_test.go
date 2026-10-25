package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestRPCOperationCompositionIsOwnerOnlyAndNilSafe(t *testing.T) {
	cluster := &recoveryWiringCluster{newFakePresenceCluster(1, nil)}
	_, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(cluster))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint8{accessnode.PresenceAuthorityRPCServiceID, accessnode.PresenceOwnerRPCServiceID} {
		classifier, ok := cluster.registeredHandlers[id].(interface{ TraceOperationFor([]byte) uint8 })
		if !ok {
			t.Fatalf("service%d lost optional operation declaration", id)
		}
		for _, op := range []byte{0, 1, 5, 7, 8, 9, 10, 11, 12} {
			want := uint8(0)
			if id == accessnode.PresenceOwnerRPCServiceID && (op == 7 || op == 9 || op == 10 || op == 11) {
				want = op
			}
			if classifier.TraceOperationFor([]byte{'W', 'K', 'V', 'P', 2, op}) != want {
				t.Fatalf("wrong operation service%d op%d", id, op)
			}
		}
	}
	var empty classifiedNodeRPCHandler
	if empty.CancelRunningFor(nil) || empty.TraceOperationFor(nil) != 0 {
		t.Fatal("nil classifier is not unknown")
	}
}

func TestRPCOperationSurvivesObserverStoreQueryAndJSON(t *testing.T) {
	store := obsdiagnostics.NewStore(obsdiagnostics.StoreOptions{Capacity: 2})
	observer := &rpcDiagnosticsObserver{store: store, nodeID: 2}
	sample := &rpctrace.Sample{TraceID: "fixture/1", Side: rpctrace.SideServer, ServiceID: 14, Operation: 11, Result: rpctrace.ResultCanceled}
	observer.ObserveTransport(transport.Event{Name: "rpc_trace", RPCTrace: sample})
	sample.Operation = 7
	first := store.Query(context.Background(), obsdiagnostics.Query{Stage: obsdiagnostics.StageRPCServer})
	if len(first.Events) != 1 || first.Events[0].RPC.Operation != 11 {
		t.Fatal("observer/store lost or aliased operation")
	}
	first.Events[0].RPC.Operation = 9
	second := store.Query(context.Background(), obsdiagnostics.Query{TraceID: "fixture/1"})
	data, err := json.Marshal(second)
	if err != nil || second.Events[0].RPC.Operation != 11 || !strings.Contains(string(data), `"operation":11`) {
		t.Fatal("query/JSON lost or aliased operation")
	}
}
