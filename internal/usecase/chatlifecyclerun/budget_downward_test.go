package chatlifecyclerun

import (
	"encoding/json"
	"testing"
)

func TestDownwardBudgetMaterialization(t *testing.T) {
	input, trusted, proof := releasedPreclockFixture(t)
	trusted.Attempt, trusted.CommittedMicros = 1, 0
	template := loadRepositoryTemplate(t)
	original, err := Materialize(template, input, trusted)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{1500000000, 1351044800, 150000001} {
		t.Run(stringBudget(limit), func(t *testing.T) {
			current := trusted
			current.RequestBudgetLimitMicros = &limit
			plan, err := Materialize(template, input, current)
			if err != nil {
				t.Fatal(err)
			}
			if plan.LeasePlan.Budget.LimitMicros != limit || plan.OperationalStopMicros != limit-150000000 || plan.LeasePlan.Budget.OperationalStopMicros != plan.OperationalStopMicros || plan.LeasePlan.Budget.CommittedMicros != 0 || plan.Attempt != 1 {
				t.Fatalf("wrong envelope: %+v", plan)
			}
			if limit == 1500000000 {
				a, _ := json.Marshal(original)
				b, _ := json.Marshal(plan)
				if string(a) != string(b) {
					t.Fatal("explicit default changed the plan")
				}
			}
		})
	}
	lower := int64(1351044800)
	for name, mutate := range map[string]func(*Template, *TrustedContext){
		"zero":          func(_ *Template, c *TrustedContext) { v := int64(0); c.RequestBudgetLimitMicros = &v },
		"negative":      func(_ *Template, c *TrustedContext) { v := int64(-1); c.RequestBudgetLimitMicros = &v },
		"no-stop":       func(_ *Template, c *TrustedContext) { v := int64(150000000); c.RequestBudgetLimitMicros = &v },
		"increase":      func(_ *Template, c *TrustedContext) { v := int64(1500000001); c.RequestBudgetLimitMicros = &v },
		"overflow-edge": func(_ *Template, c *TrustedContext) { v := int64(1<<63 - 1); c.RequestBudgetLimitMicros = &v },
		"mutated-template": func(p *Template, _ *TrustedContext) {
			p.Budget.HardLimitMicros = lower
			p.Budget.OperationalStopMicros = lower - 150000000
		},
		"committed-first-start": func(_ *Template, c *TrustedContext) { c.CommittedMicros = 1 },
		"attempt-two":           func(_ *Template, c *TrustedContext) { c.Attempt = 2 },
		"old-proof-lower-cap": func(_ *Template, c *TrustedContext) {
			c.Attempt = 2
			c.CommittedMicros = proof.CommittedMicros
			c.ReleasedPreclockProof = &proof
		},
		"repair-override": func(p *Template, _ *TrustedContext) { *p = loadRepositoryTemplateNamed(t, "repair-v1.json") },
	} {
		t.Run(name, func(t *testing.T) {
			p, c := template, trusted
			c.RequestBudgetLimitMicros = &lower
			mutate(&p, &c)
			if _, err := Materialize(p, input, c); err == nil {
				t.Fatal("invalid request budget accepted")
			}
		})
	}
	def := int64(1500000000)
	old := trusted
	old.RequestBudgetLimitMicros = &def
	old.Attempt = 2
	old.CommittedMicros = proof.CommittedMicros
	old.ReleasedPreclockProof = &proof
	if _, err := Materialize(template, input, old); err != nil {
		t.Fatalf("old proof with explicit default: %v", err)
	}
	formal := loadRepositoryTemplateNamed(t, "formal-v1.json")
	trusted.RequestBudgetLimitMicros = &lower
	trusted.CommittedMicros = 74477600
	trusted.Transition = &StageTransition{Schema: FormalTransitionSchemaV1, FromStage: StageRehearsal, Outcome: "rehearsal_pass", RequestID: input.RequestID, SourceSHA: input.SourceSHA, BundleDigest: trusted.BundleDigest, CodexDiagnosticPubKey: input.CodexDiagnosticPubKey, CommittedMicros: trusted.CommittedMicros, ZeroInventory: true}
	if plan, err := Materialize(formal, input, trusted); err != nil || plan.LeasePlan.Budget.LimitMicros != lower {
		t.Fatalf("formal lower envelope: %+v / %v", plan, err)
	}
	trusted.CommittedMicros = lower - 150000000
	trusted.Transition.CommittedMicros = trusted.CommittedMicros
	if _, err := Materialize(formal, input, trusted); err == nil {
		t.Fatal("formal prior at lowered stop accepted")
	}
}
func stringBudget(n int64) string { b, _ := json.Marshal(n); return string(b) }
