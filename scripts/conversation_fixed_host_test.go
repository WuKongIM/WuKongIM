package scripts_test

// Failure inventory (before implementation): mixed jobs or mutable candidate;
// changed baseline/order/thresholds; repeated builds or driver compilation;
// hidden numeric rejection; partial/dirty receipt accepted; failed candidate
// entering the gate; child processes leaking between blocks; missing artifacts.
import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestConversationFixedHostPlanIsBoundedAndCannotOverrideWorkload(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "run-conversation-fixed-host.py")
	sha := strings.Repeat("c", 40)
	out, err := exec.Command("python3", script, "--plan", "--candidate-sha", sha).CombinedOutput()
	require.NoError(t, err, "%s", out)
	var plan struct {
		Products map[string]string `json:"products"`
		Sequence []struct {
			ID      string `json:"id"`
			Product string `json:"product"`
		} `json:"sequence"`
		DiagnosticSeconds int `json:"diagnostic_timeout_seconds"`
		GateSeconds       int `json:"gate_timeout_seconds"`
		JobMinutes        int `json:"job_timeout_minutes"`
		PreparationAge    int `json:"preparation_age_seconds"`
	}
	require.NoError(t, json.Unmarshal(out, &plan))
	require.Equal(t, "4d44b39dda6fdaa7dafb637ca6cfb718e9530f77", plan.Products["A"])
	require.Equal(t, "b469c6e5e2b4ff65611a8fe3389fb5034de3f152", plan.Products["B"])
	require.Equal(t, sha, plan.Products["C"])
	var products, ids []string
	for _, block := range plan.Sequence {
		products = append(products, block.Product)
		ids = append(ids, block.ID)
	}
	require.Equal(t, []string{"B", "B", "A", "B", "B", "A", "C"}, products)
	require.Equal(t, []string{"01-B", "02-B", "03-A", "04-B", "05-B", "06-A", "07-C"}, ids)
	require.Equal(t, 720, plan.DiagnosticSeconds)
	require.Equal(t, 1080, plan.GateSeconds)
	require.Equal(t, 120, plan.JobMinutes)
	require.Equal(t, 180, plan.PreparationAge)
	for _, args := range [][]string{{"--help"}, {"--plan", "--candidate-sha", "main"}, {"--plan", "--candidate-sha", sha, "--qps", "1"}, {"--plan", "--candidate-sha", sha, "--comparison-sha", sha}} {
		result, e := exec.Command("python3", append([]string{script}, args...)...).CombinedOutput()
		if args[0] == "--help" {
			require.NoError(t, e, "%s", result)
		} else {
			require.Error(t, e, "%s", result)
		}
	}
}

func TestConversationFixedHostWorkflowAndRunnerContracts(t *testing.T) {
	raw := readWorkflow(t, "conversation-qps-diagnose.yml")
	var workflow struct {
		On map[string]struct {
			Inputs map[string]struct {
				Type    string
				Default bool
			}
		} `yaml:"on"`
		Jobs map[string]struct {
			If          string
			RunsOn      string `yaml:"runs-on"`
			Timeout     int    `yaml:"timeout-minutes"`
			Environment any
			Steps       []struct {
				Run  string
				With map[string]any
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(raw, &workflow))
	input := workflow.On["workflow_dispatch"].Inputs["fixed_host_validation"]
	require.Equal(t, "boolean", input.Type)
	require.False(t, input.Default)
	require.Equal(t, "${{ !inputs.fixed_host_validation && !inputs.cpu_contention_validation }}", workflow.Jobs["diagnose"].If)
	fixed := workflow.Jobs["fixed-host"]
	require.Equal(t, "${{ inputs.fixed_host_validation && !inputs.cpu_contention_validation }}", fixed.If)
	require.Equal(t, "ubuntu-24.04", fixed.RunsOn)
	require.Equal(t, 120, fixed.Timeout)
	require.Nil(t, fixed.Environment)
	preflight := strings.Index(string(raw), "TestConversationFixedHostTimeoutCleansChildProcessGroups$")
	sequence := strings.Index(string(raw), "python3 scripts/run-conversation-fixed-host.py")
	require.Greater(t, preflight, 0, "Linux cleanup preflight must run before product measurement")
	require.Less(t, preflight, sequence)
	require.Contains(t, string(raw), "conversation-fixed-host-preflight.jsonl")
	require.Contains(t, string(raw), `.Action == "pass"`)
	require.Contains(t, string(raw), `.Action == "skip"`)
	require.Contains(t, string(raw), "run-conversation-fixed-host.py")
	require.Contains(t, string(raw), "ref: ${{ github.sha }}")
	require.Contains(t, string(raw), "FIXED_HOST_WORKFLOW_SHA: ${{ github.sha }}")
	require.Contains(t, string(raw), "conversation-fixed-host-${{ github.run_id }}-${{ github.run_attempt }}")
	source := readFile(t, filepath.Join(repoRoot(t), "scripts", "run-conversation-fixed-host.py"))
	for _, required := range []string{
		`"go", "test", "-c", "-tags=e2e"`, `"go", "build", "-tags=e2e"`,
		`"-test.count=1"`, `"-test.timeout=12m"`, `"-test.timeout=18m"`,
		`"WK_E2E_CONVERSATION_FIXED_HOST": "1"`, `"GOMAXPROCS": "4"`,
		`"TestConversationQPSMixedAttribution"`, `"TestConversationQPSReleaseGate"`,
		`"merge-base", "--is-ancestor", "origin/main", candidate`,
		`FIXED_HOST_WORKFLOW_SHA`, `os.sched_setaffinity`, `os.sched_getaffinity`,
		`start_new_session=True`, `cleanup_session`, `"events.jsonl"`, `"summary.json"`,
		`validate-conversation-qps-report.jq`, `"numeric_pass"`, `"candidate_windows_rejected"`,
	} {
		require.Contains(t, source, required)
	}
	require.NotContains(t, source, "shell=True")
}

func TestConversationFixedHostReceiptKeepsRejectedWindowsAndRejectsBrokenControls(t *testing.T) {
	// Pure receipt/parser contracts: no binaries, listeners, sleeps or background work.
	code := `import copy,importlib.util,pathlib,sys
spec=importlib.util.spec_from_file_location("fixed",sys.argv[1]); mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
p={"min_completion_ratio":0.95,"max_p99_ms":500,"stress_gate":{"mixed_seconds":60,"mixed_workers_per_endpoint":8,"mixed_list_qps":200,"mixed_sync_qps":60,"max_allocated_bytes_per_request":{"mixed":100}}}
r={"schema":"wukongim/conversation-mixed-attribution/v1","complete":True,"product_sha":"p","harness_sha":"h","harness_dirty":False,"binary_sha256":"b","profile_sha256":"s","driver_gomaxprocs":4,"node_gomaxprocs":2,"config":p["stress_gate"],"fixed_environment":{"schema":"wukongim/conversation-fixed-host/v1","valid":True,"target_age_seconds":180,"preparation_age_seconds":100,"release_age_seconds":180.001},"windows":[]}
for _ in range(3):
 r["windows"].append({"runtime_loads":0,"membership_writes":0,"active_runtimes_before":0,"active_runtimes_after":0,"allocated_bytes":20,"phases":[{"case":{"endpoint":endpoint,"offered_qps":qps,"page_size":100},"nodes":3,"driver_workers":8,"queue_capacity":qps//2,"duration_seconds":60,"scheduled":qps*60,"completed":qps*60,"completed_in_window":qps*60,"actual_qps":qps,"p99_ms":10,"errors":0,"dropped":0,"unexpected_errors":0,"verdict":"pass"} for endpoint,qps in [("/conversation/list",200),("/conversation/sync",60)]]})
def check(value): return mod.validate_diagnostic(value,"p","h","b","s",p)
assert check(r)
for verdict in ("incomplete or failed requests","P99 exceeds budget","completion ratio below target"):
 red=copy.deepcopy(r); red["windows"][0]["phases"][0]["verdict"]=verdict
 assert check(red) is False
 assert len(red["windows"])==3
red=copy.deepcopy(r); red["windows"][0]["allocated_bytes"]=10000000
assert check(red) is False
for field,value in [("p99_ms",501),("actual_qps",0),("dropped",1),("errors",1),("completed",0)]:
 red=copy.deepcopy(r); red["windows"][0]["phases"][0][field]=value
 if field=="completed": red["windows"][0]["phases"][0]["completed_in_window"]=0
 assert check(red) is False, (field,value)
for change in (lambda v:v.update(complete=False), lambda v:v.update(harness_dirty=True), lambda v:v["windows"].pop(), lambda v:v["fixed_environment"].update(valid=False), lambda v:v["fixed_environment"].update(preparation_age_seconds=181), lambda v:v["windows"][0]["phases"][0].update(unexpected_errors=1)):
 broken=copy.deepcopy(r); change(broken)
 try: check(broken)
 except RuntimeError: pass
 else: raise AssertionError(broken)
`
	cmd := exec.Command("python3", "-B", "-c", code, filepath.Join(repoRoot(t), "scripts", "run-conversation-fixed-host.py"))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}
