package deploy

import "fmt"

// BootstrapSnapshot is the bounded evidence collected before workload start.
type BootstrapSnapshot struct {
	// BundleDigests maps every host role to its locally verified digest.
	BundleDigests         map[string]string   `json:"bundle_digests"`
	// ActiveServices maps every host role to its active systemd units.
	ActiveServices        map[string][]string `json:"active_services"`
	// ReadyNodeIDs contains cluster node IDs whose readiness probes passed.
	ReadyNodeIDs          []uint64            `json:"ready_node_ids"`
	// ClusterMemberCount is the count of active, runtime-ready cluster members.
	ClusterMemberCount    int                 `json:"cluster_member_count"`
	// HashSlotCount is the converged cluster hash-slot count.
	HashSlotCount         int                 `json:"hash_slot_count"`
	// HealthySlotLeaders is the count of slots with a healthy current leader.
	HealthySlotLeaders    int                 `json:"healthy_slot_leaders"`
	// HealthySlotReplicas is the count of slots with the required voter set.
	HealthySlotReplicas   int                 `json:"healthy_slot_replicas"`
	// PendingControllerTask is the count of unconverged controller tasks.
	PendingControllerTask int                 `json:"pending_controller_tasks"`
	// PrometheusTargetsUp is the number of currently healthy scrape targets.
	PrometheusTargetsUp   int                 `json:"prometheus_targets_up"`
	// PrometheusTargetsWant is the expected scrape target count.
	PrometheusTargetsWant int                 `json:"prometheus_targets_want"`
	// AnalysisMCPSelfCheck reports the simulator gateway dependency check.
	AnalysisMCPSelfCheck  bool                `json:"analysis_mcp_self_check"`
	// WKBenchValidate reports strict scenario validation success.
	WKBenchValidate       bool                `json:"wkbench_validate"`
	// WKBenchDoctor reports worker and target preflight success.
	WKBenchDoctor         bool                `json:"wkbench_doctor"`
}

// GateResult reports every failed invariant instead of passing partial evidence.
type GateResult struct {
	// Passed is true only when every bootstrap invariant holds.
	Passed   bool     `json:"passed"`
	// Failures contains every bounded failed invariant.
	Failures []string `json:"failures"`
}

// EvaluateBootstrapGate checks the exact three-node, 256-slot cloud contract.
func EvaluateBootstrapGate(snapshot BootstrapSnapshot, expectedDigest string) GateResult {
	result := GateResult{Failures: make([]string, 0)}
	for _, role := range []string{"node-1", "node-2", "node-3", "sim"} {
		if snapshot.BundleDigests[role] != expectedDigest {
			result.Failures = append(result.Failures, fmt.Sprintf("%s bundle digest mismatch", role))
		}
	}
	required := map[string][]string{
		"node-1": {"wukongim", "node-exporter"}, "node-2": {"wukongim", "node-exporter"}, "node-3": {"wukongim", "node-exporter"},
		"sim": {"wkbench-worker", "prometheus", "node-exporter", "wkanalysis"},
	}
	for role, services := range required {
		active := make(map[string]struct{}, len(snapshot.ActiveServices[role]))
		for _, service := range snapshot.ActiveServices[role] {
			active[service] = struct{}{}
		}
		for _, service := range services {
			if _, ok := active[service]; !ok {
				result.Failures = append(result.Failures, fmt.Sprintf("%s service %s inactive", role, service))
			}
		}
	}
	if len(snapshot.ReadyNodeIDs) != 3 {
		result.Failures = append(result.Failures, "three node readiness probes did not pass")
	}
	if snapshot.ClusterMemberCount != 3 {
		result.Failures = append(result.Failures, "cluster member count is not three")
	}
	if snapshot.HashSlotCount != 256 || snapshot.HealthySlotLeaders != 256 || snapshot.HealthySlotReplicas != 256 {
		result.Failures = append(result.Failures, "256 slots, leaders, and replicas are not healthy")
	}
	if snapshot.PendingControllerTask != 0 {
		result.Failures = append(result.Failures, "controller convergence is pending")
	}
	if snapshot.PrometheusTargetsWant <= 0 || snapshot.PrometheusTargetsUp != snapshot.PrometheusTargetsWant {
		result.Failures = append(result.Failures, "Prometheus targets are incomplete")
	}
	if !snapshot.AnalysisMCPSelfCheck {
		result.Failures = append(result.Failures, "Analysis MCP self-check failed")
	}
	if !snapshot.WKBenchValidate || !snapshot.WKBenchDoctor {
		result.Failures = append(result.Failures, "wkbench validate or doctor failed")
	}
	result.Passed = len(result.Failures) == 0
	return result
}
