package chatlifecyclerun

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestRepositoryRehearsalTemplateMaterializesExactFourHostLease(t *testing.T) {
	template := loadRepositoryTemplate(t)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	plan, err := Materialize(template, OperatorInput{
		SourceSHA: strings.Repeat("a", 40), Operator: "tangtaoit",
		CodexDiagnosticPubKey: testPublicKey(t), RequestID: "chat-run-20260807",
	}, TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("b", 64),
		DeploymentPubKey: testPublicKey(t), Now: now, Attempt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != RunPlanSchemaV1 || plan.Stage != StageRehearsal || plan.Attempt != 1 ||
		plan.WorkloadDurationSeconds != 7200 || plan.ReadinessTimeoutSeconds != int64((2*time.Hour)/time.Second) ||
		plan.OperationalStopMicros != 1_350_000_000 || plan.LeasePlan.ExpiresAt != now.Add(12*time.Hour) {
		t.Fatalf("run plan = %+v", plan)
	}
	lease := plan.LeasePlan
	if lease.Budget != (cloudlease.Budget{Currency: "CNY", LimitMicros: 1_500_000_000, OperationalStopMicros: 1_350_000_000}) ||
		len(lease.HostGroups) != 2 || lease.HostGroups[0].Count != 3 || lease.HostGroups[1].Count != 1 ||
		lease.HostGroups[0].Compute.VCPUs != 4 || lease.HostGroups[0].Compute.MemoryBytes != 8<<30 ||
		lease.HostGroups[0].DataDisks[0].SizeBytes != 500<<30 || lease.HostGroups[1].DataDisks[0].SizeBytes != 200<<30 ||
		!lease.HostGroups[1].PublicIPv4 || lease.HostGroups[1].PeakBandwidthMbps != 20 || len(lease.Network.InitialAccess) != 2 ||
		lease.Network.InitialAccess[0].SourcePrefix.String() != "0.0.0.0/0" || len(plan.BootstrapAccess.AuthorizedKeys) != 2 {
		t.Fatalf("lease plan = %+v", lease)
	}
	if !reflect.DeepEqual(lease.Tags, map[string]string{"stage": StageRehearsal}) {
		t.Fatalf("lease tags = %#v, want one stage tag within the provider resource limit", lease.Tags)
	}
	if err := cloudlease.ValidatePlan(lease, now); err != nil {
		t.Fatalf("materialized generic Lease Plan was rejected: %v", err)
	}
}

func TestSecondProcurementAttemptIsRejected(t *testing.T) {
	template := loadRepositoryTemplate(t)
	input := OperatorInput{SourceSHA: strings.Repeat("c", 40), Operator: "tangtaoit", CodexDiagnosticPubKey: testPublicKey(t), RequestID: "retry-run"}
	base := TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("d", 64),
		DeploymentPubKey: testPublicKey(t), Now: time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC),
	}
	if _, err := Materialize(template, input, TrustedContext{
		Repository: base.Repository, BundleDigest: base.BundleDigest, DeploymentPubKey: base.DeploymentPubKey,
		Now: base.Now, Attempt: 2, CommittedMicros: 50_000_000,
	}); err == nil {
		t.Fatal("second procurement attempt was accepted")
	}
}

func TestRepositoryFormalTemplateRequiresReleasedPassingRehearsalAndCarriesAggregateBudget(t *testing.T) {
	template := loadRepositoryTemplateNamed(t, "formal-v1.json")
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	input := OperatorInput{
		SourceSHA: strings.Repeat("1", 40), Operator: "tangtaoit",
		CodexDiagnosticPubKey: testPublicKey(t), RequestID: "chat-run-20260808",
	}
	trusted := TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("2", 64),
		DeploymentPubKey: testPublicKey(t), Now: now, Attempt: 1, CommittedMicros: 75_000_000,
		Transition: &StageTransition{
			Schema: FormalTransitionSchemaV1, FromStage: StageRehearsal, Outcome: "rehearsal_pass",
			RequestID: input.RequestID, SourceSHA: input.SourceSHA,
			BundleDigest: "sha256:" + strings.Repeat("2", 64), CommittedMicros: 75_000_000,
			CodexDiagnosticPubKey: input.CodexDiagnosticPubKey, ZeroInventory: true,
		},
	}
	plan, err := Materialize(template, input, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stage != StageFormal || plan.WorkloadDurationSeconds != int64((72*time.Hour)/time.Second) ||
		plan.ReadinessTimeoutSeconds != int64((2*time.Hour)/time.Second) ||
		plan.LeasePlan.ExpiresAt != now.Add(96*time.Hour) || plan.LeasePlan.LeaseID != "chat-run-20260808-formal-1" ||
		plan.LeasePlan.Budget.CommittedMicros != 75_000_000 || plan.LeasePlan.Tags["stage"] != StageFormal {
		t.Fatalf("formal run plan = %+v", plan)
	}

	missing := trusted
	missing.Transition = nil
	if _, err := Materialize(template, input, missing); err == nil {
		t.Fatal("formal run without a typed rehearsal transition was accepted")
	}
	notReleased := *trusted.Transition
	notReleased.ZeroInventory = false
	missing.Transition = &notReleased
	if _, err := Materialize(template, input, missing); err == nil {
		t.Fatal("formal run before rehearsal zero-inventory proof was accepted")
	}
	wrongSource := *trusted.Transition
	wrongSource.SourceSHA = strings.Repeat("3", 40)
	missing.Transition = &wrongSource
	if _, err := Materialize(template, input, missing); err == nil {
		t.Fatal("formal run with different source provenance was accepted")
	}
	wrongCodexIdentity := *trusted.Transition
	wrongCodexIdentity.CodexDiagnosticPubKey = testPublicKey(t)
	missing.Transition = &wrongCodexIdentity
	if _, err := Materialize(template, input, missing); err == nil {
		t.Fatal("formal run with a different Codex diagnostic identity was accepted")
	}
}

func TestRepositoryRepairTemplateCreatesReusableLeaseWithoutFormalTransition(t *testing.T) {
	template := loadRepositoryTemplateNamed(t, "repair-v1.json")
	now := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	input := OperatorInput{
		SourceSHA: strings.Repeat("4", 40), Operator: "tangtaoit",
		CodexDiagnosticPubKey: testPublicKey(t), RequestID: "repair-run-20260822",
	}
	trusted := TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("5", 64),
		DeploymentPubKey: testPublicKey(t), Now: now, Attempt: 1,
	}
	plan, err := Materialize(template, input, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stage != StageRepair || plan.WorkloadDurationSeconds != 600 ||
		plan.ReadinessTimeoutSeconds != 1800 || plan.LeasePlan.ExpiresAt != now.Add(6*time.Hour) ||
		plan.LeasePlan.LeaseID != "repair-run-20260822-repair-1" ||
		plan.LeasePlan.Budget.LimitMicros != 300_000_000 ||
		plan.LeasePlan.Budget.OperationalStopMicros != 250_000_000 ||
		plan.LeasePlan.Tags["stage"] != StageRepair {
		t.Fatalf("repair run plan = %+v", plan)
	}
	transition := &StageTransition{
		Schema: FormalTransitionSchemaV1, FromStage: StageRehearsal, Outcome: "rehearsal_pass",
		RequestID: input.RequestID, SourceSHA: input.SourceSHA, BundleDigest: trusted.BundleDigest,
		CodexDiagnosticPubKey: input.CodexDiagnosticPubKey, CommittedMicros: 1, ZeroInventory: true,
	}
	trusted.Transition = transition
	if _, err := Materialize(template, input, trusted); err == nil {
		t.Fatal("repair template accepted a formal-transition receipt")
	}
}

func TestTemplateAndOperatorSurfaceFailClosed(t *testing.T) {
	template := loadRepositoryTemplate(t)
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "configs", "cloud", "chat-lifecycle", "rehearsal-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTemplate(strings.NewReader(strings.Replace(string(body), "\n}", ",\n  \"runtime_override\": true\n}", 1))); err == nil {
		t.Fatal("unknown template field was accepted")
	}
	input := OperatorInput{SourceSHA: strings.Repeat("e", 40), Operator: "somebody", CodexDiagnosticPubKey: testPublicKey(t), RequestID: "surface-run"}
	trusted := TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("f", 64),
		DeploymentPubKey: testPublicKey(t), Now: time.Date(2026, 8, 7, 14, 0, 0, 0, time.UTC), Attempt: 1,
	}
	if _, err := Materialize(template, input, trusted); err == nil {
		t.Fatal("operator other than tangtaoit was accepted")
	}
	input.Operator = "tangtaoit"
	trusted.Repository = "WuKongIM/WuKongIM/extra"
	if _, err := Materialize(template, input, trusted); err == nil {
		t.Fatal("malformed trusted repository identity was accepted")
	}
	trusted.Repository = "WuKongIM/WuKongIM"
	template.Compute.VCPUs = 8
	if _, err := Materialize(template, input, trusted); err == nil {
		t.Fatal("unreviewed infrastructure override was accepted")
	}
}

func loadRepositoryTemplate(t *testing.T) Template {
	return loadRepositoryTemplateNamed(t, "rehearsal-v1.json")
}

func loadRepositoryTemplateNamed(t *testing.T, name string) Template {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "..", "configs", "cloud", "chat-lifecycle", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	template, err := DecodeTemplate(file)
	if err != nil {
		t.Fatal(err)
	}
	return template
}

func testPublicKey(t *testing.T) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}
