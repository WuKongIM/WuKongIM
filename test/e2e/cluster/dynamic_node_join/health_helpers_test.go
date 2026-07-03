//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

func eventuallyNodeSchedulable(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last suite.NodeDTO
	var lastErr error
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		list, err := manager.ListNodes(reqCtx)
		cancelReq()
		if err == nil {
			for _, item := range list.Items {
				if item.NodeID != nodeID {
					continue
				}
				last = item
				if item.Membership.Schedulable && item.Health.Fresh && item.Health.Status == "alive" && item.Health.RuntimeReady {
					return
				}
				lastErr = fmt.Errorf("schedulable=%t fresh=%t status=%s runtime_ready=%t",
					item.Membership.Schedulable,
					item.Health.Fresh,
					item.Health.Status,
					item.Health.RuntimeReady,
				)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not become schedulable: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
