package clouddeploy_test

import (
	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
	"testing"
	"time"
)

func TestDownwardImmutableDeploymentBudget(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	lease := deploymentLease(now)
	manifest := deploymentManifest()
	original, err := clouddeploy.BuildPlan(lease, manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	lease.Budget.LimitMicros = 1351044800
	lease.Budget.OperationalStopMicros = 1201044800
	plan, err := clouddeploy.BuildPlan(lease, manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Budget.LimitMicros != 1351044800 || plan.Budget.OperationalStopMicros != 1201044800 || plan.PlanDigest == original.PlanDigest {
		t.Fatalf("budget/digest not bound: %+v", plan)
	}
	if err := clouddeploy.ValidatePlan(plan, manifest, now); err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.Budget.LimitMicros++
	changed.Budget.OperationalStopMicros++
	if err := clouddeploy.ValidatePlan(changed, manifest, now); err == nil {
		t.Fatal("digest mutation accepted")
	}
	for name, pair := range map[string][2]int64{"raise": {1500000001, 1350000001}, "no-stop": {150000000, 0}, "gap": {1351044800, 1201044801}, "negative": {-1, -150000001}} {
		t.Run(name, func(t *testing.T) {
			c := lease
			c.Budget.LimitMicros = pair[0]
			c.Budget.OperationalStopMicros = pair[1]
			if _, err := clouddeploy.BuildPlan(c, manifest, now); err == nil {
				t.Fatal("invalid limits accepted")
			}
		})
	}
	// The existing generic Quote admission remains inclusive at the admitted stop.
	lease.Budget.CommittedMicros = lease.Budget.OperationalStopMicros - lease.Budget.EstimatedCostMicros
	if _, err := clouddeploy.BuildPlan(lease, manifest, now); err != nil {
		t.Fatalf("quote at remaining stop rejected: %v", err)
	}
	lease.Budget.CommittedMicros++
	if _, err := clouddeploy.BuildPlan(lease, manifest, now); err == nil {
		t.Fatal("quote above remaining stop accepted")
	}
	lease.Budget.CommittedMicros = lease.Budget.OperationalStopMicros
	if _, err := clouddeploy.BuildPlan(lease, manifest, now); err == nil {
		t.Fatal("exhausted lower budget accepted")
	}
}
