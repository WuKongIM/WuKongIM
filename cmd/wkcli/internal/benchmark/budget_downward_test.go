package benchmark

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"strconv"
	"testing"
	"time"
)

func TestDownwardRuntimeBudgetEnvelope(t *testing.T) {
	now := time.Unix(1970300000, 0).UTC()
	for name, cfg := range map[string]chatlifecycle.Config{"rehearsal": chatlifecycle.RehearsalConfig(), "formal": chatlifecycle.FormalConfig()} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WK_CHAT_RUNTIME_ENVELOPE", "")
			t.Setenv("WK_CHAT_LEASE_CREATED_AT", now.Format(time.RFC3339))
			t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(96*time.Hour).Format(time.RFC3339))
			t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", "1351044800")
			t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", "1201044800")
			t.Setenv("WK_CHAT_BUDGET_COMMITTED_MICROS", "1200044800")
			t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "1000000")
			setFormalBudgetLineItems(t, []formalBudgetLineItem{{Kind: "eip_retention_policy_risk_hour", Role: "load", Quantity: 96, CostMicros: 999999}, {Kind: "eip_public_egress_gib", Role: "load", Quantity: 1, CostMicros: 1}})
			guard, err := loadFormalRuntimeEnvelope(cfg, now)
			if err != nil {
				t.Fatal(err)
			}
			before, err := guard.Observe(context.Background(), now, 0)
			if err != nil || before.AccruedCostMicros != 1201044799 || before.Cause == chatlifecycle.RuntimeSafetyBudgetStop {
				t.Fatalf("before boundary: %+v / %v", before, err)
			}
			at, err := guard.Observe(context.Background(), now, 1<<30)
			if err != nil || at.AccruedCostMicros != 1201044800 || at.Cause != chatlifecycle.RuntimeSafetyBudgetStop {
				t.Fatalf("stop boundary: %+v / %v", at, err)
			}
			for _, pair := range [][2]int64{{1500000001, 1350000001}, {150000000, 0}, {1351044800, 1201044801}, {0, 0}} {
				t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", strconv.FormatInt(pair[0], 10))
				t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", strconv.FormatInt(pair[1], 10))
				if _, err := loadFormalRuntimeEnvelope(cfg, now); err == nil {
					t.Fatalf("invalid envelope %v accepted", pair)
				}
			}
		})
	}
}
