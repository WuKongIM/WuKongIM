package chatlifecyclerun

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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

func releasedPreclockFixture(t *testing.T) (OperatorInput, TrustedContext, ReleasedPreclockProof) {
	t.Helper()
	input := OperatorInput{SourceSHA: strings.Repeat("a", 40), Operator: "tangtaoit",
		CodexDiagnosticPubKey: testPublicKey(t), RequestID: "released-run"}
	trusted := TrustedContext{Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("b", 64),
		DeploymentPubKey: testPublicKey(t), Now: time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC),
		Attempt: 2, CommittedMicros: 74477600}
	proof := ReleasedPreclockProof{
		Schema:     "wukongim.chat_lifecycle.released_preclock_continuation/v1",
		Repository: trusted.Repository, RepositoryID: 638054601, RequestID: input.RequestID,
		SourceSHA: input.SourceSHA, Operator: input.Operator, CodexDiagnosticPublicKey: input.CodexDiagnosticPubKey,
		PredecessorRunID: 100, PredecessorWorkflowID: 200, PredecessorRunNumber: 32, PredecessorRunAttempt: 1,
		PredecessorControlSHA: strings.Repeat("c", 40), PredecessorLeaseID: "released-run-rehearsal-1",
		PredecessorPlanDigest: strings.Repeat("d", 64),
		PredecessorCreatedAt:  trusted.Now.Add(-time.Hour), PredecessorZeroObservedAt: trusted.Now.Add(-time.Minute),
		AcquireRunID: 101, ReleaseRunID: 102, CurrentRunID: 103, CurrentWorkflowID: 200,
		CurrentRunNumber: 33, CurrentRunAttempt: 1, ControlSHA: strings.Repeat("e", 40), NextAttempt: 2,
		CommittedMicros: trusted.CommittedMicros, HardLimitMicros: 1500000000, OperationalStopMicros: 1350000000,
		FailureClass: "unclassified_before_accepted_run_start", CostMethod: "full_admitted_quote_reserve",
		EvidenceManifestSHA256: strings.Repeat("f", 64),
	}
	return input, trusted, proof
}

func TestReleasedPreclockMaterializationKeepsOneRequestBudgetAndFixedLease(t *testing.T) {
	template := loadRepositoryTemplate(t)
	input, trusted, proof := releasedPreclockFixture(t)
	proof.CodexDiagnosticPublicKey += " diagnostic-owner"
	trusted.ReleasedPreclockProof = &proof
	plan, err := Materialize(template, input, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Attempt != 2 || plan.LeasePlan.LeaseID != "released-run-rehearsal-2" ||
		plan.LeasePlan.RequestID != input.RequestID || plan.LeasePlan.Provenance.SourceSHA != input.SourceSHA ||
		plan.LeasePlan.Budget.CommittedMicros != 74477600 || plan.LeasePlan.Budget.LimitMicros != 1500000000 ||
		plan.OperationalStopMicros != 1350000000 || plan.WorkloadDurationSeconds != 7200 ||
		plan.LeasePlan.ExpiresAt != trusted.Now.Add(12*time.Hour) || len(plan.LeasePlan.HostGroups) != 2 ||
		plan.LeasePlan.HostGroups[0].Count != 3 || plan.LeasePlan.HostGroups[1].Count != 1 {
		t.Fatalf("released continuation changed fixed policy or lost commitment: %+v", plan)
	}
	proof.FailureClass = "preflight_before_accepted_run_start"
	if _, err := Materialize(template, input, trusted); err != nil {
		t.Fatal(err)
	}
}

func TestReleasedPreclockMaterializationRejectsUnboundOrRepeatedProof(t *testing.T) {
	template := loadRepositoryTemplate(t)
	cases := map[string]func(*ReleasedPreclockProof, *OperatorInput, *TrustedContext){
		"missing-proof":      func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.ReleasedPreclockProof = nil },
		"attempt-one":        func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.Attempt = 1 },
		"attempt-three":      func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.Attempt = 3 },
		"commitment-reset":   func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.CommittedMicros = 0 },
		"commitment-changed": func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.CommittedMicros++ },
		"schema":             func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.Schema = "future/v2" },
		"request":            func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.RequestID = "other" },
		"source": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.SourceSHA = strings.Repeat("0", 40)
		},
		"repository":    func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.Repository = "other/repo" },
		"repository-id": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.RepositoryID = 0 },
		"operator":      func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.Operator = "other" },
		"diagnostic-key": func(p *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) {
			p.CodexDiagnosticPublicKey = c.DeploymentPubKey
		},
		"predecessor-id": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.PredecessorRunID = 0 },
		"parent-reuse": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.CurrentRunID = p.PredecessorRunID
		},
		"child-reuse":   func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.ReleaseRunID = p.AcquireRunID },
		"child-parent":  func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.AcquireRunID = p.CurrentRunID },
		"missing-child": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.ReleaseRunID = 0 },
		"workflow-id":   func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.CurrentWorkflowID++ },
		"missing-workflow": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.CurrentWorkflowID = 0
			p.PredecessorWorkflowID = 0
		},
		"nonadjacent": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.CurrentRunNumber++ },
		"number-zero": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorRunNumber = 0
			p.CurrentRunNumber = 1
		},
		"number-overflow": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorRunNumber = 1<<63 - 1
			p.CurrentRunNumber = -1 << 63
		},
		"predecessor-rerun": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.PredecessorRunAttempt++ },
		"current-rerun":     func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.CurrentRunAttempt++ },
		"chained-predecessor": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorLeaseID = "released-run-rehearsal-2"
		},
		"third-lease": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.NextAttempt = 3 },
		"predecessor-control": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorControlSHA = "invalid"
		},
		"current-control": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.ControlSHA = "" },
		"plan-digest":     func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.PredecessorPlanDigest = "" },
		"evidence-digest": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.EvidenceManifestSHA256 = "" },
		"zero-before-create": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorZeroObservedAt = p.PredecessorCreatedAt.Add(-time.Second)
		},
		"future-zero": func(p *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) {
			p.PredecessorZeroObservedAt = c.Now.Add(time.Second)
		},
		"missing-created": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorCreatedAt = time.Time{}
		},
		"non-utc": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.PredecessorCreatedAt = p.PredecessorCreatedAt.In(time.FixedZone("offset", 3600))
		},
		"zero-reserve": func(p *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) {
			p.CommittedMicros = 0
			c.CommittedMicros = 0
		},
		"stop-exhausted": func(p *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) {
			p.CommittedMicros = p.OperationalStopMicros
			c.CommittedMicros = p.CommittedMicros
		},
		"hard-limit": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.HardLimitMicros++ },
		"stop-limit": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.OperationalStopMicros++ },
		"runtime-failure": func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) {
			p.FailureClass = "runtime_failure"
		},
		"elapsed-cost":    func(p *ReleasedPreclockProof, _ *OperatorInput, _ *TrustedContext) { p.CostMethod = "elapsed_estimate" },
		"with-transition": func(_ *ReleasedPreclockProof, _ *OperatorInput, c *TrustedContext) { c.Transition = &StageTransition{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input, trusted, proof := releasedPreclockFixture(t)
			trusted.ReleasedPreclockProof = &proof
			mutate(&proof, &input, &trusted)
			if _, err := Materialize(template, input, trusted); err == nil {
				t.Fatal("unbound/replayed proof was accepted")
			}
		})
	}
	for _, stage := range []string{"repair-v1.json", "formal-v1.json"} {
		t.Run(stage, func(t *testing.T) {
			input, trusted, proof := releasedPreclockFixture(t)
			trusted.ReleasedPreclockProof = &proof
			trusted.Attempt = 1
			if _, err := Materialize(loadRepositoryTemplateNamed(t, stage), input, trusted); err == nil {
				t.Fatal("proof crossed stage boundary")
			}
		})
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
	if plan.Stage != StageRepair || plan.WorkloadDurationSeconds != 4500 ||
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

func TestRepairTemplateAcceptsOnlyDerivedCustomDurationEnvelope(t *testing.T) {
	template := loadRepositoryTemplateNamed(t, "repair-v1.json")
	template.WorkloadDurationSeconds = int64((72*time.Hour + 15*time.Minute) / time.Second)
	template.LeaseDurationSeconds = int64(77 * time.Hour / time.Second)
	body, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTemplate(bytes.NewReader(body)); err != nil {
		t.Fatalf("derived 72-hour repair envelope rejected: %v", err)
	}
	template.LeaseDurationSeconds++
	body, err = json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTemplate(bytes.NewReader(body)); err == nil {
		t.Fatal("repair template accepted a lease duration outside the derived envelope")
	}
}

func TestRepairTemplateAcceptsStructurallyValidExplicitBudget(t *testing.T) {
	template := loadRepositoryTemplateNamed(t, "repair-v1.json")
	template.Budget.HardLimitMicros = 450_000_000
	template.Budget.OperationalStopMicros = 430_000_000
	body, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTemplate(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("bounded explicit repair budget rejected before trusted binding: %v", err)
	}
	input := OperatorInput{
		SourceSHA: strings.Repeat("a", 40), Operator: "tangtaoit",
		CodexDiagnosticPubKey: testPublicKey(t), RequestID: "explicit-repair-budget",
	}
	trusted := TrustedContext{
		Repository: "WuKongIM/WuKongIM", BundleDigest: "sha256:" + strings.Repeat("b", 64),
		DeploymentPubKey: testPublicKey(t), Now: time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC), Attempt: 1,
	}
	if _, err := Materialize(decoded, input, trusted); err == nil {
		t.Fatal("explicit repair budget without independent trusted authorization was accepted")
	}
	trusted.AuthorizedRepairBudgetCNY = 450
	plan, err := Materialize(decoded, input, trusted)
	if err != nil {
		t.Fatalf("exact trusted explicit repair budget rejected: %v", err)
	}
	if plan.LeasePlan.Budget.LimitMicros != 450_000_000 || plan.OperationalStopMicros != 430_000_000 {
		t.Fatalf("materialized explicit budget = %+v", plan.LeasePlan.Budget)
	}
	trusted.AuthorizedRepairBudgetCNY = 451
	if _, err := Materialize(decoded, input, trusted); err == nil {
		t.Fatal("repair budget mismatching trusted authorization was accepted")
	}

	template.Budget.OperationalStopMicros = 429_000_000
	body, err = json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTemplate(bytes.NewReader(body)); err == nil {
		t.Fatal("explicit repair budget without the bounded stop reserve was accepted")
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
