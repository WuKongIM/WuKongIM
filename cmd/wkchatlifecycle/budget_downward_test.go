package main

import (
	"bytes"
	"encoding/json"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeDownwardBudgetCLI(t *testing.T) {
	key, deploy := commandPublicKey(t), commandPublicKey(t)
	base := []string{"materialize", "--template", filepath.Join("..", "..", "configs", "cloud", "chat-lifecycle", "rehearsal-v1.json"), "--source-sha", strings.Repeat("a", 40), "--operator", "tangtaoit", "--codex-diagnostic-pubkey", key, "--request-id", "budget-cli", "--repository", "WuKongIM/WuKongIM", "--bundle-digest", "sha256:" + strings.Repeat("b", 64), "--deployment-pubkey", deploy, "--now", "2026-10-21T12:00:00Z", "--attempt", "1"}
	for _, tc := range []struct {
		value string
		ok    bool
	}{{"1351044800", true}, {"1500000000", true}, {"150000001", true}, {"", false}, {"0", false}, {"-1", false}, {"150000000", false}, {"1500000001", false}, {"9223372036854775808", false}, {"1351044800.0", false}, {"1e9", false}, {"+1351044800", false}, {"01351044800", false}, {" 1351044800", false}, {"1351044800 ", false}} {
		t.Run(tc.value, func(t *testing.T) {
			var out bytes.Buffer
			c := newRootCommand(&out)
			c.SetArgs(append(append([]string{}, base...), "--request-budget-limit-micros", tc.value))
			err := c.Execute()
			if (err == nil) != tc.ok {
				t.Fatalf("budget %q: %v output=%s", tc.value, err, out.String())
			}
			if tc.ok {
				var p chatlifecyclerun.RunPlan
				if err := json.Unmarshal(out.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if p.OperationalStopMicros != p.LeasePlan.Budget.LimitMicros-150000000 {
					t.Fatal("wrong derived stop")
				}
			} else if strings.Contains(out.String(), `"lease_plan"`) {
				t.Fatal("rejected CLI emitted plan")
			}
		})
	}
}
