package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runBudgetReader(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := exec.Command("python3", append([]string{filepath.Join(repoRoot(t), "scripts/chat-lifecycle/authenticate-released-preclock.py")}, args...)...)
	// Pure parser modes must never consult ambient GitHub/cloud credentials.
	cmd.Env = append(os.Environ(), "GH_TOKEN=", "GITHUB_REPOSITORY=", "GITHUB_REPOSITORY_ID=")
	output, err := cmd.CombinedOutput()
	var result map[string]any
	if e := json.Unmarshal(output, &result); e != nil {
		t.Fatalf("expected controlled parser JSON, err=%v output=%s", err, output)
	}
	return result, err
}
func budgetDocumentFixture(t *testing.T, dir string, limit int64) {
	t.Helper()
	budget := map[string]any{"currency": "CNY", "limit_micros": limit, "operational_stop_micros": limit - 150000000, "committed_micros": 0}
	for name, doc := range map[string]any{
		"run-plan.json":        map[string]any{"schema": "wukongim.chat_lifecycle.run_plan/v1", "stage": "rehearsal", "attempt": 1, "operational_stop_micros": limit - 150000000, "lease_plan": map[string]any{"lease_id": "request-1-rehearsal-1", "request_id": "request-1", "budget": budget}},
		"receipt.json":         map[string]any{"schema": "wukongim.cloud_lease.receipt/v1", "receipt": map[string]any{"lease_id": "request-1-rehearsal-1", "request_id": "request-1", "budget": budget}},
		"deployment-plan.json": map[string]any{"schema": "wukongim.cloud_deployment.plan/v2", "lease_id": "request-1-rehearsal-1", "request_id": "request-1", "budget": budget},
	} {
		b, e := json.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestChatLifecycleDownwardBudgetInput(t *testing.T) {
	for _, tc := range []struct {
		value, prior string
		ok           bool
	}{{"1500000000", "", true}, {"1351044800", "", true}, {"150000001", "", true}, {"1500000000", "100", true}, {"1351044800", "100", false}, {"0", "", false}, {"-1", "", false}, {"150000000", "", false}, {"1500000001", "", false}, {"9223372036854775808", "", false}, {"1351044800.0", "", false}, {"1e9", "", false}, {"+1351044800", "", false}, {"01351044800", "", false}, {" 1351044800", "", false}, {"1351044800 ", "", false}, {"", "", false}} {
		t.Run(tc.value+"_"+tc.prior, func(t *testing.T) {
			r, err := runBudgetReader(t, "budget-input", "--limit", tc.value, "--released-preclock-run-id", tc.prior)
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected parser decision: %v %v", r, err)
			}
			if tc.ok {
				if r["operational_stop_micros"].(float64) != r["limit_micros"].(float64)-150000000 {
					t.Fatal("wrong derived stop")
				}
			} else if r["status"] != "rejected" || r["reason"] != "request_budget_invalid" {
				t.Fatalf("wrong rejection: %v", r)
			}
		})
	}
}
func TestChatLifecycleDownwardBudgetDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, file, from, to string
		ok                   bool
	}{
		{name: "lower", ok: true}, {name: "default", ok: true},
		{"root-stop", "run-plan.json", `"operational_stop_micros":1201044800`, `"operational_stop_micros":1201044801`, false},
		{"missing-root", "run-plan.json", `"operational_stop_micros":1201044800,`, ``, false},
		{"currency", "run-plan.json", `"currency":"CNY"`, `"currency":"USD"`, false},
		{"float-limit", "run-plan.json", `"limit_micros":1351044800`, `"limit_micros":1351044800.0`, false},
		{"boolean-prior", "run-plan.json", `"committed_micros":0`, `"committed_micros":false`, false},
		{"raised-pair", "run-plan.json", `1351044800`, `1500000001`, false},
		{"missing-cap", "run-plan.json", `"limit_micros":1351044800,`, ``, false},
		{"receipt-cap", "receipt.json", `1351044800`, `1500000000`, false},
		{"receipt-bool-prior", "receipt.json", `"committed_micros":0`, `"committed_micros":false`, false},
		{"receipt-prior", "receipt.json", `"committed_micros":0`, `"committed_micros":1`, false},
		{"receipt-identity", "receipt.json", `request-1-rehearsal-1`, `request-1-rehearsal-2`, false},
		{"deployment-stop", "deployment-plan.json", `1201044800`, `1201044801`, false},
		{"deployment-schema", "deployment-plan.json", `plan/v2`, `plan/v9`, false},
		{"proof-attempt-lower", "run-plan.json", `"attempt":1`, `"attempt":2`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			limit := int64(1351044800)
			if tc.name == "default" {
				limit = 1500000000
			}
			budgetDocumentFixture(t, dir, limit)
			if tc.file != "" {
				p := filepath.Join(dir, tc.file)
				b, _ := os.ReadFile(p)
				v := strings.Replace(string(b), tc.from, tc.to, 1)
				if v == string(b) {
					t.Fatal("mutation did not match")
				}
				os.WriteFile(p, []byte(v), 0600)
			}
			r, err := runBudgetReader(t, "budget", "--run-plan", filepath.Join(dir, "run-plan.json"), "--receipt", filepath.Join(dir, "receipt.json"), "--deployment-plan", filepath.Join(dir, "deployment-plan.json"))
			if (err == nil) != tc.ok {
				t.Fatalf("decision: %v %v", r, err)
			}
			if !tc.ok && r["reason"] != "request_budget_identity" {
				t.Fatalf("wrong rejection: %v", r)
			}
		})
	}
}
func TestChatLifecycleDownwardBudgetFirstStartAndFormalCaps(t *testing.T) {
	dir := t.TempDir()
	budgetDocumentFixture(t, dir, 1351044800)
	r, err := runBudgetReader(t, "validate", "--directory", dir, "--run-plan", filepath.Join(dir, "run-plan.json"))
	if err != nil || r["status"] != "first_start" {
		t.Fatalf("lower first start: %v %v", r, err)
	}
	original := filepath.Join(dir, "original.json")
	b, _ := os.ReadFile(filepath.Join(dir, "run-plan.json"))
	os.WriteFile(original, b, 0600)
	for _, limit := range []int64{1351044800, 1500000000} {
		budgetDocumentFixture(t, dir, limit)
		r, err := runBudgetReader(t, "budget", "--run-plan", filepath.Join(dir, "run-plan.json"), "--receipt", filepath.Join(dir, "receipt.json"), "--matches-run-plan", original)
		if (err == nil) != (limit == 1351044800) {
			t.Fatalf("formal cap binding: %v %v", r, err)
		}
	}
	budgetDocumentFixture(t, dir, 1351044800)
	os.Mkdir(filepath.Join(dir, "continuation"), 0700)
	r, err = runBudgetReader(t, "validate", "--directory", dir, "--run-plan", filepath.Join(dir, "run-plan.json"))
	if err == nil || r["reason"] != "sidecar_identity" {
		t.Fatalf("lower first start accepted sidecar: %v %v", r, err)
	}
}
func TestChatLifecycleDownwardBudgetRehearsalWiring(t *testing.T) {
	w := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	s := readFile(t, filepath.Join(repoRoot(t), "scripts/chat-lifecycle/stage-orchestrate.sh"))
	for _, part := range []string{"request_budget_limit_micros:", `default: "1500000000"`, "WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS: ${{ inputs.request_budget_limit_micros }}", "authenticate-released-preclock.py budget-input"} {
		if !strings.Contains(w, part) {
			t.Fatalf("missing workflow input contract %q", part)
		}
	}
	p := strings.Index(s, "authenticate-released-preclock.py budget-input")
	if p < 0 || p > strings.Index(s, "cloud-deployment-bundle.yml") || p > strings.Index(s, "ssh-keygen -q") {
		t.Fatal("budget guard must precede bundle/key/Quote")
	}
	for _, part := range []string{`--request-budget-limit-micros "$request_budget_limit_micros"`, `--argjson operational_stop "$request_budget_stop_micros"`, `WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS:?`} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing stage budget binding %q", part)
		}
	}
}
