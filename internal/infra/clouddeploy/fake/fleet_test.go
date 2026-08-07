package fake

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestFleetRecordsLoadHopAndExactFailure(t *testing.T) {
	load := clouddeploy.HostPlan{Role: "load"}
	service := clouddeploy.HostPlan{Role: "service-1"}
	fleet := New(Options{FailOperation: "relay:load:service-1"})
	if err := fleet.StageBundle(context.Background(), load, "sha256:test"); err != nil {
		t.Fatal(err)
	}
	if err := fleet.RelayBundle(context.Background(), load, service, "sha256:test"); err == nil {
		t.Fatal("RelayBundle() succeeded, want injected failure")
	}
	operations := fleet.Operations()
	if len(operations) != 2 || operations[0] != "stage:load" || operations[1] != "relay:load:service-1" {
		t.Fatalf("operations = %v", operations)
	}
}

func TestFleetReturnsConfiguredSnapshot(t *testing.T) {
	want := clouddeploy.ReadinessSnapshot{Schema: clouddeploy.SnapshotSchemaV1, DeploymentPlanDigest: "sha256:test"}
	fleet := New(Options{Snapshot: want})
	got, err := fleet.Snapshot(context.Background(), clouddeploy.DeploymentPlan{})
	if err != nil || got.Schema != want.Schema || got.DeploymentPlanDigest != want.DeploymentPlanDigest {
		t.Fatalf("Snapshot() = %#v, %v", got, err)
	}
}
