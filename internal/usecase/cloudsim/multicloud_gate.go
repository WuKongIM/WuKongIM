package cloudsim

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrAlibabaCanaryIncomplete prevents a second provider adapter from starting
	// before the complete first-provider lifecycle has real cleanup evidence.
	ErrAlibabaCanaryIncomplete = errors.New("internal/usecase/cloudsim: Alibaba canary incomplete")
)

// AlibabaCanaryAttestation is human-reviewed evidence for the multi-cloud gate.
// It is intentionally not produced by unit tests or fake-provider execution.
type AlibabaCanaryAttestation struct {
	// ReviewedAt records when a repository owner reviewed the real cloud drills.
	ReviewedAt time.Time `json:"reviewed_at"`
	// Reviewer is the accountable repository owner identity.
	Reviewer string `json:"reviewer"`
	// Drills contains one real workflow reference and zero-residual decision per required drill.
	Drills map[string]CanaryDrill `json:"drills"`
}

// CanaryDrill is one immutable manual-cloud verification result.
type CanaryDrill struct {
	// WorkflowURL points to the exact GitHub Actions run used as evidence.
	WorkflowURL string `json:"workflow_url"`
	// RunID is the exact Simulation Run identity, or a comma-separated set for cancellation stages.
	RunID string `json:"run_id"`
	// Green is true only after the expected behavior was observed.
	Green bool `json:"green"`
	// ZeroResidual is true only after provider inventory proved cleanup complete.
	ZeroResidual bool `json:"zero_residual"`
}

// RequiredAlibabaCanaryDrills returns the immutable gate vocabulary.
func RequiredAlibabaCanaryDrills() []string {
	return []string{
		"small-2h-lifecycle",
		"provision-cancellations",
		"native-release-sweep",
		"stale-ingress-cleanup",
		"detached-disk-cleanup",
		"cluster-node-reclaim",
		"simulator-loss",
		"released-unknown-preflight",
		"healthy-seeded-defect-diagnosis",
		"isolated-draft-pr",
	}
}

// ValidateTencentAdmission enforces the accepted Alibaba-first delivery order.
func ValidateTencentAdmission(attestation AlibabaCanaryAttestation, repository string) error {
	if attestation.ReviewedAt.IsZero() || strings.TrimSpace(attestation.Reviewer) == "" || strings.Count(repository, "/") != 1 {
		return ErrAlibabaCanaryIncomplete
	}
	for _, name := range RequiredAlibabaCanaryDrills() {
		drill, ok := attestation.Drills[name]
		if !ok || !drill.Green || !drill.ZeroResidual || strings.TrimSpace(drill.RunID) == "" {
			return fmt.Errorf("%w: %s", ErrAlibabaCanaryIncomplete, name)
		}
		workflowURL, err := url.Parse(drill.WorkflowURL)
		if err != nil || workflowURL.Scheme != "https" || workflowURL.Host != "github.com" || !strings.HasPrefix(workflowURL.Path, "/"+repository+"/actions/runs/") {
			return fmt.Errorf("%w: %s workflow", ErrAlibabaCanaryIncomplete, name)
		}
	}
	return nil
}
