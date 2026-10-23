//go:build integration

package proxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSendPermissionTraceKeepsFreshBarrierAndSnapshotSeparate(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "success"
		if canceled {
			name = "canceled_barrier"
		}
		t.Run(name, func(t *testing.T) {
			db := boundaryDB(t)
			write := db.NewWriteBatch()
			if _, err := write.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1}); err != nil {
				t.Fatal(err)
			}
			if err := write.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := write.Close(); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			owner := newBoundaryCluster(2)
			owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			serving := New(owner, db)
			var handler func(context.Context, []byte) ([]byte, error)
			serving.RegisterRPCHandlers(func(id uint8, registered func(context.Context, []byte) ([]byte, error)) {
				if id == clusternet.RPCNodeSendPermissions {
					handler = registered
				}
			})
			if handler == nil {
				t.Fatal("permission service was not registered")
			}
			recorder, err := rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
			trace := recorder.Start(context.Background(), rpctrace.SideServer, uint16(clusternet.RPCNodeSendPermissions))
			origin := newBoundaryCluster(1)
			origin.rpc = func(ctx context.Context, _ multiraft.NodeID, raw []byte) ([]byte, error) {
				// Model the receiver's own RPC observation. The origin carries no
				// trace, so this does not manufacture a shared-cohort parent.
				return handler(rpctrace.With(ctx, trace), raw)
			}
			caller := New(origin, db)
			t.Cleanup(caller.CloseSendPermissionReads)
			t.Cleanup(serving.CloseSendPermissionReads)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan []PermissionMetadataReadResult, 1)
			go func() {
				done <- caller.ReadSendPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}})
			}()
			awaitBoundaryEvent(t, entered)
			want := rpctrace.ResultOK
			if canceled {
				want = rpctrace.ResultCanceled
				cancel()
			} else {
				unblock()
			}
			select {
			case results := <-done:
				if len(results) != 1 {
					t.Fatal("permission result alignment changed")
				}
				if canceled {
					if !errors.Is(results[0].Err, context.Canceled) {
						t.Fatal("barrier cancellation did not retain caller outcome")
					}
				} else if results[0].Err != nil || !results[0].Found || results[0].UserPolicy.SendBan != 1 {
					t.Fatal("fresh permission snapshot changed")
				}
			case <-time.After(time.Second):
				t.Fatal("permission lookup did not join")
			}
			sample := trace.Finish(want)
			stages := []rpctrace.Stage{rpctrace.StagePermissionAdmission, rpctrace.StagePermissionAuthority, rpctrace.StagePermissionBarrier}
			if !canceled {
				stages = append(stages, rpctrace.StagePermissionSnapshot)
			}
			if len(sample.Spans) != len(stages) {
				t.Fatalf("fresh permission stages missing or canceled barrier reached snapshot: %+v", sample.Spans)
			}
			for i, stage := range stages {
				result := rpctrace.ResultOK
				if canceled && (stage == rpctrace.StagePermissionAuthority || stage == rpctrace.StagePermissionBarrier) {
					result = want
				}
				if sample.Spans[i].Stage != stage || sample.Spans[i].Result != result || sample.Spans[i].Incomplete {
					t.Fatalf("fresh permission stage misclassified: %+v", sample.Spans)
				}
			}
		})
	}
}
