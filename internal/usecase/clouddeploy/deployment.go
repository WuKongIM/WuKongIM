package clouddeploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	// PlanSchemaV1 is the exact four-host native activation contract.
	PlanSchemaV1 = "wukongim.cloud_deployment.plan/v1"
	// SnapshotSchemaV1 is bounded evidence collected after native activation.
	SnapshotSchemaV1 = "wukongim.cloud_deployment.readiness/v1"
	// ReceiptSchemaV1 proves one exact Plan was activated on one exact Lease.
	ReceiptSchemaV1 = "wukongim.cloud_deployment.receipt/v1"
	// FailureSchemaV1 is the stable non-secret deployment failure contract.
	FailureSchemaV1 = "wukongim.cloud_deployment.failure/v1"

	ServiceHostCount        = 3
	LoadHostCount           = 1
	PhysicalHashSlotCount   = 256
	LogicalSlotGroupCount   = 12
	SlotReplicaCount        = 3
	ChannelReplicaCount     = 3
	PrometheusTargetCount   = 7
	MinimumSystemDiskBytes  = 40_000_000_000
	MinimumServiceDataBytes = 500_000_000_000
	MinimumLoadDataBytes    = 200_000_000_000
	MinimumFreePercent      = 5
	MaximumClockDriftMillis = 1000
	MaximumReadinessAge     = 5 * time.Minute
	FormalBudgetHardMicros  = int64(1_500_000_000)
	FormalBudgetStopMicros  = int64(1_350_000_000)
)

var (
	// ErrInvalidDeployment reports a malformed or identity-conflicting deployment artifact.
	ErrInvalidDeployment = errors.New("internal/usecase/clouddeploy: invalid deployment")
)

// LeaseInventory is the provider-neutral subset of an active Lease Receipt
// needed by Deployment. Cloud-specific receipt mapping belongs at an entry boundary.
type LeaseInventory struct {
	LeaseID      string
	RequestID    string
	Repository   string
	Provider     string
	Region       string
	Zone         string
	PlanDigest   string
	SourceSHA    string
	BundleDigest string
	State        string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Budget       DeploymentBudget
	Resources    []LeaseResource
}

// DeploymentBudget is the immutable admitted whole-Lease cost envelope.
type DeploymentBudget struct {
	// Currency is the immutable ISO-style unit shared by every line item.
	Currency string `json:"currency"`
	// LimitMicros is the absolute whole-request authorization ceiling.
	LimitMicros int64 `json:"limit_micros"`
	// OperationalStopMicros is the lower conservative stop threshold checked at runtime.
	OperationalStopMicros int64 `json:"operational_stop_micros"`
	// CommittedMicros carries authenticated spend from earlier released Leases.
	CommittedMicros int64 `json:"committed_micros"`
	// EstimatedCostMicros is this proposed Lease's conservative quoted cost.
	EstimatedCostMicros int64 `json:"estimated_cost_micros"`
	// LineItems preserve the bounded inputs required for later conservative accrual.
	LineItems []DeploymentBudgetLineItem `json:"line_items"`
}

// DeploymentBudgetLineItem is one immutable quoted cost component retained
// for conservative runtime accrual; it contains no provider credential.
type DeploymentBudgetLineItem struct {
	// Kind is the closed provider-independent billing component name.
	Kind string `json:"kind"`
	// Role identifies the service, load, disk, or public-network allocation.
	Role string `json:"role"`
	// Quantity is the quoted count or usage allowance for this component.
	Quantity int `json:"quantity"`
	// CostMicros is the conservative total for Quantity in the budget currency.
	CostMicros int64 `json:"cost_micros"`
}

// LeaseResource is one normalized instance, data disk, or public address.
type LeaseResource struct {
	ID             string
	Kind           string
	Role           string
	ParentID       string
	SizeBytes      int64
	PrivateAddress string
	PublicAddress  string
}

// Topology fixes cluster and logical workload coverage independently.
type Topology struct {
	ServiceNodes      int `json:"service_nodes"`
	LoadNodes         int `json:"load_nodes"`
	PhysicalHashSlots int `json:"physical_hash_slots"`
	LogicalSlotGroups int `json:"logical_slot_groups"`
	SlotReplicas      int `json:"slot_replicas"`
	ChannelReplicas   int `json:"channel_replicas"`
}

// HostPlan binds one stable deployment role to exact Lease inventory.
type HostPlan struct {
	Role                       string `json:"role"`
	LeaseRole                  string `json:"lease_role"`
	NodeID                     int    `json:"node_id,omitempty"`
	InstanceID                 string `json:"instance_id"`
	PrivateAddress             string `json:"private_address"`
	PublicAddress              string `json:"public_address,omitempty"`
	DataDiskID                 string `json:"data_disk_id"`
	MinimumDataFilesystemBytes int64  `json:"minimum_data_filesystem_bytes"`
}

// DeploymentPlan is the immutable WuKongIM-specific consumer of a Lease Receipt.
type DeploymentPlan struct {
	Schema          string           `json:"schema"`
	PlanDigest      string           `json:"plan_digest"`
	LeaseID         string           `json:"lease_id"`
	RequestID       string           `json:"request_id"`
	Repository      string           `json:"repository"`
	Provider        string           `json:"provider"`
	Region          string           `json:"region"`
	Zone            string           `json:"zone"`
	LeasePlanDigest string           `json:"lease_plan_digest"`
	SourceSHA       string           `json:"source_sha"`
	ControlSHA      string           `json:"control_sha"`
	BundleDigest    string           `json:"bundle_digest"`
	LeaseCreatedAt  time.Time        `json:"lease_created_at"`
	ExpiresAt       time.Time        `json:"expires_at"`
	Budget          DeploymentBudget `json:"budget"`
	OperatingSystem string           `json:"operating_system"`
	OSVersion       string           `json:"operating_system_version"`
	Architecture    string           `json:"architecture"`
	Topology        Topology         `json:"topology"`
	Hosts           []HostPlan       `json:"hosts"`
}

// HostSnapshot is bounded local evidence for one planned host.
type HostSnapshot struct {
	Role                    string   `json:"role"`
	OperatingSystem         string   `json:"operating_system"`
	OSVersion               string   `json:"operating_system_version"`
	Architecture            string   `json:"architecture"`
	BaseToolsAvailable      bool     `json:"base_tools_available"`
	BundleDigest            string   `json:"bundle_digest"`
	DataDiskID              string   `json:"data_disk_id"`
	DataMount               string   `json:"data_mount"`
	DataFilesystemBytes     int64    `json:"data_filesystem_bytes"`
	DataFreeBytes           int64    `json:"data_free_bytes"`
	SystemFilesystemBytes   int64    `json:"system_filesystem_bytes"`
	SystemFreeBytes         int64    `json:"system_free_bytes"`
	ClockOffsetMilliseconds int64    `json:"clock_offset_milliseconds"`
	ActiveUnits             []string `json:"active_units"`
}

// ClusterSnapshot proves three-node membership and full physical/logical Slot topology.
type ClusterSnapshot struct {
	ReadyNodes             int `json:"ready_nodes"`
	Members                int `json:"members"`
	PhysicalHashSlots      int `json:"physical_hash_slots"`
	HealthySlotLeaders     int `json:"healthy_slot_leaders"`
	HealthySlotReplicaSets int `json:"healthy_slot_replica_sets"`
	LogicalSlotGroups      int `json:"logical_slot_groups"`
	RuntimeConfigNodes     int `json:"runtime_config_nodes"`
	SlotReplicas           int `json:"slot_replicas"`
	ChannelReplicas        int `json:"channel_replicas"`
	PendingControllerTasks int `json:"pending_controller_tasks"`
}

// LoadSnapshot proves every load-node process and local public observation edge.
type LoadSnapshot struct {
	ReadyWorkers          int  `json:"ready_workers"`
	PrometheusTargetsUp   int  `json:"prometheus_targets_up"`
	PrometheusTargetsWant int  `json:"prometheus_targets_want"`
	WorkloadConfigValid   bool `json:"workload_config_valid"`
	ProxyReady            bool `json:"proxy_ready"`
	ManagerReady          bool `json:"manager_ready"`
	DemoReady             bool `json:"demo_ready"`
	AnalysisReady         bool `json:"analysis_ready"`
}

// ReadinessSnapshot is the complete bounded input to the fail-closed gate.
type ReadinessSnapshot struct {
	Schema               string          `json:"schema"`
	DeploymentPlanDigest string          `json:"deployment_plan_digest"`
	ObservedAt           time.Time       `json:"observed_at"`
	Hosts                []HostSnapshot  `json:"hosts"`
	Cluster              ClusterSnapshot `json:"cluster"`
	Load                 LoadSnapshot    `json:"load"`
}

// FailureCode is a stable operator and retry classification.
type FailureCode string

const (
	FailureInvalidPlan        FailureCode = "invalid_plan"
	FailureArtifactProvenance FailureCode = "artifact_provenance_invalid"
	FailureArtifactDownload   FailureCode = "artifact_download_failed"
	FailureCredentials        FailureCode = "credential_materialization_failed"
	FailureCredentialCleanup  FailureCode = "credential_cleanup_failed"
	FailureBundleTransfer     FailureCode = "bundle_transfer_failed"
	FailureBundleDigest       FailureCode = "bundle_digest_mismatch"
	FailureHostIdentity       FailureCode = "host_identity_invalid"
	FailureBaseTools          FailureCode = "base_tools_missing"
	FailureDiskMount          FailureCode = "data_disk_mount_invalid"
	FailureDiskCapacity       FailureCode = "filesystem_capacity_insufficient"
	FailureDiskFree           FailureCode = "filesystem_free_space_low"
	FailureTimeDrift          FailureCode = "time_drift_exceeded"
	FailureActivation         FailureCode = "native_activation_failed"
	FailureServiceInactive    FailureCode = "systemd_service_inactive"
	FailureClusterMembership  FailureCode = "cluster_membership_unready"
	FailureSlotTopology       FailureCode = "slot_topology_unready"
	FailureWorkers            FailureCode = "workers_unready"
	FailurePrometheus         FailureCode = "prometheus_targets_unready"
	FailureWorkloadConfig     FailureCode = "workload_config_invalid"
	FailurePublicEndpoints    FailureCode = "public_endpoints_unready"
	FailureAnalysis           FailureCode = "analysis_unready"
	FailureEvidence           FailureCode = "readiness_evidence_invalid"
)

// Gate is the last fully completed deployment gate.
type Gate string

const (
	GateNone              Gate = "none"
	GatePlanValidated     Gate = "plan_validated"
	GateBundleTransferred Gate = "bundle_transferred"
	GateBundleVerified    Gate = "bundle_verified"
	GateHostsPrepared     Gate = "hosts_prepared"
	GateServicesActive    Gate = "services_active"
	GateClusterConverged  Gate = "cluster_converged"
	GateReady             Gate = "ready"
)

// DeploymentFailure contains no raw command output or secret-bearing strings.
type DeploymentFailure struct {
	Schema            string      `json:"schema"`
	Code              FailureCode `json:"code"`
	LastCompletedGate Gate        `json:"last_completed_gate"`
	HostRole          string      `json:"host_role,omitempty"`
	Evidence          []string    `json:"evidence"`
}

// HostProof binds one activated host to the exact bundle and Lease resources.
type HostProof struct {
	Role           string `json:"role"`
	InstanceID     string `json:"instance_id"`
	PrivateAddress string `json:"private_address"`
	PublicAddress  string `json:"public_address,omitempty"`
	DataDiskID     string `json:"data_disk_id"`
	BundleDigest   string `json:"bundle_digest"`
}

// PublicEndpoints are the exact non-secret HTTP entry points exposed through
// the load host for the lifetime of the Lease.
type PublicEndpoints struct {
	Manager string `json:"manager"`
	Demo    string `json:"demo"`
}

// DeploymentReceipt is the non-secret handoff to workload orchestration.
type DeploymentReceipt struct {
	Schema               string          `json:"schema"`
	LeaseID              string          `json:"lease_id"`
	RequestID            string          `json:"request_id"`
	Repository           string          `json:"repository"`
	LeasePlanDigest      string          `json:"lease_plan_digest"`
	DeploymentPlanDigest string          `json:"deployment_plan_digest"`
	SourceSHA            string          `json:"source_sha"`
	ControlSHA           string          `json:"control_sha"`
	BundleDigest         string          `json:"bundle_digest"`
	ActivatedAt          time.Time       `json:"activated_at"`
	LeaseExpiresAt       time.Time       `json:"lease_expires_at"`
	Topology             Topology        `json:"topology"`
	PublicEndpoints      PublicEndpoints `json:"public_endpoints"`
	Hosts                []HostProof     `json:"hosts"`
}

// Outcome carries exactly one successful Receipt or one structured Failure.
type Outcome struct {
	Passed  bool               `json:"passed"`
	Receipt *DeploymentReceipt `json:"receipt,omitempty"`
	Failure *DeploymentFailure `json:"failure,omitempty"`
}

// Fleet is the SSH/native-host boundary used by the deployment controller.
// Provider lifecycle operations are intentionally absent.
type Fleet interface {
	StageBundle(context.Context, HostPlan, string) error
	RelayBundle(context.Context, HostPlan, HostPlan, string) error
	VerifyBundle(context.Context, HostPlan, string) error
	PrepareHost(context.Context, HostPlan) error
	ActivateHost(context.Context, HostPlan) error
	Snapshot(context.Context, DeploymentPlan) (ReadinessSnapshot, error)
}

// BuildPlan converts exact active Lease inventory into the fixed native topology.
func BuildPlan(lease LeaseInventory, manifest Manifest, now time.Time) (DeploymentPlan, error) {
	if manifest.Schema != ManifestSchemaV1 || !validDigest(manifest.IntentSHA256) ||
		strings.TrimSpace(lease.LeaseID) == "" || strings.TrimSpace(lease.RequestID) == "" ||
		strings.TrimSpace(lease.Repository) == "" || strings.TrimSpace(lease.Provider) == "" ||
		strings.TrimSpace(lease.Region) == "" || strings.TrimSpace(lease.Zone) == "" ||
		!validDigest(lease.PlanDigest) || lease.State != "active" || !lease.ExpiresAt.After(now.UTC()) ||
		!validDeploymentBudget(lease.Budget) || lease.CreatedAt.IsZero() || !lease.CreatedAt.Before(lease.ExpiresAt) ||
		lease.SourceSHA != manifest.SourceSHA || lease.BundleDigest != manifest.BundleDigest ||
		!validSHA(lease.SourceSHA) || !validDigest(lease.BundleDigest) || !validSHA(manifest.ControlSHA) {
		return DeploymentPlan{}, ErrInvalidDeployment
	}
	instances := make([]LeaseResource, 0, ServiceHostCount+LoadHostCount)
	dataDisks := make(map[string][]LeaseResource)
	publicByRole := make(map[string]string)
	for _, resource := range lease.Resources {
		switch resource.Kind {
		case "instance":
			instances = append(instances, resource)
		case "data_disk":
			dataDisks[resource.ParentID] = append(dataDisks[resource.ParentID], resource)
		case "public_address":
			if strings.TrimSpace(resource.ID) == "" || resource.Role != "load" || !validIP(resource.PublicAddress) || publicByRole[resource.Role] != "" {
				return DeploymentPlan{}, ErrInvalidDeployment
			}
			publicByRole[resource.Role] = resource.PublicAddress
		}
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Role == instances[j].Role {
			return instances[i].ID < instances[j].ID
		}
		return instances[i].Role < instances[j].Role
	})
	serviceOrdinal := 0
	loadCount := 0
	hosts := make([]HostPlan, 0, ServiceHostCount+LoadHostCount)
	seenInstance := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		if strings.TrimSpace(instance.ID) == "" || !validIP(instance.PrivateAddress) {
			return DeploymentPlan{}, ErrInvalidDeployment
		}
		if _, duplicate := seenInstance[instance.ID]; duplicate {
			return DeploymentPlan{}, ErrInvalidDeployment
		}
		seenInstance[instance.ID] = struct{}{}
		disks := dataDisks[instance.ID]
		if len(disks) != 1 || strings.TrimSpace(disks[0].ID) == "" || disks[0].Role != instance.Role {
			return DeploymentPlan{}, ErrInvalidDeployment
		}
		host := HostPlan{LeaseRole: instance.Role, InstanceID: instance.ID, PrivateAddress: instance.PrivateAddress, DataDiskID: disks[0].ID}
		switch instance.Role {
		case "service":
			serviceOrdinal++
			host.Role = fmt.Sprintf("service-%d", serviceOrdinal)
			host.NodeID = serviceOrdinal
			host.MinimumDataFilesystemBytes = MinimumServiceDataBytes
			if disks[0].SizeBytes < MinimumServiceDataBytes || instance.PublicAddress != "" {
				return DeploymentPlan{}, ErrInvalidDeployment
			}
		case "load":
			loadCount++
			host.Role = "load"
			host.MinimumDataFilesystemBytes = MinimumLoadDataBytes
			host.PublicAddress = instance.PublicAddress
			if host.PublicAddress != "" && publicByRole["load"] != "" && host.PublicAddress != publicByRole["load"] {
				return DeploymentPlan{}, ErrInvalidDeployment
			}
			if host.PublicAddress == "" {
				host.PublicAddress = publicByRole["load"]
			}
			if disks[0].SizeBytes < MinimumLoadDataBytes || !validIP(host.PublicAddress) {
				return DeploymentPlan{}, ErrInvalidDeployment
			}
		default:
			return DeploymentPlan{}, ErrInvalidDeployment
		}
		hosts = append(hosts, host)
	}
	if serviceOrdinal != ServiceHostCount || loadCount != LoadHostCount || len(dataDisks) != len(instances) {
		return DeploymentPlan{}, ErrInvalidDeployment
	}
	sort.Slice(hosts, func(i, j int) bool { return hostOrder(hosts[i].Role) < hostOrder(hosts[j].Role) })
	budget := lease.Budget
	budget.LineItems = append([]DeploymentBudgetLineItem(nil), lease.Budget.LineItems...)
	plan := DeploymentPlan{
		Schema: PlanSchemaV1, LeaseID: lease.LeaseID, RequestID: lease.RequestID,
		Repository: lease.Repository, Provider: lease.Provider, Region: lease.Region, Zone: lease.Zone,
		LeasePlanDigest: lease.PlanDigest, SourceSHA: manifest.SourceSHA, ControlSHA: manifest.ControlSHA,
		BundleDigest: manifest.BundleDigest, LeaseCreatedAt: lease.CreatedAt.UTC(), ExpiresAt: lease.ExpiresAt.UTC(), Budget: budget,
		OperatingSystem: "ubuntu", OSVersion: "24.04", Architecture: "amd64",
		Topology: fixedTopology(), Hosts: hosts,
	}
	plan.PlanDigest = deploymentPlanDigest(plan)
	if err := ValidatePlan(plan, manifest, now); err != nil {
		return DeploymentPlan{}, err
	}
	return plan, nil
}

// ValidatePlan independently checks identity, topology, inventory, and digest.
func ValidatePlan(plan DeploymentPlan, manifest Manifest, now time.Time) error {
	if manifest.Schema != ManifestSchemaV1 || !validDigest(manifest.IntentSHA256) ||
		plan.Schema != PlanSchemaV1 || !validDigest(plan.PlanDigest) || plan.Topology != fixedTopology() ||
		plan.SourceSHA != manifest.SourceSHA || plan.ControlSHA != manifest.ControlSHA || plan.BundleDigest != manifest.BundleDigest ||
		!validDigest(plan.LeasePlanDigest) || plan.OperatingSystem != "ubuntu" || plan.OSVersion != "24.04" ||
		plan.Architecture != "amd64" || plan.LeaseCreatedAt.IsZero() || !plan.LeaseCreatedAt.Before(plan.ExpiresAt) || !plan.ExpiresAt.After(now.UTC()) ||
		!validDeploymentBudget(plan.Budget) || len(plan.Hosts) != 4 ||
		!boundedText(plan.LeaseID, 128) || !boundedText(plan.RequestID, 128) || !boundedText(plan.Repository, 256) ||
		!boundedText(plan.Provider, 64) || !boundedText(plan.Region, 128) || !boundedText(plan.Zone, 128) ||
		deploymentPlanDigest(plan) != plan.PlanDigest {
		return ErrInvalidDeployment
	}
	seenInstances := make(map[string]struct{}, 4)
	seenDisks := make(map[string]struct{}, 4)
	seenPrivateAddresses := make(map[string]struct{}, 4)
	for index, host := range plan.Hosts {
		wantRole := []string{"service-1", "service-2", "service-3", "load"}[index]
		if host.Role != wantRole || !boundedText(host.InstanceID, 256) || !boundedText(host.DataDiskID, 256) ||
			!validIP(host.PrivateAddress) || host.MinimumDataFilesystemBytes != minimumDataBytes(host.Role) {
			return ErrInvalidDeployment
		}
		if strings.HasPrefix(host.Role, "service-") {
			if host.LeaseRole != "service" || host.NodeID != index+1 || host.PublicAddress != "" {
				return ErrInvalidDeployment
			}
		} else if host.LeaseRole != "load" || host.NodeID != 0 || !validIP(host.PublicAddress) {
			return ErrInvalidDeployment
		}
		if _, exists := seenInstances[host.InstanceID]; exists {
			return ErrInvalidDeployment
		}
		if _, exists := seenDisks[host.DataDiskID]; exists {
			return ErrInvalidDeployment
		}
		if _, exists := seenPrivateAddresses[host.PrivateAddress]; exists {
			return ErrInvalidDeployment
		}
		seenInstances[host.InstanceID] = struct{}{}
		seenDisks[host.DataDiskID] = struct{}{}
		seenPrivateAddresses[host.PrivateAddress] = struct{}{}
	}
	return nil
}

// Deploy executes the provider-free transfer, verification, native activation,
// and readiness sequence through an SSH-like Fleet port.
func Deploy(ctx context.Context, fleet Fleet, plan DeploymentPlan, manifest Manifest, now time.Time) Outcome {
	if fleet == nil || ValidatePlan(plan, manifest, now) != nil {
		return failed(FailureInvalidPlan, GateNone, "", "deployment plan validation failed")
	}
	load, _ := findHost(plan.Hosts, "load")
	if err := fleet.StageBundle(ctx, load, plan.BundleDigest); err != nil {
		return failed(FailureBundleTransfer, GatePlanValidated, "load", "load host staging failed")
	}
	for _, host := range plan.Hosts[:ServiceHostCount] {
		if err := fleet.RelayBundle(ctx, load, host, plan.BundleDigest); err != nil {
			return failed(FailureBundleTransfer, GatePlanValidated, host.Role, "private host relay failed")
		}
	}
	for _, host := range plan.Hosts {
		if err := fleet.VerifyBundle(ctx, host, plan.BundleDigest); err != nil {
			return failed(FailureBundleDigest, GateBundleTransferred, host.Role, "host bundle verification failed")
		}
	}
	for _, host := range plan.Hosts {
		if err := fleet.PrepareHost(ctx, host); err != nil {
			return failed(FailureDiskMount, GateBundleVerified, host.Role, "host preparation failed")
		}
	}
	for _, host := range plan.Hosts {
		if err := fleet.ActivateHost(ctx, host); err != nil {
			return failed(FailureActivation, GateHostsPrepared, host.Role, "native service activation failed")
		}
	}
	snapshot, err := fleet.Snapshot(ctx, plan)
	if err != nil {
		return failed(FailureEvidence, GateServicesActive, "", "readiness snapshot unavailable")
	}
	return EvaluateReadiness(plan, snapshot, now)
}

// EvaluateReadiness returns every successful identity in a typed receipt or
// stops at the first stable gate failure with bounded generated evidence.
func EvaluateReadiness(plan DeploymentPlan, snapshot ReadinessSnapshot, now time.Time) Outcome {
	if snapshot.Schema != SnapshotSchemaV1 || snapshot.DeploymentPlanDigest != plan.PlanDigest ||
		snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.Before(now.UTC().Add(-MaximumReadinessAge)) ||
		snapshot.ObservedAt.After(now.UTC().Add(time.Minute)) || len(snapshot.Hosts) != len(plan.Hosts) {
		return failed(FailureEvidence, GateServicesActive, "", "readiness identity mismatch")
	}
	byRole := make(map[string]HostSnapshot, len(snapshot.Hosts))
	for _, host := range snapshot.Hosts {
		if _, duplicate := byRole[host.Role]; duplicate {
			return failed(FailureEvidence, GateServicesActive, host.Role, "duplicate host evidence")
		}
		byRole[host.Role] = host
	}
	for _, planned := range plan.Hosts {
		host, ok := byRole[planned.Role]
		if !ok || host.OperatingSystem != plan.OperatingSystem || host.OSVersion != plan.OSVersion || host.Architecture != "x86_64" {
			return failed(FailureHostIdentity, GateServicesActive, planned.Role, "Ubuntu 24.04 x86_64 identity failed")
		}
		if !host.BaseToolsAvailable {
			return failed(FailureBaseTools, GateServicesActive, planned.Role, "required offline base tools are missing")
		}
		if host.BundleDigest != plan.BundleDigest {
			return failed(FailureBundleDigest, GateServicesActive, planned.Role, "runtime bundle digest differs")
		}
		if host.DataDiskID != planned.DataDiskID || host.DataMount != "/var/lib/wukongim-cloud" {
			return failed(FailureDiskMount, GateServicesActive, planned.Role, "planned data disk is not mounted")
		}
		if host.SystemFilesystemBytes < MinimumSystemDiskBytes || host.DataFilesystemBytes < planned.MinimumDataFilesystemBytes {
			return failed(FailureDiskCapacity, GateServicesActive, planned.Role, "filesystem is smaller than the deployment minimum")
		}
		if belowFreeThreshold(host.SystemFreeBytes, host.SystemFilesystemBytes) || belowFreeThreshold(host.DataFreeBytes, host.DataFilesystemBytes) {
			return failed(FailureDiskFree, GateServicesActive, planned.Role, "filesystem has less than five percent free")
		}
		if host.ClockOffsetMilliseconds < -MaximumClockDriftMillis || host.ClockOffsetMilliseconds > MaximumClockDriftMillis {
			return failed(FailureTimeDrift, GateServicesActive, planned.Role, "clock drift exceeds one second")
		}
		for _, unit := range requiredUnits(planned.Role) {
			if !slices.Contains(host.ActiveUnits, unit) {
				return failed(FailureServiceInactive, GateHostsPrepared, planned.Role, "required systemd unit is inactive: "+unit)
			}
		}
	}
	cluster := snapshot.Cluster
	if cluster.ReadyNodes != ServiceHostCount || cluster.Members != ServiceHostCount || cluster.PendingControllerTasks != 0 {
		return failed(FailureClusterMembership, GateServicesActive, "", "three-node cluster has not converged")
	}
	if cluster.PhysicalHashSlots != PhysicalHashSlotCount || cluster.HealthySlotLeaders != PhysicalHashSlotCount ||
		cluster.HealthySlotReplicaSets != PhysicalHashSlotCount || cluster.LogicalSlotGroups != LogicalSlotGroupCount ||
		cluster.RuntimeConfigNodes != ServiceHostCount || cluster.SlotReplicas != SlotReplicaCount || cluster.ChannelReplicas != ChannelReplicaCount {
		return failed(FailureSlotTopology, GateClusterConverged, "", "256 physical slots or 12 logical groups are unready")
	}
	if snapshot.Load.ReadyWorkers != 3 {
		return failed(FailureWorkers, GateClusterConverged, "load", "three workers are not ready")
	}
	if snapshot.Load.PrometheusTargetsWant != PrometheusTargetCount || snapshot.Load.PrometheusTargetsUp != PrometheusTargetCount {
		return failed(FailurePrometheus, GateClusterConverged, "load", "Prometheus targets are incomplete")
	}
	if !snapshot.Load.WorkloadConfigValid {
		return failed(FailureWorkloadConfig, GateClusterConverged, "load", "formal workload config validation failed")
	}
	if !snapshot.Load.ProxyReady || !snapshot.Load.ManagerReady || !snapshot.Load.DemoReady {
		return failed(FailurePublicEndpoints, GateClusterConverged, "load", "HTTP proxy, Manager, or Demo is unready")
	}
	if !snapshot.Load.AnalysisReady {
		return failed(FailureAnalysis, GateClusterConverged, "load", "Analysis gateway is unready")
	}
	hostProofs := make([]HostProof, 0, len(plan.Hosts))
	for _, host := range plan.Hosts {
		hostProofs = append(hostProofs, HostProof{Role: host.Role, InstanceID: host.InstanceID, PrivateAddress: host.PrivateAddress,
			PublicAddress: host.PublicAddress, DataDiskID: host.DataDiskID, BundleDigest: plan.BundleDigest})
	}
	load, found := findHost(plan.Hosts, "load")
	publicEndpoints, validEndpoints := deploymentPublicEndpoints(load.PublicAddress)
	if !found || !validEndpoints {
		return failed(FailureEvidence, GateClusterConverged, "load", "public endpoint identity is invalid")
	}
	receipt := &DeploymentReceipt{
		Schema: ReceiptSchemaV1, LeaseID: plan.LeaseID, RequestID: plan.RequestID, Repository: plan.Repository,
		LeasePlanDigest: plan.LeasePlanDigest, DeploymentPlanDigest: plan.PlanDigest,
		SourceSHA: plan.SourceSHA, ControlSHA: plan.ControlSHA, BundleDigest: plan.BundleDigest,
		ActivatedAt: snapshot.ObservedAt.UTC(), LeaseExpiresAt: plan.ExpiresAt.UTC(), Topology: plan.Topology,
		PublicEndpoints: publicEndpoints, Hosts: hostProofs,
	}
	if !receipt.ActivatedAt.Before(receipt.LeaseExpiresAt) {
		return failed(FailureEvidence, GateClusterConverged, "", "readiness timestamp is invalid")
	}
	return Outcome{Passed: true, Receipt: receipt}
}

func deploymentPublicEndpoints(publicAddress string) (PublicEndpoints, bool) {
	address, err := netip.ParseAddr(publicAddress)
	if err != nil {
		return PublicEndpoints{}, false
	}
	host := address.String()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	base := "http://" + host
	return PublicEndpoints{Manager: base + "/", Demo: base + "/demo/"}, true
}

func fixedTopology() Topology {
	return Topology{ServiceNodes: ServiceHostCount, LoadNodes: LoadHostCount, PhysicalHashSlots: PhysicalHashSlotCount,
		LogicalSlotGroups: LogicalSlotGroupCount, SlotReplicas: SlotReplicaCount, ChannelReplicas: ChannelReplicaCount}
}

func requiredUnits(role string) []string {
	common := []string{"node-exporter.service", "wukongim-process-metrics.service", "wukongim-evidence.timer"}
	if strings.HasPrefix(role, "service-") {
		return append([]string{"wukongim.service", "wkbench-host-metrics.service"}, common...)
	}
	return append([]string{
		"wkbench-host-metrics.service",
		"wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service",
		"prometheus.service", "wkanalysis.service", "caddy.service",
	}, common...)
}

func failed(code FailureCode, gate Gate, host string, evidence ...string) Outcome {
	if hostOrder(host) == 99 {
		host = ""
	}
	if len(evidence) > 8 {
		evidence = evidence[:8]
	}
	for index := range evidence {
		evidence[index] = strings.TrimSpace(evidence[index])
		if len(evidence[index]) > 256 {
			evidence[index] = evidence[index][:256]
		}
	}
	return Outcome{Failure: &DeploymentFailure{Schema: FailureSchemaV1, Code: code, LastCompletedGate: gate, HostRole: host, Evidence: evidence}}
}

func deploymentPlanDigest(plan DeploymentPlan) string {
	plan.PlanDigest = ""
	encoded, _ := json.Marshal(plan)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validIP(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4() && !address.IsUnspecified()
}

func boundedText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum
}

func minimumDataBytes(role string) int64 {
	if role == "load" {
		return MinimumLoadDataBytes
	}
	return MinimumServiceDataBytes
}

func validDeploymentBudget(budget DeploymentBudget) bool {
	if budget.Currency != "CNY" || budget.LimitMicros != FormalBudgetHardMicros ||
		budget.OperationalStopMicros != FormalBudgetStopMicros || budget.CommittedMicros < 0 ||
		budget.EstimatedCostMicros <= 0 || budget.CommittedMicros >= budget.OperationalStopMicros || len(budget.LineItems) == 0 {
		return false
	}
	var total int64
	for _, item := range budget.LineItems {
		if !boundedText(item.Kind, 64) || !boundedText(item.Role, 64) || item.Quantity <= 0 || item.CostMicros <= 0 ||
			item.CostMicros > math.MaxInt64-total {
			return false
		}
		total += item.CostMicros
	}
	return total == budget.EstimatedCostMicros && budget.EstimatedCostMicros <= budget.OperationalStopMicros-budget.CommittedMicros
}

func hostOrder(role string) int {
	switch role {
	case "service-1":
		return 1
	case "service-2":
		return 2
	case "service-3":
		return 3
	case "load":
		return 4
	default:
		return 99
	}
}

func findHost(hosts []HostPlan, role string) (HostPlan, bool) {
	for _, host := range hosts {
		if host.Role == role {
			return host, true
		}
	}
	return HostPlan{}, false
}

func belowFreeThreshold(free, total int64) bool {
	return total <= 0 || free < total*MinimumFreePercent/100
}
