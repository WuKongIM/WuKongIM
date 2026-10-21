// Package chatlifecyclerun binds the fixed operator surface to reviewed cloud
// and workload Plans without acquiring or deploying infrastructure.
package chatlifecyclerun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	TemplateSchemaV1              = "wukongim.chat_lifecycle.run_plan_template/v1"
	RunPlanSchemaV1               = "wukongim.chat_lifecycle.run_plan/v1"
	FormalTransitionSchemaV1      = "wukongim.chat_lifecycle.formal_transition/v1"
	ReleasedPreclockProofSchemaV1 = "wukongim.chat_lifecycle.released_preclock_continuation/v1"
	StageRepair                   = "repair"
	StageRehearsal                = "rehearsal"
	StageFormal                   = "formal"
	maxDocumentBytes              = 1 << 20
	maxReleasedPreclockProofBytes = 256 << 10
	minimumRepairWorkload         = int64((16 * time.Minute) / time.Second)
	maximumRepairWorkload         = int64((72*time.Hour + 15*time.Minute) / time.Second)
	minimumRepairLease            = int64((6 * time.Hour) / time.Second)
	repairLeaseReserve            = int64((4*time.Hour + 45*time.Minute) / time.Second)
	defaultRepairBudgetCNY        = int64(300)
	defaultRepairStopCNY          = int64(250)
	maximumRepairBudgetCNY        = int64(1500)
	customRepairStopReserve       = int64(20)
	microsPerCNY                  = int64(1_000_000)
)

var (
	ErrInvalidTemplate = errors.New("chat lifecycle run: invalid template")
	ErrInvalidInput    = errors.New("chat lifecycle run: invalid input")
	identityPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)
	repositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	hexDigestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	proofFieldPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Template is the reviewed, versioned policy source. Direct repair may derive
// its bounded workload and Lease durations plus an explicitly authorized
// repair budget before this package validates it.
type Template struct {
	Schema                  string              `json:"schema"`
	Stage                   string              `json:"stage"`
	Provider                string              `json:"provider"`
	Region                  string              `json:"region"`
	LeaseDurationSeconds    int64               `json:"lease_duration_seconds"`
	WorkloadDurationSeconds int64               `json:"workload_duration_seconds"`
	ReadinessTimeoutSeconds int64               `json:"readiness_timeout_seconds"`
	Budget                  BudgetTemplate      `json:"budget"`
	Network                 NetworkTemplate     `json:"network"`
	Compute                 ComputeTemplate     `json:"compute"`
	HostGroups              []HostGroupTemplate `json:"host_groups"`
	Retry                   RetryTemplate       `json:"retry"`
}

type BudgetTemplate struct {
	Currency              string `json:"currency"`
	HardLimitMicros       int64  `json:"hard_limit_micros"`
	OperationalStopMicros int64  `json:"operational_stop_micros"`
}

type NetworkTemplate struct {
	ConservativePublicEgressBytes int64 `json:"conservative_public_egress_bytes"`
	PeakBandwidthMbps             int   `json:"peak_bandwidth_mbps"`
}

type ComputeTemplate struct {
	VCPUs          int    `json:"vcpus"`
	MemoryBytes    int64  `json:"memory_bytes"`
	Architecture   string `json:"architecture"`
	BillingModel   string `json:"billing_model"`
	AllowBurstable bool   `json:"allow_burstable"`
}

type HostGroupTemplate struct {
	Role            string `json:"role"`
	Count           int    `json:"count"`
	SystemDiskBytes int64  `json:"system_disk_bytes"`
	DataDiskBytes   int64  `json:"data_disk_bytes"`
	PublicIPv4      bool   `json:"public_ipv4"`
}

type RetryTemplate struct {
	// DeploymentRetries is the number of fresh-Lease deployment retries. It is
	// fixed to zero because control repair retains the existing Lease.
	DeploymentRetries int `json:"deployment_retries"`
}

// OperatorInput is the complete public top-level operator surface.
type OperatorInput struct {
	SourceSHA             string
	Operator              string
	CodexDiagnosticPubKey string
	RequestID             string
}

// TrustedContext is derived only by the protected workflow or a prior typed
// receipt. It is not an additional operator-controlled surface.
type TrustedContext struct {
	// Repository is the protected workflow repository bound into every Lease selector.
	Repository string
	// BundleDigest identifies the already authenticated immutable deployment bundle.
	BundleDigest string
	// DeploymentPubKey is the fresh Lease-scoped activation and finalization identity.
	DeploymentPubKey string
	// Now is the trusted orchestration time used for expiry and transition checks.
	Now time.Time
	// Attempt is one per stage, or two for an explicitly authenticated released rehearsal.
	Attempt int
	// CommittedMicros is authenticated spend from all earlier attempts and stages.
	CommittedMicros int64
	// AuthorizedRepairBudgetCNY is the whole-CNY repair ceiling independently
	// bound by the direct controller. Zero preserves the reviewed default.
	AuthorizedRepairBudgetCNY int64
	// Transition authorizes formal procurement only after released rehearsal evidence.
	Transition *StageTransition
	// ReleasedPreclockProof carries the protected helper's authenticated history;
	// it permits only one adjacent-run rehearsal with the old Quote reserve.
	ReleasedPreclockProof *ReleasedPreclockProof
}

// ReleasedPreclockProof is the bounded public output of the read-only history
// authenticator. Materialize checks its bindings but does not authenticate GitHub
// or provider evidence itself. Consumers must retain and verify the original
// evidence manifest; this document is not a standalone procurement credential.
type ReleasedPreclockProof struct {
	Schema       string `json:"schema"`
	Repository   string `json:"repository"`
	RepositoryID int64  `json:"repository_id"`
	RequestID    string `json:"request_id"`
	SourceSHA    string `json:"source_sha"`
	Operator     string `json:"operator"`
	// CodexDiagnosticPublicKey preserves the original request's diagnostic owner.
	CodexDiagnosticPublicKey string `json:"codex_diagnostic_public_key"`
	// Predecessor identifies the original, fully released rehearsal-1, never a continuation.
	PredecessorRunID          int64     `json:"predecessor_run_id"`
	PredecessorWorkflowID     int64     `json:"predecessor_workflow_id"`
	PredecessorRunNumber      int64     `json:"predecessor_run_number"`
	PredecessorRunAttempt     int64     `json:"predecessor_run_attempt"`
	PredecessorControlSHA     string    `json:"predecessor_control_sha"`
	PredecessorLeaseID        string    `json:"predecessor_lease_id"`
	PredecessorPlanDigest     string    `json:"predecessor_plan_digest"`
	PredecessorCreatedAt      time.Time `json:"predecessor_created_at"`
	PredecessorZeroObservedAt time.Time `json:"predecessor_zero_observed_at"`
	AcquireRunID              int64     `json:"acquire_run_id"`
	ReleaseRunID              int64     `json:"release_run_id"`
	// CurrentRunNumber must be the immediate successor in the same numeric workflow.
	CurrentRunID      int64  `json:"current_run_id"`
	CurrentWorkflowID int64  `json:"current_workflow_id"`
	CurrentRunNumber  int64  `json:"current_run_number"`
	CurrentRunAttempt int64  `json:"current_run_attempt"`
	ControlSHA        string `json:"control_sha"`
	NextAttempt       int    `json:"next_attempt"`
	// CommittedMicros reserves the full admitted old Quote, not an asserted invoice.
	CommittedMicros       int64  `json:"committed_micros"`
	HardLimitMicros       int64  `json:"hard_limit_micros"`
	OperationalStopMicros int64  `json:"operational_stop_micros"`
	FailureClass          string `json:"failure_class"`
	CostMethod            string `json:"cost_method"`
	// EvidenceManifestSHA256 binds the retained public predecessor evidence bytes.
	EvidenceManifestSHA256 string `json:"evidence_manifest_sha256"`
}

// DecodeReleasedPreclockProof rejects oversized, duplicate, unknown and trailing
// fields before the protected workflow may bind the proof to a new Run Plan.
func DecodeReleasedPreclockProof(reader io.Reader) (ReleasedPreclockProof, error) {
	if reader == nil {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxReleasedPreclockProofBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxReleasedPreclockProofBytes {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	// Scan the same bounded bytes for duplicate keys; encoding/json otherwise
	// silently replaces a previously decoded field with the later value.
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		// DisallowUnknownFields still accepts case-folded JSON tag aliases.
		// Require the schema's lower-case ASCII spelling before typed decoding.
		if err != nil || !ok || !proofFieldPattern.MatchString(key) || seen[key] {
			return ReleasedPreclockProof{}, ErrInvalidInput
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ReleasedPreclockProof{}, ErrInvalidInput
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var proof ReleasedPreclockProof
	if err := decoder.Decode(&proof); err != nil || !validReleasedPreclockProof(proof) {
		return ReleasedPreclockProof{}, ErrInvalidInput
	}
	return proof, nil
}

func validReleasedPreclockProof(proof ReleasedPreclockProof) bool {
	if proof.Schema != ReleasedPreclockProofSchemaV1 || !repositoryPattern.MatchString(proof.Repository) ||
		proof.RepositoryID <= 0 || !identityPattern.MatchString(proof.RequestID) ||
		!shaPattern.MatchString(proof.SourceSHA) || proof.Operator != "tangtaoit" ||
		!shaPattern.MatchString(proof.PredecessorControlSHA) || !shaPattern.MatchString(proof.ControlSHA) ||
		!hexDigestPattern.MatchString(proof.PredecessorPlanDigest) || !hexDigestPattern.MatchString(proof.EvidenceManifestSHA256) ||
		proof.PredecessorLeaseID != proof.RequestID+"-rehearsal-1" || proof.NextAttempt != 2 ||
		proof.PredecessorWorkflowID <= 0 || proof.CurrentWorkflowID != proof.PredecessorWorkflowID ||
		proof.CurrentRunID <= proof.PredecessorRunID || proof.PredecessorRunNumber <= 0 ||
		proof.CurrentRunNumber <= proof.PredecessorRunNumber || proof.CurrentRunNumber-proof.PredecessorRunNumber != 1 ||
		proof.PredecessorRunAttempt != 1 || proof.CurrentRunAttempt != 1 ||
		proof.PredecessorCreatedAt.IsZero() || proof.PredecessorCreatedAt.Location() != time.UTC ||
		proof.PredecessorZeroObservedAt.IsZero() || proof.PredecessorZeroObservedAt.Location() != time.UTC ||
		proof.PredecessorZeroObservedAt.Before(proof.PredecessorCreatedAt) ||
		proof.CommittedMicros <= 0 || proof.CommittedMicros >= proof.OperationalStopMicros ||
		proof.HardLimitMicros != 1_500_000_000 || proof.OperationalStopMicros != 1_350_000_000 ||
		proof.CostMethod != "full_admitted_quote_reserve" ||
		(proof.FailureClass != "unclassified_before_accepted_run_start" && proof.FailureClass != "preflight_before_accepted_run_start") {
		return false
	}
	seen := make(map[int64]bool, 4)
	for _, id := range []int64{proof.PredecessorRunID, proof.CurrentRunID, proof.AcquireRunID, proof.ReleaseRunID} {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	_, err := normalizeEd25519PublicKey(proof.CodexDiagnosticPublicKey)
	return err == nil
}

// StageTransition is the authenticated, non-secret proof that the rehearsal
// report survived upload and its Lease reached exact zero inventory before a
// fresh formal Lease may consume the same aggregate cost envelope.
type StageTransition struct {
	// Schema pins the closed transition contract version.
	Schema string `json:"schema"`
	// FromStage must identify the completed rehearsal stage.
	FromStage string `json:"from_stage"`
	// Outcome must be the authenticated passing rehearsal outcome.
	Outcome string `json:"outcome"`
	// RequestID binds both paid Leases to one aggregate operator request.
	RequestID string `json:"request_id"`
	// SourceSHA binds formal execution to the same protected-main source revision.
	SourceSHA string `json:"source_sha"`
	// BundleDigest binds formal deployment to the reviewed rehearsal bundle.
	BundleDigest string `json:"bundle_digest"`
	// CodexDiagnosticPubKey preserves the request-scoped local diagnostic
	// identity across the released rehearsal and fresh formal Lease boundary.
	CodexDiagnosticPubKey string `json:"codex_diagnostic_pubkey"`
	// CommittedMicros is conservative rehearsal spend carried into the formal quote gate.
	CommittedMicros int64 `json:"committed_micros"`
	// ZeroInventory proves the rehearsal Lease was fully released before formal procurement.
	ZeroInventory bool `json:"zero_inventory"`
}

// RunPlan is the non-secret materialized policy passed to generic Actions.
type RunPlan struct {
	Schema                  string                     `json:"schema"`
	TemplateSchema          string                     `json:"template_schema"`
	Stage                   string                     `json:"stage"`
	Attempt                 int                        `json:"attempt"`
	WorkloadDurationSeconds int64                      `json:"workload_duration_seconds"`
	ReadinessTimeoutSeconds int64                      `json:"readiness_timeout_seconds"`
	OperationalStopMicros   int64                      `json:"operational_stop_micros"`
	LeasePlan               cloudlease.Plan            `json:"lease_plan"`
	BootstrapAccess         cloudlease.BootstrapAccess `json:"bootstrap_access"`
}

// DecodeTemplate strictly reads one bounded repository template.
func DecodeTemplate(reader io.Reader) (Template, error) {
	if reader == nil {
		return Template{}, ErrInvalidTemplate
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxDocumentBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxDocumentBytes {
		return Template{}, ErrInvalidTemplate
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var template Template
	if err := decoder.Decode(&template); err != nil {
		return Template{}, fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Template{}, ErrInvalidTemplate
	}
	if !validTemplate(template) {
		return Template{}, ErrInvalidTemplate
	}
	return template, nil
}

// Materialize binds one immutable Lease attempt to the reviewed template.
func Materialize(template Template, input OperatorInput, trusted TrustedContext) (RunPlan, error) {
	if !validTemplate(template) || !validOperatorInput(input) || !validTrustedContext(template, trusted) {
		return RunPlan{}, ErrInvalidInput
	}
	if template.Stage == StageFormal && (trusted.Transition.RequestID != input.RequestID ||
		trusted.Transition.SourceSHA != input.SourceSHA) {
		return RunPlan{}, ErrInvalidInput
	}
	deploymentKey, err := normalizeEd25519PublicKey(trusted.DeploymentPubKey)
	if err != nil {
		return RunPlan{}, ErrInvalidInput
	}
	codexKey, err := normalizeEd25519PublicKey(input.CodexDiagnosticPubKey)
	if err != nil || codexKey == deploymentKey {
		return RunPlan{}, ErrInvalidInput
	}
	if proof := trusted.ReleasedPreclockProof; proof != nil {
		proofKey, err := normalizeEd25519PublicKey(proof.CodexDiagnosticPublicKey)
		if err != nil || proofKey != codexKey || proof.RequestID != input.RequestID ||
			proof.SourceSHA != input.SourceSHA || proof.Operator != input.Operator {
			return RunPlan{}, ErrInvalidInput
		}
	}
	if template.Stage == StageFormal {
		transitionCodexKey, transitionErr := normalizeEd25519PublicKey(trusted.Transition.CodexDiagnosticPubKey)
		if transitionErr != nil || transitionCodexKey != codexKey {
			return RunPlan{}, ErrInvalidInput
		}
	}
	keys := []string{deploymentKey, codexKey}
	slices.Sort(keys)
	expiresAt := trusted.Now.UTC().Add(time.Duration(template.LeaseDurationSeconds) * time.Second)
	compute := cloudlease.ComputePlan{
		VCPUs: template.Compute.VCPUs, MemoryBytes: template.Compute.MemoryBytes,
		Architecture: template.Compute.Architecture, BillingModel: template.Compute.BillingModel,
		AllowBurstable: template.Compute.AllowBurstable,
	}
	hostGroups := make([]cloudlease.HostGroupPlan, len(template.HostGroups))
	for index, group := range template.HostGroups {
		hostGroups[index] = cloudlease.HostGroupPlan{
			Role: group.Role, Count: group.Count, Compute: compute,
			SystemDisk: cloudlease.DiskPlan{Role: "system", CountPerHost: 1, SizeBytes: group.SystemDiskBytes, Class: "essd", PerformanceLevel: "PL0"},
			DataDisks:  []cloudlease.DiskPlan{{Role: "data", CountPerHost: 1, SizeBytes: group.DataDiskBytes, Class: "essd", PerformanceLevel: "PL0"}},
			PublicIPv4: group.PublicIPv4, InternetEgress: group.PublicIPv4,
		}
		if group.PublicIPv4 {
			hostGroups[index].PeakBandwidthMbps = template.Network.PeakBandwidthMbps
		}
	}
	plan := cloudlease.Plan{
		Schema:    cloudlease.PlanSchemaV1,
		LeaseID:   fmt.Sprintf("%s-%s-%d", input.RequestID, template.Stage, trusted.Attempt),
		RequestID: input.RequestID, Provider: template.Provider, Region: template.Region,
		Repository: trusted.Repository, Operator: input.Operator, ExpiresAt: expiresAt,
		Budget: cloudlease.Budget{
			Currency: template.Budget.Currency, LimitMicros: template.Budget.HardLimitMicros,
			CommittedMicros: trusted.CommittedMicros, OperationalStopMicros: template.Budget.OperationalStopMicros,
		},
		Provenance: cloudlease.Provenance{SourceSHA: input.SourceSHA, BundleDigest: trusted.BundleDigest},
		Network: cloudlease.NetworkPlan{
			Isolated: true, SingleZone: true,
			ConservativePublicEgressBytes: template.Network.ConservativePublicEgressBytes,
			InitialAccess: []cloudlease.AccessGrant{
				{ID: "public-ssh", TargetRole: "load", Protocol: cloudlease.ProtocolTCP, PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: expiresAt},
				{ID: "public-http", TargetRole: "load", Protocol: cloudlease.ProtocolTCP, PortFrom: 80, PortTo: 80, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: expiresAt},
			},
		},
		HostGroups: hostGroups,
		Tags:       map[string]string{"stage": template.Stage},
	}
	if err := cloudlease.ValidatePlan(plan, trusted.Now); err != nil {
		return RunPlan{}, ErrInvalidInput
	}
	return RunPlan{
		Schema: RunPlanSchemaV1, TemplateSchema: template.Schema, Stage: template.Stage,
		Attempt: trusted.Attempt, WorkloadDurationSeconds: template.WorkloadDurationSeconds,
		ReadinessTimeoutSeconds: template.ReadinessTimeoutSeconds,
		OperationalStopMicros:   template.Budget.OperationalStopMicros,
		LeasePlan:               plan, BootstrapAccess: cloudlease.BootstrapAccess{AuthorizedKeys: keys},
	}, nil
}

func validTemplate(template Template) bool {
	if template.Schema != TemplateSchemaV1 ||
		template.Provider != "alibaba" || template.Region != "cn-hangzhou" ||
		template.ReadinessTimeoutSeconds <= 0 || template.ReadinessTimeoutSeconds > int64((2*time.Hour)/time.Second) ||
		template.Budget.Currency != "CNY" ||
		template.Network.ConservativePublicEgressBytes <= 0 || template.Network.PeakBandwidthMbps != 20 ||
		template.Compute.VCPUs != 4 || template.Compute.MemoryBytes != 8<<30 ||
		template.Compute.Architecture != "x86_64" || !strings.EqualFold(template.Compute.BillingModel, "postpaid") ||
		template.Compute.AllowBurstable || template.Retry.DeploymentRetries != 0 || len(template.HostGroups) != 2 {
		return false
	}
	switch template.Stage {
	case StageRepair:
		expectedLease := template.WorkloadDurationSeconds + repairLeaseReserve
		if expectedLease < minimumRepairLease {
			expectedLease = minimumRepairLease
		}
		if template.WorkloadDurationSeconds < minimumRepairWorkload ||
			template.WorkloadDurationSeconds > maximumRepairWorkload ||
			template.LeaseDurationSeconds != expectedLease ||
			template.ReadinessTimeoutSeconds != int64((30*time.Minute)/time.Second) ||
			!validRepairBudget(template.Budget) {
			return false
		}
	case StageRehearsal:
		if template.LeaseDurationSeconds != int64((12*time.Hour)/time.Second) ||
			template.WorkloadDurationSeconds != int64((2*time.Hour)/time.Second) ||
			template.Budget.HardLimitMicros != 1_500_000_000 ||
			template.Budget.OperationalStopMicros != 1_350_000_000 {
			return false
		}
	case StageFormal:
		if template.LeaseDurationSeconds != int64((96*time.Hour)/time.Second) ||
			template.WorkloadDurationSeconds != int64((72*time.Hour)/time.Second) ||
			template.Budget.HardLimitMicros != 1_500_000_000 ||
			template.Budget.OperationalStopMicros != 1_350_000_000 {
			return false
		}
	default:
		return false
	}
	service, load := template.HostGroups[0], template.HostGroups[1]
	return service == (HostGroupTemplate{Role: "service", Count: 3, SystemDiskBytes: 40 << 30, DataDiskBytes: 500 << 30}) &&
		load == (HostGroupTemplate{Role: "load", Count: 1, SystemDiskBytes: 40 << 30, DataDiskBytes: 200 << 30, PublicIPv4: true})
}

func validOperatorInput(input OperatorInput) bool {
	return shaPattern.MatchString(input.SourceSHA) && input.Operator == "tangtaoit" &&
		identityPattern.MatchString(input.RequestID) && strings.TrimSpace(input.CodexDiagnosticPubKey) != ""
}

func validTrustedContext(template Template, trusted TrustedContext) bool {
	if trusted.Now.IsZero() || trusted.Now != trusted.Now.UTC() || !digestPattern.MatchString(trusted.BundleDigest) ||
		!repositoryPattern.MatchString(trusted.Repository) ||
		strings.TrimSpace(trusted.DeploymentPubKey) == "" || trusted.CommittedMicros < 0 ||
		trusted.CommittedMicros >= template.Budget.HardLimitMicros {
		return false
	}
	if !validTrustedRepairBudget(template, trusted.AuthorizedRepairBudgetCNY) {
		return false
	}
	baseCommitted := int64(0)
	expectedAttempt := 1
	if proof := trusted.ReleasedPreclockProof; proof != nil {
		if template.Stage != StageRehearsal || !validReleasedPreclockProof(*proof) ||
			proof.Repository != trusted.Repository || proof.PredecessorZeroObservedAt.After(trusted.Now) {
			return false
		}
		baseCommitted, expectedAttempt = proof.CommittedMicros, 2
	}
	if template.Stage == StageFormal {
		transition := trusted.Transition
		if transition == nil || transition.Schema != FormalTransitionSchemaV1 ||
			transition.FromStage != StageRehearsal || transition.Outcome != "rehearsal_pass" ||
			!transition.ZeroInventory || transition.RequestID == "" || !shaPattern.MatchString(transition.SourceSHA) ||
			transition.BundleDigest != trusted.BundleDigest || transition.CommittedMicros <= 0 ||
			transition.CommittedMicros >= template.Budget.OperationalStopMicros {
			return false
		}
		baseCommitted = transition.CommittedMicros
	} else if trusted.Transition != nil {
		return false
	}
	return trusted.Attempt == expectedAttempt && trusted.CommittedMicros == baseCommitted
}

func validRepairBudget(budget BudgetTemplate) bool {
	if budget.HardLimitMicros == defaultRepairBudgetCNY*microsPerCNY {
		return budget.OperationalStopMicros == defaultRepairStopCNY*microsPerCNY
	}
	if budget.HardLimitMicros%microsPerCNY != 0 {
		return false
	}
	hardLimitCNY := budget.HardLimitMicros / microsPerCNY
	return hardLimitCNY > defaultRepairBudgetCNY && hardLimitCNY <= maximumRepairBudgetCNY &&
		budget.OperationalStopMicros == (hardLimitCNY-customRepairStopReserve)*microsPerCNY
}

func validTrustedRepairBudget(template Template, authorizedCNY int64) bool {
	if template.Stage != StageRepair {
		return authorizedCNY == 0
	}
	if authorizedCNY == 0 {
		return template.Budget.HardLimitMicros == defaultRepairBudgetCNY*microsPerCNY &&
			template.Budget.OperationalStopMicros == defaultRepairStopCNY*microsPerCNY
	}
	if authorizedCNY < defaultRepairBudgetCNY || authorizedCNY > maximumRepairBudgetCNY {
		return false
	}
	expectedStopCNY := authorizedCNY - customRepairStopReserve
	if authorizedCNY == defaultRepairBudgetCNY {
		expectedStopCNY = defaultRepairStopCNY
	}
	return template.Budget.HardLimitMicros == authorizedCNY*microsPerCNY &&
		template.Budget.OperationalStopMicros == expectedStopCNY*microsPerCNY
}

func normalizeEd25519PublicKey(value string) (string, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(value) + "\n"))
	if err != nil || len(bytes.TrimSpace(rest)) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return "", ErrInvalidInput
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
