package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestFormalChainCommandOwnsOneInProcessRunner(t *testing.T) {
	originalFactory := newFormalChainCommandRunner
	defer func() { newFormalChainCommandRunner = originalFactory }()
	compositions := 0
	newFormalChainCommandRunner = func(cli formalChainCLIConfig) (chatLifecycleCommandRunner, error) {
		compositions++
		if cli.config.Profile != chatlifecycle.ProfileFormal || cli.config.Mode != chatlifecycle.ModeSoak {
			t.Fatalf("formal-chain config = %+v", cli.config)
		}
		return immediateChatLifecycleRunner{}, nil
	}
	var stderr bytes.Buffer
	code := executeRoot([]string{
		"formal-chain", "chat-lifecycle",
		"--config", filepath.Join("..", "..", "configs", "wkbench", "chat-lifecycle", "formal.yaml"),
		"--output-dir", t.TempDir(),
	}, &stderr)
	if code != 0 || compositions != 1 {
		t.Fatalf("formal-chain code/compositions = %d/%d, stderr=%q", code, compositions, stderr.String())
	}
}

func TestFormalRuntimeEnvelopeRejectsBudgetAndExpiryRisk(t *testing.T) {
	cfg := chatlifecycle.FormalConfig()
	now := time.Unix(1_970_000_000, 0).UTC()
	t.Setenv("WK_CHAT_LEASE_CREATED_AT", now.Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(96*time.Hour).Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", "1500000000")
	t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", "1350000000")
	t.Setenv("WK_CHAT_BUDGET_COMMITTED_MICROS", "100000000")
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "1000000000")
	setFormalBudgetLineItems(t, []formalBudgetLineItem{{
		Kind: "postpaid_host_hour", Role: "all", Quantity: 384, CostMicros: 1_000_000_000,
	}})
	if err := validateFormalRuntimeEnvelope(cfg, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "1300000000")
	if err := validateFormalRuntimeEnvelope(cfg, now); err == nil {
		t.Fatal("budget above operational stop was accepted")
	}
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "1000000000")
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(81*time.Hour).Format(time.RFC3339Nano))
	if err := validateFormalRuntimeEnvelope(cfg, now); err == nil {
		t.Fatal("lease without cleanup reserve was accepted")
	}
}

func TestFormalRuntimeEnvelopeStopsOnObservedTrafficBudgetAndExpiry(t *testing.T) {
	cfg := chatlifecycle.FormalConfig()
	now := time.Unix(1_970_100_000, 0).UTC()
	t.Setenv("WK_CHAT_LEASE_CREATED_AT", now.Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(96*time.Hour).Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", "1500000000")
	t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", "1350000000")
	t.Setenv("WK_CHAT_BUDGET_COMMITTED_MICROS", "100000000")
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "1206000000")
	setFormalBudgetLineItems(t, []formalBudgetLineItem{
		{Kind: "postpaid_host_hour", Role: "all", Quantity: 384, CostMicros: 96_000_000},
		{Kind: "eip_public_egress_gib", Role: "load", Quantity: 100, CostMicros: 1_100_000_000},
		{Kind: "eip_retention_policy_risk_hour", Role: "load", Quantity: 96, CostMicros: 10_000_000},
	})
	guard, err := loadFormalRuntimeEnvelope(cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := guard.Observe(context.Background(), now.Add(time.Hour), 120*(1<<30))
	if err != nil || snapshot.Cause != chatlifecycle.RuntimeSafetyBudgetStop || snapshot.AccruedCostMicros < 1_350_000_000 {
		t.Fatalf("budget safety snapshot = %+v, %v", snapshot, err)
	}
	snapshot, err = guard.Observe(context.Background(), now.Add(95*time.Hour), 0)
	if err != nil || snapshot.Cause != chatlifecycle.RuntimeSafetyLeaseExpiryRisk || snapshot.LeaseRemaining != time.Hour {
		t.Fatalf("expiry safety snapshot = %+v, %v", snapshot, err)
	}
}

func TestRehearsalRuntimeEnvelopeUsesTwoHourRunAndCleanupReserve(t *testing.T) {
	cfg := chatlifecycle.RehearsalConfig()
	now := time.Unix(1_970_200_000, 0).UTC()
	t.Setenv("WK_CHAT_LEASE_CREATED_AT", now.Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(6*time.Hour).Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", "1500000000")
	t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", "1350000000")
	t.Setenv("WK_CHAT_BUDGET_COMMITTED_MICROS", "0")
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "60000000")
	setFormalBudgetLineItems(t, []formalBudgetLineItem{{
		Kind: "postpaid_host_hour", Role: "all", Quantity: 24, CostMicros: 60_000_000,
	}})
	if _, err := loadFormalRuntimeEnvelope(cfg, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(3*time.Hour-time.Second).Format(time.RFC3339Nano))
	if _, err := loadFormalRuntimeEnvelope(cfg, now); err == nil {
		t.Fatal("rehearsal Lease without the full cleanup reserve was accepted")
	}
}

func TestDirectRepairRuntimeEnvelopeAcceptsItsAuthenticatedLeaseBudget(t *testing.T) {
	cfg := chatlifecycle.RehearsalConfig()
	now := time.Unix(1_970_300_000, 0).UTC()
	t.Setenv("WK_CHAT_RUNTIME_ENVELOPE", "direct_repair")
	t.Setenv("WK_CHAT_LEASE_CREATED_AT", now.Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(6*time.Hour).Format(time.RFC3339Nano))
	t.Setenv("WK_CHAT_BUDGET_LIMIT_MICROS", "300000000")
	t.Setenv("WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS", "250000000")
	t.Setenv("WK_CHAT_BUDGET_COMMITTED_MICROS", "0")
	t.Setenv("WK_CHAT_BUDGET_ESTIMATED_MICROS", "41318000")
	setFormalBudgetLineItems(t, []formalBudgetLineItem{
		{Kind: "postpaid_host_hour", Role: "service", Quantity: 18, CostMicros: 18_306_000},
		{Kind: "postpaid_host_hour", Role: "load", Quantity: 6, CostMicros: 4_212_000},
		{Kind: "eip_public_egress_gib", Role: "load", Quantity: 11, CostMicros: 8_800_000},
		{Kind: "eip_retention_policy_risk_hour", Role: "load", Quantity: 10, CostMicros: 10_000_000},
	})
	if _, err := loadFormalRuntimeEnvelope(cfg, now); err != nil {
		t.Fatalf("loadFormalRuntimeEnvelope(direct repair) error = %v", err)
	}
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(70*time.Minute-time.Second).Format(time.RFC3339Nano))
	if _, err := loadFormalRuntimeEnvelope(cfg, now); err == nil {
		t.Fatal("direct repair Lease without its ten-minute run and cleanup reserve was accepted")
	}
	t.Setenv("WK_CHAT_LEASE_EXPIRES_AT", now.Add(6*time.Hour).Format(time.RFC3339Nano))
	if _, err := loadFormalRuntimeEnvelope(chatlifecycle.FormalConfig(), now); err == nil {
		t.Fatal("direct repair envelope was accepted for a formal run")
	}
}

func setFormalBudgetLineItems(t *testing.T, items []formalBudgetLineItem) {
	t.Helper()
	body, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WK_CHAT_BUDGET_LINE_ITEMS_BASE64", base64.StdEncoding.EncodeToString(body))
}

type immediateChatLifecycleRunner struct{}

func (immediateChatLifecycleRunner) Run(context.Context) (chatLifecycleRunResult, error) {
	return chatLifecycleRunResult{Verdict: chatlifecycle.VerdictSnapshot{
		Outcome: chatlifecycle.VerdictPass, Cause: chatlifecycle.VerdictCauseCompleted, Terminal: true,
	}}, nil
}

func (immediateChatLifecycleRunner) RequestStop() {}
