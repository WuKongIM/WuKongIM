//go:build integration

package app

import (
	"context"
	"testing"

	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestPresenceRecoveryCensusFollowsReopenedRuntime(t *testing.T) {
	for _, tc := range []struct {
		name        string
		diagnostics DiagnosticsConfig
		enabled     bool
	}{
		{"disabled", DiagnosticsConfig{RPCSampleEvery: 1}, false},
		{"unsampled", DiagnosticsConfig{Enabled: true}, false},
		{"enabled", DiagnosticsConfig{Enabled: true, RPCSampleEvery: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &recoveryWiringCluster{newFakePresenceCluster(1, nil)}
			a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}, Observability: ObservabilityConfig{MetricsEnabled: true, Diagnostics: tc.diagnostics, diagnosticsEnabledSet: true}}, WithCluster(cluster))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if a.presenceRecovery != nil {
					_ = a.presenceRecovery.Close(context.Background())
				}
			})
			r := a.presenceRecovery.(*presence.AuthorityRecovery)
			target := presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1}
			a.presenceDirectory.BecomeAuthority(target)
			_, _ = r.Endpoints(context.Background(), target, []string{"census-user"})
			if r.Stats().ResultCensusEnabled != tc.enabled || r.Stats().BatchCallsTotal != 1 {
				t.Fatalf("runtime census=%+v, result census enabled want=%v", r.Stats(), tc.enabled)
			}
			read := func() float64 {
				families, err := a.metrics.Gather()
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range families {
					if f.GetName() == "wukongim_presence_recovery_batch_calls_total" {
						return f.Metric[0].GetCounter().GetValue()
					}
				}
				t.Fatal("recovery census was not composed")
				return 0
			}
			if got := read(); got != 1 {
				t.Fatalf("actual runtime census=%v", got)
			}
			if err := r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			a.reopenPresenceRecovery()
			if a.presenceRecovery == r {
				t.Fatal("recovery not reopened")
			}
			if got := read(); got != 0 {
				t.Fatalf("collector retained old runtime: %v", got)
			}
		})
	}
}
