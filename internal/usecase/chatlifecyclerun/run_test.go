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
		plan.WorkloadDurationSeconds != 7200 || plan.ReadinessTimeoutSeconds != 3600 ||
		plan.OperationalStopMicros != 1_350_000_000 || plan.LeasePlan.ExpiresAt != now.Add(6*time.Hour) {
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
	if err := cloudlease.ValidatePlan(lease, now); err != nil {
		t.Fatalf("materialized generic Lease Plan was rejected: %v", err)
	}
}

func TestRetryCarriesBudgetAndExcludesExactlyPriorOffer(t *testing.T) {
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
		t.Fatal("retry without an excluded placement was accepted")
	}
	exclusion := cloudlease.PlacementExclusion{Zone: "cn-hangzhou-h", ComputeType: "ecs.g8.large"}
	base.Attempt, base.CommittedMicros, base.ExcludedPlacement = 2, 50_000_000, &exclusion
	retry, err := Materialize(template, input, base)
	if err != nil {
		t.Fatal(err)
	}
	if retry.LeasePlan.Budget.CommittedMicros != 50_000_000 ||
		!reflect.DeepEqual(retry.LeasePlan.Placement.ExcludedOffers, []cloudlease.PlacementExclusion{exclusion}) ||
		retry.LeasePlan.LeaseID != "retry-run-rehearsal-2" {
		t.Fatalf("retry plan = %+v", retry)
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
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "..", "configs", "cloud", "chat-lifecycle", "rehearsal-v1.json"))
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
