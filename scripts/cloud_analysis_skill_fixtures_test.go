package scripts_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCloudAnalysisSkillDocumentsProcessLossGuard(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"node_memory_percent", "node_oom_kills", "process_start_time_seconds",
		"gateway_active_connections", "channel_active_channels",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route process-loss analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	if !strings.Contains(skill, "invalidates performance and storage calibration") {
		t.Fatal("SKILL.md must reject calibration evidence after a node OOM or process restart")
	}
	for _, required := range []string{"step_seconds=5", "`inuse_space`", "`alloc_space`"} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document bounded transient-memory evidence %q", required)
		}
		if required != "step_seconds=5" && !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document heap sample view %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsFailureOperationRouting(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, operation := range []string{
		"person_sendack_lock", "person_send", "person_sendack", "person_recv", "person_recvack",
		"group_sendack_lock", "group_send", "group_sendack", "group_recv", "group_recvack",
		"worker_status", "phase_completion",
	} {
		if !strings.Contains(contract, "`"+operation+"`") {
			t.Fatalf("tool-contract.md must list failure operation %q", operation)
		}
	}
	for _, route := range []string{"sendack_lock", "send` or `group_send", "sendack`", "recv` or `group_recv", "recvack`"} {
		if !strings.Contains(skill, route) {
			t.Fatalf("SKILL.md must route failure operation family %q", route)
		}
	}
	for _, route := range []string{"`worker_status`", "`phase_completion`"} {
		if !strings.Contains(skill, route) {
			t.Fatalf("SKILL.md must route timeout stage %q", route)
		}
	}
	for _, required := range []string{"`worker_stop_failed`", "`stop`"} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must route terminal stop evidence %q", required)
		}
		if !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document terminal stop evidence %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsObservationNodeContract(t *testing.T) {
	contract := readFile(t, filepath.Join(repoRoot(t), ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, required := range []string{"Observation node is `cluster`", "no simulator log target"} {
		if !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsRecipientDeliveryAndPostCommitEvidence(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"delivery_recipient_worker_queue_depth",
		"delivery_recipient_worker_queue_capacity",
		"delivery_recipient_worker_inflight",
		"delivery_recipient_worker_capacity",
		"delivery_recipient_worker_admission_cumulative",
		"delivery_recipient_worker_admission_wait_p99",
		"delivery_recipient_worker_process_cumulative",
		"delivery_recipient_worker_process_p99",
		"delivery_recipient_worker_process_recipients_cumulative",
		"channelappend_post_commit_handoff_depth",
		"channelappend_post_commit_handoff_capacity",
		"channelappend_post_commit_retry_queue_depth",
		"channelappend_post_commit_retry_contended",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route recipient delivery analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	for _, required := range []string{
		"accepted_delta - processed_delta = backlog_end - backlog_start",
		"quiescent bracketing endpoint samples",
		"planned or attempted recipients",
		"pending append, append in-flight",
		"`ErrChannelBusy`",
		"retry depth can be zero while contended remains one",
		"must remain approximate or unknown",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document recipient delivery evidence rule %q", required)
		}
		if !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document recipient delivery evidence rule %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsRecipientPipelineStageEvidence(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"delivery_recipient_authority_resolve_rate",
		"delivery_recipient_authority_resolve_items_rate",
		"delivery_recipient_authority_resolve_targets_rate",
		"delivery_recipient_authority_resolve_p99",
		"presence_endpoint_lookup_rate",
		"presence_endpoint_lookup_items_rate",
		"presence_endpoint_lookup_groups_rate",
		"presence_endpoint_lookup_p99",
		"delivery_ack_batch_cumulative",
		"delivery_ack_batch_items_cumulative",
		"delivery_ack_batch_shards_cumulative",
		"delivery_ack_batch_rejected_cumulative",
		"delivery_ack_batch_rollback_cumulative",
		"delivery_ack_batch_p99",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route recipient pipeline analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	for _, required := range []string{
		"one recipient batch can",
		"`stale_retry`",
		"observed in both phases",
		"quiescent bracketing",
		"unknown rather than zero",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document recipient pipeline evidence rule %q", required)
		}
		if !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document recipient pipeline evidence rule %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsConversationActiveConservation(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"conversation_active_cache_rows",
		"conversation_active_dirty_rows",
		"conversation_active_dirty_queue_rows",
		"conversation_active_dirty_age_buckets",
		"conversation_active_oldest_dirty_age",
		"conversation_active_dirty_mutation_rate",
		"conversation_active_cache_lock_p99",
		"conversation_active_flush_rows_cumulative",
		"conversation_active_flush_stage_p99",
		"conversation_active_flush_attempt_rate",
		"conversation_active_pressure_events",
		"conversation_active_pressure_state",
		"conversation_active_pressure_wakeup_p99",
		"storage_commit_queue_depth",
		"storage_commit_request_p99",
		"storage_commit_batch_stage_p99",
		"slot_proposal_rate",
		"slot_proposal_apply_p99",
		"slot_apply_gap",
		"slot_background_proposal_admission_rate",
		"slot_runtime_queue_pressure",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route conversation-active analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	for _, required := range []string{
		"selected = persisted + skipped",
		"selected = cleared + requeued + superseded",
		"durable row count is unknown",
		"exact measured-window",
		"two consecutive",
		"`clear_lock_wait`",
		"`clear_apply`",
		"`cache_pressure`",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document conversation-active evidence rule %q", required)
		}
	}
}

func TestCloudAnalysisSkillDocumentsPreferredLeaderEvidence(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"slot_preferred_leader_reconcile_rate",
		"slot_preferred_leader_strict_wait_p99",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route preferred-leader analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	for _, required := range []string{
		"`transfer_started` means",
		"`TransferLeader` was issued",
		"unknown loop evidence",
		"never substitute",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document preferred-leader evidence rule %q", required)
		}
	}
	for _, required := range []string{
		"`slot_id`",
		"`stage=slot.preferred_leader_reconcile`",
		"`actual_leader_id`",
		"`preferred_leader_id`",
		"`raft_term`",
		"`config_epoch`",
		"intentionally omitted",
		"node-local events",
		"recovery evidence",
		"owning Slot",
		"remain unknown",
		"former leader",
		"cluster-wide",
		"at most once every 30 seconds",
		"event counts",
		"Prometheus",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document preferred-leader diagnostics contract %q", required)
		}
		if !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document preferred-leader diagnostics contract %q", required)
		}
	}
}

func TestCloudAnalysisSkillFixturesCoverEveryVerdict(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot(t), ".agents", "skills", "wukongim-cloud-analysis", "fixtures")
	stopFixturePath := filepath.Join(fixtureDir, "worker_stop_failed.json")
	if _, err := os.Stat(stopFixturePath); err != nil {
		t.Fatalf("terminal stop fixture must be committed: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	want := []string{"healthy", "infrastructure_interrupted", "insufficient_evidence", "product_defect", "scenario_invalid"}
	allowedVerdicts := make(map[string]struct{}, len(want))
	for _, verdict := range want {
		allowedVerdicts[verdict] = struct{}{}
	}
	got := make([]string, 0, len(paths))
	for _, path := range paths {
		fixture := decodeCloudAnalysisSkillFixture(t, path)
		if fixture.Schema != "wukongim.analysis.skill_fixture/v1" || fixture.Name == "" || fixture.ExpectedVerdict == "" {
			t.Fatalf("fixture %s identity = %#v", path, fixture)
		}
		if _, ok := allowedVerdicts[fixture.ExpectedVerdict]; !ok {
			t.Fatalf("fixture %s verdict = %q, want one of %v", path, fixture.ExpectedVerdict, want)
		}
		if fixture.RunID == "" || len(fixture.Observations) < 2 || fixture.Observations[0].Tool != "run_inspect" {
			t.Fatalf("fixture %s must begin with run_inspect", path)
		}
		if fixture.Observations[1].Tool != "workload_inspect" {
			t.Fatalf("fixture %s must inspect the final workload before other live sources", path)
		}
		for _, observation := range fixture.Observations {
			if observation.RunID != fixture.RunID || observation.Node == "" || observation.Source == "" ||
				observation.ObservedAt.IsZero() || observation.Window.Start.IsZero() || observation.Window.End.IsZero() ||
				observation.Window.End.Before(observation.Window.Start) || observation.Completeness == "" || observation.Warnings == nil {
				t.Fatalf("fixture %s observation is not run-bound: %#v", path, observation)
			}
		}
		if fixture.Name == "scenario_invalid" {
			if fixture.ExpectedRoute != "worker_failure" {
				t.Fatalf("fixture %s route = %q, want worker_failure", path, fixture.ExpectedRoute)
			}
			failedWorkers, ok := fixture.Observations[1].Data["failed_workers"].([]any)
			if !ok || len(failedWorkers) == 0 {
				t.Fatalf("fixture %s must expose structured worker failure evidence", path)
			}
		}
		if fixture.Name == "worker_status_mismatch" {
			if fixture.ExpectedVerdict != "scenario_invalid" || fixture.ExpectedRoute != "worker_status_mismatch" {
				t.Fatalf("fixture %s mismatch route = %q/%q", path, fixture.ExpectedVerdict, fixture.ExpectedRoute)
			}
			failedWorkers, ok := fixture.Observations[1].Data["failed_workers"].([]any)
			if !ok || len(failedWorkers) != 1 {
				t.Fatalf("fixture %s must expose one status mismatch", path)
			}
			failure, ok := failedWorkers[0].(map[string]any)
			if !ok || failure["reason_code"] != "worker_status_mismatch" {
				t.Fatalf("fixture %s reason = %#v, want worker_status_mismatch", path, failedWorkers[0])
			}
		}
		if fixture.Name == "worker_stop_failed" {
			if fixture.ExpectedVerdict != "insufficient_evidence" || fixture.ExpectedRoute != "worker_stop_failed" {
				t.Fatalf("fixture %s stop-failure route = %q/%q", path, fixture.ExpectedVerdict, fixture.ExpectedRoute)
			}
			if len(fixture.Observations) < 3 || fixture.Observations[2].Tool != "cluster_snapshot" {
				t.Fatalf("fixture %s must establish cluster state before stop-failure drilldown", path)
			}
			failedWorkers, ok := fixture.Observations[1].Data["failed_workers"].([]any)
			if !ok || len(failedWorkers) != 1 {
				t.Fatalf("fixture %s must expose one terminal stop failure", path)
			}
			failure, ok := failedWorkers[0].(map[string]any)
			if !ok || failure["phase"] != "stop" || failure["reason_code"] != "worker_stop_failed" {
				t.Fatalf("fixture %s stop failure = %#v", path, failedWorkers[0])
			}
			simulatorMemoryObserved := false
			for _, observation := range fixture.Observations[3:] {
				switch observation.Tool {
				case "metrics_query_range":
					if observation.Node != "cluster" || observation.Source != "prometheus" {
						t.Fatalf("fixture %s Prometheus observation node/source = %q/%q, want cluster/prometheus", path, observation.Node, observation.Source)
					}
					queryID, _ := observation.Data["query_id"].(string)
					if queryID == "process_start_time_seconds" {
						t.Fatalf("fixture %s must not use the server process-start query as simulator continuity evidence", path)
					}
					if queryID == "simulator_memory_percent" {
						simulatorMemoryObserved = true
					}
				case "logs_search":
					if observation.Node == "sim" {
						t.Fatalf("fixture %s must not invent a simulator logs_search node", path)
					}
				}
			}
			if !simulatorMemoryObserved {
				t.Fatalf("fixture %s must retain bounded simulator headroom evidence", path)
			}
		}
		if fixture.Name == "process_loss_ambiguous" {
			if fixture.ExpectedVerdict != "insufficient_evidence" || fixture.ExpectedRoute != "process_loss" {
				t.Fatalf("fixture %s process-loss route = %q/%q", path, fixture.ExpectedVerdict, fixture.ExpectedRoute)
			}
			queryIDs := make(map[string]struct{})
			for _, observation := range fixture.Observations {
				if queryID, ok := observation.Data["query_id"].(string); ok {
					queryIDs[queryID] = struct{}{}
				}
			}
			for _, queryID := range []string{"node_oom_kills", "process_start_time_seconds", "channel_active_channels"} {
				if _, ok := queryIDs[queryID]; !ok {
					t.Fatalf("fixture %s missing process-loss query %q", path, queryID)
				}
			}
		}
		got = append(got, fixture.ExpectedVerdict)
	}
	sort.Strings(got)
	for _, verdict := range want {
		index := sort.SearchStrings(got, verdict)
		if index == len(got) || got[index] != verdict {
			t.Fatalf("fixture verdicts = %v, missing %q", got, verdict)
		}
	}
}

type cloudAnalysisSkillFixture struct {
	Schema          string                          `json:"schema"`
	Name            string                          `json:"name"`
	RunID           string                          `json:"run_id"`
	Observations    []cloudAnalysisSkillObservation `json:"observations"`
	ExpectedVerdict string                          `json:"expected_verdict"`
	ExpectedRoute   string                          `json:"expected_route,omitempty"`
}

type cloudAnalysisSkillObservation struct {
	Tool         string                   `json:"tool"`
	RunID        string                   `json:"run_id"`
	Node         string                   `json:"node"`
	Source       string                   `json:"source"`
	ObservedAt   time.Time                `json:"observed_at"`
	Window       cloudAnalysisSkillWindow `json:"window"`
	Completeness string                   `json:"completeness"`
	Warnings     []string                 `json:"warnings"`
	Data         map[string]any           `json:"data"`
}

type cloudAnalysisSkillWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func decodeCloudAnalysisSkillFixture(t *testing.T, path string) cloudAnalysisSkillFixture {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var fixture cloudAnalysisSkillFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("fixture %s contains trailing JSON", path)
	}
	return fixture
}
