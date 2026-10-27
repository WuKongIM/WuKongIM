package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// F06/F11: composition, not a generic sink, owns the opt-in allocation.
func TestAttemptLinkCompositionKeepsGenericDiagnosticsOffAndWiresExplicitCapability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			cfg := Config{Cluster: cluster.Config{NodeID: 7}, Observability: ObservabilityConfig{Diagnostics: DiagnosticsConfig{Enabled: true, SampleRate: 1, AttemptLinkEnable: enabled}}}
			cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)
			app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
			if err != nil {
				t.Fatal(err)
			}
			if sendtrace.GatewayAttemptEnabled() != enabled {
				t.Fatal("optional capability does not follow explicit app config")
			}
			key := sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: 1, ConnectFingerprint: [16]byte{2}}
			token := sendtrace.BeginGatewayAttempt(key, time.Unix(1, 0))
			result := app.QuerySendAttempts(context.Background(), []sendtrace.AttemptKey{key})
			if !enabled {
				if token.Valid() || result.Status != "unsupported" {
					t.Fatal("general diagnostics activated summary state")
				}
				return
			}
			if !token.Valid() || len(result.Results) != 1 || len(result.Results[0].Candidates) != 1 {
				t.Fatalf("explicit capability not composed: %+v", result)
			}
		})
	}
}
