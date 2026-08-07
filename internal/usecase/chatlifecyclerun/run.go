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
	TemplateSchemaV1         = "wukongim.chat_lifecycle.run_plan_template/v1"
	RunPlanSchemaV1          = "wukongim.chat_lifecycle.run_plan/v1"
	FormalTransitionSchemaV1 = "wukongim.chat_lifecycle.formal_transition/v1"
	StageRehearsal           = "rehearsal"
	StageFormal              = "formal"
	maxDocumentBytes         = 1 << 20
)

var (
	ErrInvalidTemplate = errors.New("chat lifecycle run: invalid template")
	ErrInvalidInput    = errors.New("chat lifecycle run: invalid input")
	identityPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)
	repositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Template is the reviewed, versioned policy source. None of its quantities
// are exposed as top-level workflow inputs.
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
	// Attempt is the one-based deployment attempt within the bounded retry policy.
	Attempt int
	// CommittedMicros is authenticated spend from all earlier attempts and stages.
	CommittedMicros int64
	// ExcludedPlacement prevents the sole retry from selecting the failed placement again.
	ExcludedPlacement *cloudlease.PlacementExclusion
	// Transition authorizes formal procurement only after released rehearsal evidence.
	Transition *StageTransition
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
		Tags:       map[string]string{"scenario": "chat-lifecycle", "stage": template.Stage},
	}
	if trusted.ExcludedPlacement != nil {
		plan.Placement.ExcludedOffers = []cloudlease.PlacementExclusion{*trusted.ExcludedPlacement}
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
		template.Budget.Currency != "CNY" || template.Budget.HardLimitMicros != 1_500_000_000 ||
		template.Budget.OperationalStopMicros != 1_350_000_000 ||
		template.Network.ConservativePublicEgressBytes <= 0 || template.Network.PeakBandwidthMbps != 20 ||
		template.Compute.VCPUs != 4 || template.Compute.MemoryBytes != 8<<30 ||
		template.Compute.Architecture != "x86_64" || !strings.EqualFold(template.Compute.BillingModel, "postpaid") ||
		template.Compute.AllowBurstable || template.Retry.DeploymentRetries != 1 || len(template.HostGroups) != 2 {
		return false
	}
	switch template.Stage {
	case StageRehearsal:
		if template.LeaseDurationSeconds != int64((6*time.Hour)/time.Second) ||
			template.WorkloadDurationSeconds != int64((2*time.Hour)/time.Second) {
			return false
		}
	case StageFormal:
		if template.LeaseDurationSeconds != int64((96*time.Hour)/time.Second) ||
			template.WorkloadDurationSeconds != int64((72*time.Hour)/time.Second) {
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
	baseCommitted := int64(0)
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
	switch trusted.Attempt {
	case 1:
		return trusted.CommittedMicros == baseCommitted && trusted.ExcludedPlacement == nil
	case 2:
		return trusted.CommittedMicros > baseCommitted && trusted.ExcludedPlacement != nil &&
			strings.TrimSpace(trusted.ExcludedPlacement.Zone) != "" &&
			strings.TrimSpace(trusted.ExcludedPlacement.ComputeType) != ""
	default:
		return false
	}
}

func normalizeEd25519PublicKey(value string) (string, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(value) + "\n"))
	if err != nil || len(bytes.TrimSpace(rest)) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return "", ErrInvalidInput
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
