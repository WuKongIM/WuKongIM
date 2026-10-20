package scripts_test

// Failure inventory (before implementation): a mixed-mode job or mutable product;
// changing build path/toolchain/digest or rebuilding between blocks; treating new
// harness as product source; altered load/thresholds/order; invalid or drifting
// affinity/process/age/sampler evidence; hiding numeric rejects; leaking a child;
// skipped Linux preflight; retrying or publishing; losing failed-run evidence.
import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestConversationCPUContentionPlanFreezesProductLoadOrderAndBounds(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "run-conversation-cpu-contention.py")
	sha := strings.Repeat("c", 40)
	out, err := exec.Command("python3", "-B", script, "--plan", "--harness-sha", sha).CombinedOutput()
	require.NoError(t, err, "%s", out)
	var p struct {
		Product  string                      `json:"product_sha"`
		Digest   string                      `json:"expected_binary_sha256"`
		Harness  string                      `json:"harness_sha"`
		Build    string                      `json:"canonical_source_directory"`
		Sequence []struct{ ID, Mode string } `json:"sequence"`
		Age      int                         `json:"preparation_age_seconds"`
		Block    int                         `json:"diagnostic_timeout_seconds"`
		Script   int                         `json:"script_timeout_minutes"`
		Job      int                         `json:"job_timeout_minutes"`
		Gate     bool                        `json:"run_release_matrix"`
	}
	require.NoError(t, json.Unmarshal(out, &p))
	require.Equal(t, "ef798352c40a94e57f768f76b2f4713712f827fe", p.Product)
	require.Equal(t, "8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d", p.Digest)
	require.Equal(t, sha, p.Harness)
	require.Equal(t, "/home/runner/work/_temp/conversation-fixed-host-build", p.Build)
	var ids, modes []string
	for _, b := range p.Sequence {
		ids = append(ids, b.ID)
		modes = append(modes, b.Mode)
	}
	require.Equal(t, []string{"01-S", "02-O", "03-D", "04-D", "05-O", "06-S"}, ids)
	require.Equal(t, []string{"shared", "overlap", "disjoint", "disjoint", "overlap", "shared"}, modes)
	require.Equal(t, 180, p.Age)
	require.Equal(t, 720, p.Block)
	require.Equal(t, 84, p.Script)
	require.Equal(t, 90, p.Job)
	require.False(t, p.Gate)
	for _, arg := range []string{"--product-sha", "--expected-binary-sha256", "--qps", "--comparison-sha"} {
		result, e := exec.Command("python3", "-B", script, "--plan", "--harness-sha", sha, arg, "1").CombinedOutput()
		require.Error(t, e, "%s", result)
	}
}

func TestConversationCPUContentionWorkflowContracts(t *testing.T) {
	raw := readWorkflow(t, "conversation-qps-diagnose.yml")
	var w struct {
		On map[string]struct {
			Inputs map[string]struct {
				Type    string
				Default bool
			}
		} `yaml:"on"`
		Permissions map[string]string
		Jobs        map[string]struct {
			If          string
			RunsOn      string `yaml:"runs-on"`
			Timeout     int    `yaml:"timeout-minutes"`
			Environment any
			Steps       []struct {
				Run  string
				If   string
				With map[string]any
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(raw, &w))
	require.Len(t, w.Jobs, 3)
	require.Equal(t, "boolean", w.On["workflow_dispatch"].Inputs["cpu_contention_validation"].Type)
	require.False(t, w.On["workflow_dispatch"].Inputs["cpu_contention_validation"].Default)
	require.Equal(t, "${{ !inputs.fixed_host_validation && !inputs.cpu_contention_validation }}", w.Jobs["diagnose"].If)
	require.Equal(t, "${{ inputs.fixed_host_validation && !inputs.cpu_contention_validation }}", w.Jobs["fixed-host"].If)
	j := w.Jobs["cpu-contention"]
	require.Equal(t, "${{ inputs.cpu_contention_validation }}", j.If)
	require.Equal(t, "ubuntu-24.04", j.RunsOn)
	require.Equal(t, 90, j.Timeout)
	require.Nil(t, j.Environment)
	require.Equal(t, map[string]string{"contents": "read"}, w.Permissions)
	s := string(raw)
	for _, required := range []string{"CPU_CONTENTION_WORKFLOW_SHA: ${{ github.sha }}", "CPU_CONTENTION_FIXED_HOST: ${{ inputs.fixed_host_validation }}", "CPU_CONTENTION_RELEASE_MATRIX: ${{ inputs.run_release_matrix }}", `[[ "$CPU_CONTENTION_FIXED_HOST" == false && "$CPU_CONTENTION_RELEASE_MATRIX" == false ]]`, `[[ "$PRODUCT_SHA" == ef798352c40a94e57f768f76b2f4713712f827fe ]]`, "conversation-cpu-contention-preflight", "run-conversation-cpu-contention.py", "conversation-cpu-contention-${{ github.run_id }}-${{ github.run_attempt }}"} {
		require.Contains(t, s, required)
	}
	var preflight, runIndex int = -1, -1
	for i, step := range j.Steps {
		if strings.Contains(step.Run, "TestConversationFixedHostTimeoutCleansChildProcessGroups") {
			preflight = i
		}
		if strings.Contains(step.Run, "python3 scripts/run-conversation-cpu-contention.py") {
			runIndex = i
		}
	}
	require.GreaterOrEqual(t, preflight, 0)
	require.Greater(t, runIndex, preflight)
	require.NotContains(t, s, "secrets.")
	require.NotContains(t, s, "continue-on-error:")
	for _, name := range []string{"TestCPUPlacementLinuxAllThreads", "TestCPUWindowSamplerLinuxPreflight"} {
		require.Contains(t, s, name)
	}
	require.Contains(t, s, `all($tests[]; . as $name | ([ $events[] | select(.Test == $name and .Action == "pass") ] | length) == 1 and (any($events[]; .Test == $name and .Action == "skip") | not))`)
	source := readFile(t, filepath.Join(repoRoot(t), "scripts", "run-conversation-cpu-contention.py"))
	for _, required := range []string{`run-conversation-fixed-host.py`, `fixed.run_command`, `fixed.cgroup_constraints`, `fixed.numeric_phase_pass`, `"-test.count=1"`, `"-test.timeout=12m"`, `"WK_E2E_CONVERSATION_CPU_PLACEMENT"`, `"WK_E2E_CONVERSATION_FIXED_HOST": "1"`, `TestConversationQPSCPUPlacement`, `signal.alarm(84 * 60)`, `"numeric_pass"`, `"complete"`} {
		require.Contains(t, source, required)
	}
	require.NotContains(t, source, "TestConversationQPSReleaseGate")
	require.NotContains(t, source, "shell=True")
}

func TestConversationCPUContentionReceiptRetainsRedAndRejectsChangedControls(t *testing.T) {
	code := `import copy,importlib.util,sys
spec=importlib.util.spec_from_file_location("contention",sys.argv[1]); m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
p={"min_completion_ratio":.95,"max_p99_ms":500,"stress_gate":{"mixed_seconds":60,"mixed_workers_per_endpoint":8,"mixed_list_qps":200,"mixed_sync_qps":60,"max_allocated_bytes_per_request":{"mixed":100}}}
proc=lambda pid,mask:{"pid":pid,"starttime_ticks":pid*10,"threads":[{"tid":pid,"starttime_ticks":pid*10,"affinity":mask}]}
top=[{"cpu":i,"package_id":0,"core_id":i//2,"numa_node":0,"thread_siblings":[i//2*2,i//2*2+1]} for i in range(4)]
placement={"mode":"O","host_cpus":[0,1,2,3],"driver_cpus":[0],"node_cpus":[0,2,3],"topology":top,"valid":True,"duration_ns":1000000,"attempts":1,"started_at":"2026-10-21T00:03:00.001Z","finished_at":"2026-10-21T00:03:00.002Z","initial":[proc(i,[0,1,2,3]) for i in range(1,5)],"applied":[proc(1,[0])]+[proc(i,[0,2,3]) for i in range(2,5)]}
f={"schema":"wukongim/conversation-cpu-placement-preparation/v1","valid":True,"target_age_seconds":180,"preparation_age_seconds":100,"release_age_seconds":180.001,"released_at":"2026-10-21T00:03:00Z","driver":{"pid":1,"starttime_ticks":10},"nodes":[{"pid":i,"starttime_ticks":i*10} for i in range(2,5)]}
r={"schema":"wukongim/conversation-cpu-placement/v1","complete":True,"product_sha":m.PRODUCT,"harness_sha":"h","harness_dirty":False,"binary_sha256":m.EXPECTED_BINARY,"profile_sha256":"s","driver_gomaxprocs":1,"node_gomaxprocs":2,"config":p["stress_gate"],"fixed_environment":f,"cpu_placement":placement,"windows":[]}
sampleproc=lambda i:{"valid":True,"expected":{"pid":i,"starttime_ticks":i*10,"affinity":"0" if i==1 else "0,2-3"},"stat":{"pid":i,"starttime_ticks":i*10},"affinity":"0" if i==1 else "0,2-3","enumerated_threads":1,"valid_threads":1,"threads":[{"tid":i,"starttime_ticks":i*10,"valid":True,"affinity":"0" if i==1 else "0,2-3"}]}
points=[{"slot":slot,"start_offset_ns":slot*1000000000,"deadline_lag_ns":0,"read_duration_ns":1000000,"valid":True,"processes":[sampleproc(i) for i in range(1,5)]} for slot in range(60)]
for _ in range(3):
 r["windows"].append({"start":"2026-10-21T00:03:01Z","runtime_loads":0,"membership_writes":0,"active_runtimes_before":0,"active_runtimes_after":0,"allocated_bytes":20,"cpu_samples":{"schema":"wukongim/cpu-window-samples/v1","valid":True,"duration_ns":60000000000,"start":"2026-10-21T00:03:01Z","stop_reason":"caller_stop","missed_slots":0,"finished_offset_ns":60001000000,"points":copy.deepcopy(points)},"phases":[{"case":{"endpoint":ep,"offered_qps":q,"page_size":100},"nodes":3,"driver_workers":8,"queue_capacity":q//2,"duration_seconds":60,"scheduled":q*60,"completed":q*60,"completed_in_window":q*60,"actual_qps":q,"p99_ms":10,"errors":0,"dropped":0,"unexpected_errors":0,"verdict":"pass"} for ep,q in [("/conversation/list",200),("/conversation/sync",60)]]})
check=lambda v:m.validate_report(v,"overlap","h","s",p,[0,1,2,3])
assert check(r)
early=copy.deepcopy(r);early["windows"][0]["cpu_samples"].update(finished_offset_ns=59995000000);assert check(early)
for reason in ("QPS below fixed floor","P99 exceeds budget","incomplete or failed requests"):
 v=copy.deepcopy(r);v["windows"][0]["phases"][0]["verdict"]=reason;assert check(v) is False;assert len(v["windows"])==3
v=copy.deepcopy(r);v["windows"][0]["phases"][0]["p99_ms"]=501;assert check(v) is False
for change in (lambda v:v.update(product_sha="other"),lambda v:v.update(binary_sha256="other"),lambda v:v.update(harness_dirty=True),lambda v:v.update(driver_gomaxprocs=4),lambda v:v["fixed_environment"].update(schema="wukongim/conversation-fixed-host/v1"),lambda v:v["fixed_environment"].update(release_age_seconds=179),lambda v:v["cpu_placement"].update(valid=False),lambda v:v["cpu_placement"].update(duration_ns=500000001),lambda v:v["cpu_placement"].update(started_at="2026-10-21T00:02:00Z"),lambda v:v["cpu_placement"]["initial"][0]["threads"][0].update(affinity=[0]),lambda v:v["cpu_placement"]["applied"][0]["threads"][0].update(affinity=[1]),lambda v:v["cpu_placement"]["applied"][1].update(starttime_ticks=999),lambda v:v["windows"][0].update(runtime_loads=1),lambda v:v["windows"][0]["cpu_samples"].update(valid=False),lambda v:v["windows"][0]["cpu_samples"].update(duration_ns=1),lambda v:v["windows"][0]["cpu_samples"].update(start="2026-10-21T00:03:02Z"),lambda v:v["windows"][0]["cpu_samples"]["points"][0]["processes"][0]["expected"].update(pid=99),lambda v:v["windows"][0]["cpu_samples"]["points"][0].update(valid=False),lambda v:v["windows"][0]["cpu_samples"].update(points=points[:1]),lambda v:v["windows"][0]["cpu_samples"].update(finished_offset_ns=59994999999),lambda v:v["windows"][0]["cpu_samples"]["points"][1].update(slot=2),lambda v:v["windows"][0]["cpu_samples"]["points"][1].update(read_duration_ns=999000000000),lambda v:v["windows"][0]["cpu_samples"]["points"][0]["processes"][0].update(affinity="1"),lambda v:v["windows"][0]["cpu_samples"]["points"][0]["processes"][0]["expected"].update(affinity="1"),lambda v:v["windows"][0]["cpu_samples"]["points"][0]["processes"][0]["threads"][0].update(affinity="1"),lambda v:v["windows"][0]["cpu_samples"]["points"][0]["processes"][0].update(enumerated_threads=2),lambda v:v["windows"].pop()):
 v=copy.deepcopy(r);change(v)
 try:check(v)
 except (RuntimeError,KeyError,ValueError):pass
 else:raise AssertionError(v)
assert m.placement_masks("shared",top)==([0,1,2,3],[0,1,2,3])
assert m.placement_masks("disjoint",top)==([1],[0,2,3])
`
	out, err := exec.Command("python3", "-B", "-c", code, filepath.Join(repoRoot(t), "scripts", "run-conversation-cpu-contention.py")).CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func TestConversationCPUContentionWorkflowRejectsConflictingInputs(t *testing.T) {
	// Execute only the first workflow step: shell validation and a temporary log,
	// with no source builds, background process, network request or measurement.
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Run string } }
	}
	require.NoError(t, yaml.Unmarshal(readWorkflow(t, "conversation-qps-diagnose.yml"), &workflow))
	script := workflow.Jobs["cpu-contention"].Steps[0].Run
	for _, tc := range []struct {
		name, fixed, gate, product, comparison, digest string
		valid                                          bool
	}{
		{"fixed product", "false", "false", "ef798352c40a94e57f768f76b2f4713712f827fe", "", "", true},
		{"conflicting fixed mode", "true", "false", "ef798352c40a94e57f768f76b2f4713712f827fe", "", "", false},
		{"gate requested", "false", "true", "ef798352c40a94e57f768f76b2f4713712f827fe", "", "", false},
		{"other product", "false", "false", strings.Repeat("c", 40), "", "", false},
		{"comparison override", "false", "false", "ef798352c40a94e57f768f76b2f4713712f827fe", strings.Repeat("c", 40), "", false},
		{"digest override", "false", "false", "ef798352c40a94e57f768f76b2f4713712f827fe", "", strings.Repeat("d", 64), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(cmd.Environ(), "RUNNER_TEMP="+t.TempDir(), "CPU_CONTENTION_FIXED_HOST="+tc.fixed, "CPU_CONTENTION_RELEASE_MATRIX="+tc.gate, "PRODUCT_SHA="+tc.product, "COMPARISON_SHA="+tc.comparison, "EXPECTED_BINARY_SHA256="+tc.digest)
			out, err := cmd.CombinedOutput()
			if tc.valid {
				require.NoError(t, err, "%s", out)
			} else {
				require.Error(t, err, "conflicting inputs silently accepted: %s", out)
			}
		})
	}
}
