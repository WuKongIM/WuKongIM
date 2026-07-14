package alibaba

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestBootstrapPlanAndApplyAreIdempotent(t *testing.T) {
	api := newBootstrapAPIStub()
	bootstrap, err := NewBootstrapper(testBootstrapConfig(), api, func() time.Time {
		return time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewBootstrapper() error = %v", err)
	}

	plan, err := bootstrap.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Changes) != 5 {
		t.Fatalf("initial changes = %d, want 5", len(plan.Changes))
	}
	result, err := bootstrap.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.ProvisionerRoleARN == "" || result.AnalyzerRoleARN == "" || result.OIDCProviderARN == "" {
		t.Fatalf("apply result = %#v, want non-secret ARNs", result)
	}
	plan, err = bootstrap.Plan(context.Background())
	if err != nil {
		t.Fatalf("second Plan() error = %v", err)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("second changes = %#v, want idempotent empty plan", plan.Changes)
	}
}

func TestBootstrapPoliciesSeparateProvisioningAndAnalysis(t *testing.T) {
	desired, err := DesiredBootstrapState(testBootstrapConfig())
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	if desired.ProvisionerPolicy.Document == desired.AnalyzerPolicy.Document {
		t.Fatal("provisioner and analyzer policies are identical")
	}
	assertContains(t, desired.ProvisionerPolicy.Document, "ecs:RunInstances")
	assertContains(t, desired.ProvisionerPolicy.Document, "ecs:CreateDisk")
	assertContains(t, desired.ProvisionerPolicy.Document, "ecs:AttachDisk")
	assertContains(t, desired.ProvisionerPolicy.Document, "ecs:TagResources")
	assertContains(t, desired.ProvisionerPolicy.Document, "vpc:CreateVpc")
	assertNotContains(t, desired.AnalyzerPolicy.Document, "ecs:RunInstances")
	assertNotContains(t, desired.AnalyzerPolicy.Document, "vpc:CreateVpc")
	assertContains(t, desired.AnalyzerPolicy.Document, "ecs:DescribeInstances")
	assertContains(t, desired.AnalyzerPolicy.Document, "ecs:AuthorizeSecurityGroup")
	assertContains(t, desired.ProvisionerRole.TrustPolicy, "repo:WuKongIM/WuKongIM:environment:cloud-sim-provision:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-sim-provision.yml@refs/heads/main")
	assertContains(t, desired.ProvisionerRole.TrustPolicy, "repo:WuKongIM/WuKongIM:environment:cloud-sim-cleanup:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-sim-cleanup.yml@refs/heads/main")
	assertContains(t, desired.AnalyzerRole.TrustPolicy, "repo:WuKongIM/WuKongIM:environment:cloud-sim-analysis:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-sim-analyze.yml@refs/heads/main")
	assertContains(t, desired.AnalyzerRole.TrustPolicy, "repo:WuKongIM/WuKongIM:environment:cloud-sim-analysis:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-sim-oidc-subject.yml@refs/heads/main")
	assertNotContains(t, desired.ProvisionerRole.TrustPolicy, "oidc:job_workflow_ref")
	assertNotContains(t, desired.ProvisionerRole.TrustPolicy, "oidc:ref")
	assertContains(t, desired.AnalyzerRole.TrustPolicy, "wukongim-cloud-sim")
}

func TestBootstrapPoliciesUseRepositoryScopedRoleNames(t *testing.T) {
	config := testBootstrapConfig()
	config.ProvisionerRoleName = "wukongim-cloud-sim-provisioner-92324335"
	config.AnalyzerRoleName = "wukongim-cloud-sim-analyzer-92324335"

	desired, err := DesiredBootstrapState(config)
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	if desired.ProvisionerPolicy.Name != config.ProvisionerRoleName {
		t.Fatalf("provisioner policy name = %q, want %q", desired.ProvisionerPolicy.Name, config.ProvisionerRoleName)
	}
	if desired.AnalyzerPolicy.Name != config.AnalyzerRoleName {
		t.Fatalf("analyzer policy name = %q, want %q", desired.AnalyzerPolicy.Name, config.AnalyzerRoleName)
	}
}

func TestBootstrapRemoveRefusesWhileActiveRunsExist(t *testing.T) {
	api := newBootstrapAPIStub()
	api.activeRuns = []cloudsim.Run{{ID: "run-active", State: cloudsim.StateRunning}}
	bootstrap, err := NewBootstrapper(testBootstrapConfig(), api, time.Now)
	if err != nil {
		t.Fatalf("NewBootstrapper() error = %v", err)
	}

	_, err = bootstrap.Remove(context.Background())
	if !errors.Is(err, ErrBootstrapActiveRuns) {
		t.Fatalf("Remove() error = %v, want ErrBootstrapActiveRuns", err)
	}
	if api.removeCalls != 0 {
		t.Fatalf("remove calls = %d, want 0", api.removeCalls)
	}
}

func testBootstrapConfig() BootstrapConfig {
	return BootstrapConfig{
		AccountID: "1234567890123456", Region: "cn-hangzhou", Repository: "WuKongIM/WuKongIM", DefaultBranch: "main",
		OIDCProviderName: "wukongim-github", OIDCAudience: "wukongim-cloud-sim",
		OIDCFingerprints:     []string{"6938fd4d98bab03faadb97b34396831e3780aea1"},
		ProvisionEnvironment: "cloud-sim-provision", CleanupEnvironment: "cloud-sim-cleanup", AnalysisEnvironment: "cloud-sim-analysis",
		ProvisionerRoleName: "wukongim-cloud-sim-provisioner", AnalyzerRoleName: "wukongim-cloud-sim-analyzer",
	}
}

type bootstrapAPIStub struct {
	state       BootstrapState
	activeRuns  []cloudsim.Run
	removeCalls int
}

func newBootstrapAPIStub() *bootstrapAPIStub { return &bootstrapAPIStub{} }

func (a *bootstrapAPIStub) ReadBootstrapState(context.Context, BootstrapConfig) (BootstrapState, error) {
	return a.state, nil
}

func (a *bootstrapAPIStub) ApplyBootstrapState(_ context.Context, desired BootstrapState) error {
	a.state = desired
	return nil
}

func (a *bootstrapAPIStub) RemoveBootstrapState(context.Context, BootstrapState) error {
	a.removeCalls++
	a.state = BootstrapState{}
	return nil
}

func (a *bootstrapAPIStub) ActiveRuns(context.Context) ([]cloudsim.Run, error) {
	return append([]cloudsim.Run(nil), a.activeRuns...), nil
}

func assertContains(t *testing.T, value, part string) {
	t.Helper()
	if !strings.Contains(value, part) {
		t.Fatalf("%q does not contain %q", value, part)
	}
}

func assertNotContains(t *testing.T, value, part string) {
	t.Helper()
	if strings.Contains(value, part) {
		t.Fatalf("%q unexpectedly contains %q", value, part)
	}
}
