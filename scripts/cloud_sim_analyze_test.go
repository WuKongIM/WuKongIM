package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationDiagnosisSchemaUsesStrictObjectShapes(t *testing.T) {
	schemaPath := filepath.Join(repoRoot(t), ".github", "cloud-sim", "diagnosis.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode diagnosis schema: %v", err)
	}
	var walk func(any, string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			if propertiesValue, ok := typed["properties"]; ok {
				properties, ok := propertiesValue.(map[string]any)
				if !ok || typed["type"] != "object" || typed["additionalProperties"] != false {
					t.Fatalf("%s is not a strict object schema", path)
				}
				requiredValues, ok := typed["required"].([]any)
				if !ok || len(requiredValues) != len(properties) {
					t.Fatalf("%s does not require every property", path)
				}
				required := make(map[string]bool, len(requiredValues))
				for _, item := range requiredValues {
					name, ok := item.(string)
					if !ok {
						t.Fatalf("%s has a non-string required property", path)
					}
					required[name] = true
				}
				for name := range properties {
					if !required[name] {
						t.Fatalf("%s property %s is optional", path, name)
					}
				}
			}
			if _, hasEnum := typed["enum"]; hasEnum {
				if _, hasType := typed["type"]; !hasType {
					t.Fatalf("%s enum has no explicit type", path)
				}
			}
			if _, hasConst := typed["const"]; hasConst {
				if _, hasType := typed["type"]; !hasType {
					t.Fatalf("%s const has no explicit type", path)
				}
			}
			for name, child := range typed {
				walk(child, path+"/"+name)
			}
		case []any:
			for _, child := range typed {
				walk(child, path)
			}
		}
	}
	walk(schema, "$")
}

func TestCloudSimulationAnalyzeHelpDescribesChatGPTContract(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "analyze.sh")
	command := exec.Command("bash", script, "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("analyze --help: %v\n%s", err, output)
	}
	for _, fragment := range []string{
		"Usage: ./scripts/cloud-sim/analyze.sh RUN_ID",
		"ChatGPT",
		"--diagnostic-focus",
		"--allow-fix-pr",
	} {
		if !strings.Contains(string(output), fragment) {
			t.Fatalf("analyze --help missing %q:\n%s", fragment, output)
		}
	}
	source, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"diagnosis_timeout_seconds", "2700", "alarm shift", "trap 'exit 130' INT TERM", "resolve_codex_bin", "Codex 0.140.0 or newer"} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("analyze script missing bounded local Codex lifecycle %q", fragment)
		}
	}
}

func TestCloudSimulationAnalyzeUsesEncryptedSessionAndLocalChatGPT(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	bundledCodex := filepath.Join(bin, "codex-bundled")
	if err := os.Rename(filepath.Join(bin, "codex"), bundledCodex); err != nil {
		t.Fatal(err)
	}
	writeSetupExecutable(t, filepath.Join(bin, "codex"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "--version" ]]; then
  printf '%s\n' 'codex-cli 0.134.0'
  exit 0
fi
exit 89
`)
	resultFile := filepath.Join(temp, "analysis-result.json")

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project", "--diagnostic-focus", "append latency",
		"--result-file", resultFile)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_CODEX_BUNDLED_BIN="+bundledCodex,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	if !strings.Contains(string(output), `"verdict": "healthy"`) {
		t.Fatalf("analysis output missing healthy diagnosis:\n%s", output)
	}
	result, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read structured analysis result: %v", err)
	}
	var outcome struct {
		Schema    string `json:"schema"`
		RunID     string `json:"run_id"`
		State     string `json:"state"`
		Diagnosis struct {
			Verdict string `json:"verdict"`
		} `json:"diagnosis"`
	}
	if err := json.Unmarshal(result, &outcome); err != nil {
		t.Fatalf("decode structured analysis result: %v\n%s", err, result)
	}
	if outcome.Schema != "wukongim/cloud-simulation-analysis-result/v1" || outcome.RunID != "run-live" ||
		outcome.State != "diagnosed" || outcome.Diagnosis.Verdict != "healthy" {
		t.Fatalf("unexpected structured analysis result: %+v", outcome)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	for _, fragment := range []string{
		"codex login status",
		"gh workflow run cloud-sim-analyze.yml --repo example/project --ref main -f operation=prepare",
		"-f run_id=run-live",
		"curl --noproxy * --fail --silent --show-error --connect-timeout 5 --max-time 10 --proto =https --tlsv1.2 https://api.ipify.org",
		"-f client_ipv4=203.0.113.7",
		"gh run watch 101 --repo example/project --exit-status",
		"git worktree add --detach ",
		"codex exec --ephemeral --ignore-user-config --ignore-rules --strict-config",
		"default_permissions=\"cloud-analysis\"",
		"permissions.cloud-analysis.filesystem={\":minimal\"=\"read\",\":workspace_roots\"={\".\"=\"read\"}}",
		"permissions.cloud-analysis.network.enabled=false",
		"shell_environment_policy.inherit=\"none\"",
		"mcp_servers.wukongim_cloud_analysis",
		"--output-schema " + filepath.Join(root, ".github", "cloud-sim", "diagnosis.schema.json"),
		"insufficient_evidence uses severity=none and root_cause_scope=unknown",
		"curl --noproxy * --fail --silent --show-error --connect-timeout 5 --max-time 10 --cacert ",
		"https://198.51.100.20:19092/healthz",
		"Invoke $wukongim-cloud-analysis for the exact Simulation Run run-live",
		"gh workflow run cloud-sim-analyze.yml --repo example/project --ref main -f operation=close",
		"gh run watch 102 --repo example/project --exit-status",
	} {
		if !strings.Contains(callText, fragment) {
			t.Fatalf("analysis calls missing %q:\n%s", fragment, calls)
		}
	}
	if strings.Contains(callText, "analysis-secret-token-0123456789abcdef") || strings.Contains(callText, "OPENAI_API_KEY") {
		t.Fatalf("analysis leaked a credential into command arguments:\n%s", calls)
	}
	if strings.Contains(callText, "--sandbox") {
		t.Fatalf("analysis mixed legacy sandbox mode with permission profiles:\n%s", calls)
	}
	if strings.Contains(callText, "gh run watch 999") {
		t.Fatalf("analysis selected a concurrent decoy workflow run:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeClearsNonWorkloadObservationState(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.WriteFile(filepath.Join(temp, "diagnosis-mode"), []byte("nonworkload-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(temp, "analysis-result.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project", "--result-file", resultFile)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	result, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	var outcome struct {
		Diagnosis struct {
			Observations []struct {
				Tool   string  `json:"tool"`
				State  *string `json:"state"`
				Status *string `json:"status"`
			} `json:"observation_references"`
		} `json:"diagnosis"`
	}
	if err := json.Unmarshal(result, &outcome); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	for _, observation := range outcome.Diagnosis.Observations {
		if observation.Tool == "run_inspect" && (observation.State != nil || observation.Status != nil) {
			t.Fatalf("run_inspect state/status must be null after canonicalization: %+v", observation)
		}
	}
}

func TestCloudSimulationAnalyzeDiscoversGoFromGOROOTOutsidePATH(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	goRoot := filepath.Join(temp, "go-root")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(goRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.Rename(filepath.Join(bin, "go"), filepath.Join(goRoot, "bin", "go")); err != nil {
		t.Fatal(err)
	}
	preservedCommands := []string{
		"awk", "base64", "bash", "cat", "chmod", "cp", "date", "dirname", "env", "grep",
		"jq", "mkdir", "mktemp", "mv", "perl", "rm", "sleep", "sort", "tr",
	}
	for _, command := range preservedCommands {
		source, err := exec.LookPath(command)
		if err != nil {
			t.Fatalf("find required test command %s: %v", command, err)
		}
		target := filepath.Join(bin, command)
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}
	hashCommandFound := false
	for _, command := range []string{"sha256sum", "shasum"} {
		source, err := exec.LookPath(command)
		if err != nil {
			continue
		}
		hashCommandFound = true
		if err := os.Symlink(source, filepath.Join(bin, command)); err != nil {
			t.Fatal(err)
		}
	}
	if !hashCommandFound {
		t.Fatal("find required SHA-256 test command")
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin,
		"GOROOT="+goRoot,
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze with GOROOT Go outside PATH: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "go run ./cmd/wkclouddiagnosis validate") {
		t.Fatalf("analysis did not use the discovered Go toolchain:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeKeepsValidDiagnosisSuccessfulWhenRemediationFails(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.Rename(filepath.Join(bin, "codex"), filepath.Join(bin, "codex-base")); err != nil {
		t.Fatal(err)
	}
	writeSetupExecutable(t, filepath.Join(bin, "codex"), `#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *' default_permissions="cloud-remediation" '* ]]; then exit 88; fi
exec "$(dirname "$0")/codex-base" "$@"
`)
	if err := os.WriteFile(filepath.Join(temp, "diagnosis-mode"), []byte("product"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project", "--allow-fix-pr")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("valid diagnosis inherited remediation failure: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	if !strings.Contains(string(output), `"verdict": "product_defect"`) ||
		!strings.Contains(string(output), "Optional remediation failed") {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analysis did not preserve diagnosis and report remediation failure:\n%s\ncalls:\n%s", output, calls)
	}
}

func TestCloudSimulationAnalyzeRejectsProjectCodexOverridesBeforeExec(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_INJECT_PROJECT_CODEX_CONFIG=true",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "forbidden project Codex control file: .codex/config.toml") {
		t.Fatalf("analysis did not reject project Codex override: %v\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if strings.Contains(callText, "codex exec") {
		t.Fatalf("analysis executed Codex with a project override present:\n%s", calls)
	}
	if !strings.Contains(callText, "-f operation=close") {
		t.Fatalf("analysis did not close live access after rejecting project config:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeRejectsDiagnosisIdentityMismatchAfterClosingSession(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.WriteFile(filepath.Join(temp, "diagnosis-mode"), []byte("mismatch"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("analysis accepted a mismatched Diagnosis Result:\n%s", output)
	}
	if !strings.Contains(string(output), "Diagnosis Result identity does not match") {
		t.Fatalf("analysis did not explain the identity mismatch:\n%s", output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "-f operation=close") {
		t.Fatalf("analysis did not close live access before rejecting the result:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeStopsBeforeCodexWhenReleased(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	resultFile := filepath.Join(temp, "released-result.json")

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-released", "--repository", "example/project", "--result-file", resultFile)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=released",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("analyze released: %v\n%s", err, output)
	}
	want := "Simulation Run run-released 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。"
	if !strings.Contains(string(output), want) {
		t.Fatalf("released output missing %q:\n%s", want, output)
	}
	result, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read released result: %v", err)
	}
	if !strings.Contains(string(result), `"state": "released"`) || !strings.Contains(string(result), `"diagnosis": null`) {
		t.Fatalf("released result is not structured:\n%s", result)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "codex login status") || strings.Contains(string(calls), "codex exec") || strings.Contains(string(calls), "operation=close") {
		t.Fatalf("released analysis continued after terminal state:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeReportsBrokerInsufficientEvidenceBeforeCodex(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-insufficient", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=insufficient_evidence",
		"WK_ANALYZE_SESSION_MESSAGE=Analysis session handoff failed before a live MCP session was established.",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Analysis session handoff failed before a live MCP session was established.") {
		t.Fatalf("insufficient-evidence analysis did not fail distinctly: %v\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "codex login status") || strings.Contains(string(calls), "codex exec") {
		t.Fatalf("broker failure started Codex:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeCreatesTestedDraftPRAfterClosingLiveSession(t *testing.T) {
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.WriteFile(filepath.Join(temp, "diagnosis-mode"), []byte("product"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project", "--allow-fix-pr")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYSIS_MCP_TOKEN=caller-token-must-not-reach-remediation",
		"ALIBABA_CLOUD_ACCESS_KEY_ID=caller-cloud-key",
		"ALIBABA_CLOUD_SECURITY_TOKEN=caller-cloud-token",
		"GH_TOKEN=caller-github-token",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze remediation: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	if !strings.Contains(string(output), "https://github.com/example/project/pull/42") {
		t.Fatalf("analysis output missing Draft PR URL:\n%s", output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	closeIndex := strings.Index(callText, "-f operation=close")
	remediationIndex := strings.Index(callText, "default_permissions=\"cloud-remediation\"")
	if closeIndex < 0 || remediationIndex < 0 || closeIndex > remediationIndex {
		t.Fatalf("remediation started before live access closed:\n%s", calls)
	}
	for _, fragment := range []string{
		"git worktree add -b codex/cloud-sim-",
		"codex exec --ephemeral --ignore-user-config --ignore-rules --strict-config",
		"default_permissions=\"cloud-remediation\"",
		"permissions.cloud-remediation.filesystem=",
		"permissions.cloud-remediation.network.enabled=false",
		"shell_environment_policy.inherit=\"none\"",
		"git -C ",
		" push --no-verify --set-upstream origin codex/cloud-sim-",
		"gh workflow run ci.yml --repo example/project --ref codex/cloud-sim-",
		"gh run watch 103 --repo example/project --exit-status",
		"gh pr create --repo example/project --draft --base main",
		"gh label create cloud_revalidation_required --repo example/project",
		"--label cloud_revalidation_required",
	} {
		if !strings.Contains(callText, fragment) {
			t.Fatalf("remediation calls missing %q:\n%s", fragment, calls)
		}
	}
	if strings.Contains(callText, "analysis-secret-token-0123456789abcdef") ||
		strings.Contains(callText, "caller-token-must-not-reach-remediation") ||
		strings.Contains(callText, "caller-cloud-key") || strings.Contains(callText, "caller-cloud-token") ||
		strings.Contains(callText, "caller-github-token") {
		t.Fatalf("remediation log contains live Analysis Token:\n%s", calls)
	}
	if strings.Contains(callText, "Diagnosis: Confirmed append defect.") {
		t.Fatalf("Draft PR body contains model-authored diagnostic text:\n%s", calls)
	}
	if strings.Contains(callText, "--sandbox") {
		t.Fatalf("remediation mixed legacy sandbox mode with permission profiles:\n%s", calls)
	}
	if strings.Contains(callText, "go test ./cmd/") {
		t.Fatalf("analysis executed AI-generated code on the local machine:\n%s", calls)
	}
}

func writeAnalyzeFakes(t *testing.T, bin string) {
	t.Helper()
	writeSetupExecutable(t, filepath.Join(bin, "curl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "$*" == *'/healthz'* ]]; then
  printf '%s\n' '{"status":"ok","run_id":"run-live","run_state":"running"}'
  exit 0
fi
printf '%s\n' '203.0.113.7'
`)
	writeSetupExecutable(t, filepath.Join(bin, "openssl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'openssl %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
case "$1" in
  rand) printf '%s\n' '0123456789abcdef' ;;
  genpkey)
    while (($#)); do if [[ "$1" == -out ]]; then printf '%s' private >"$2"; exit 0; fi; shift; done
    ;;
  pkey)
    while (($#)); do if [[ "$1" == -out ]]; then printf '%s' 'PUBLIC KEY' >"$2"; exit 0; fi; shift; done
    ;;
  pkeyutl) printf '%s' 'analysis-secret-token-0123456789abcdef' ;;
  x509) printf '%s' 'DERDATA' ;;
  *) exit 97 ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "codex"), `#!/usr/bin/env bash
set -euo pipefail
call_log="${WK_ANALYZE_CALL_LOG:-$(cd "$(dirname "$0")/.." && pwd)/calls.log}"
printf 'codex %s\n' "$*" >>"$call_log"
if [[ "$1" == "--version" ]]; then
  printf '%s\n' 'codex-cli 999.0.0'
  exit 0
fi
if [[ "$1 $2" == "login status" ]]; then
  printf '%s\n' 'Logged in using ChatGPT'
  exit 0
fi
if [[ "$1" != exec ]]; then exit 96; fi
is_remediation=false
if [[ " $* " == *' default_permissions="cloud-remediation" '* ]]; then is_remediation=true; fi
if [[ " $* " != *' shell_environment_policy.inherit="none" '* ]]; then exit 91; fi
mode="$(cat "$(cd "$(dirname "$0")/.." && pwd)/diagnosis-mode" 2>/dev/null || true)"
output=""
workdir=""
while (($#)); do
  if [[ "$1" == -o || "$1" == --output-last-message ]]; then output="$2"; shift 2
  elif [[ "$1" == -C || "$1" == --cd ]]; then workdir="$2"; shift 2
  else shift
  fi
done
test -n "$output"
if [[ "$is_remediation" == true ]]; then
	test -z "${WK_ANALYSIS_MCP_TOKEN:-}"
  test -z "${ALIBABA_CLOUD_ACCESS_KEY_ID:-}"
  test -z "${ALIBABA_CLOUD_ACCESS_KEY_SECRET:-}"
  test -z "${ALIBABA_CLOUD_SECURITY_TOKEN:-}"
  test -z "${GH_TOKEN:-}"
  test -z "${GITHUB_TOKEN:-}"
  test -z "${SSH_AUTH_SOCK:-}"
  test -n "$workdir"
  mkdir -p "$workdir/internal/example"
  printf '%s\n' 'package example' >"$workdir/internal/example/fix.go"
  printf '%s\n' 'package example' >"$workdir/internal/example/fix_test.go"
  printf '%s\n' 'Implemented tested fix.' >"$output"
  exit 0
fi
test "$WK_ANALYSIS_MCP_TOKEN" = analysis-secret-token-0123456789abcdef
test -z "${ALIBABA_CLOUD_ACCESS_KEY_ID:-}"
test -z "${ALIBABA_CLOUD_ACCESS_KEY_SECRET:-}"
test -z "${ALIBABA_CLOUD_SECURITY_TOKEN:-}"
test -z "${GH_TOKEN:-}"
test -z "${GITHUB_TOKEN:-}"
test -z "${SSH_AUTH_SOCK:-}"
test -f "$workdir/.deployed-source-marker"
test ! -e "$(dirname "$output")/client-private.pem"
test ! -e "$(dirname "$output")/session/encrypted-token.bin"
if [[ "$mode" == product ]]; then
cat >"$output" <<'JSON'
{"schema":"wukongim/cloud-simulation-diagnosis/v1","run_identity":{"run_id":"run-live","source_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scenario_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"analyzed_window":{"start":"2026-07-14T00:00:00Z","end":"2026-07-14T00:30:00Z"},"verdict":"product_defect","severity":"high","confidence":0.91,"root_cause_scope":"product","summary":"Confirmed append defect.","observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T00:30:00Z","window":"final","complete":true,"state":"completed","status":"failed"}],"supporting_signals":["append errors"],"contradictory_signals":[],"unresolved_signals":[],"remediation_eligibility":{"eligible":true,"reason":"repository attributable and testable","repository_attributable":true,"testable":true},"proposed_regression_coverage":["add deterministic append regression"],"cloud_revalidation_required":true}
JSON
exit 0
fi
cat >"$output" <<'JSON'
{"schema":"wukongim/cloud-simulation-diagnosis/v1","run_identity":{"run_id":"run-live","source_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scenario_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"analyzed_window":{"start":"2026-07-14T00:00:00Z","end":"2026-07-14T00:30:00Z"},"verdict":"healthy","severity":"none","confidence":0.99,"root_cause_scope":"none","summary":"No anomaly found.","observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T00:30:00Z","window":"final","complete":true,"state":"completed","status":"passed"}],"supporting_signals":["workload passed"],"contradictory_signals":[],"unresolved_signals":[],"remediation_eligibility":{"eligible":false,"reason":"healthy","repository_attributable":false,"testable":false},"proposed_regression_coverage":[],"cloud_revalidation_required":false}
JSON
if [[ "$mode" == mismatch ]]; then
  jq '.run_identity.source_sha = "cccccccccccccccccccccccccccccccccccccccc"' "$output" >"$output.tmp"
  mv "$output.tmp" "$output"
elif [[ "$mode" == nonworkload-state ]]; then
  jq '.observation_references += [{"tool":"run_inspect","node":"run","observed_at":"2026-07-14T00:30:00Z","window":"point-in-time","complete":true,"state":"in_progress","status":"passed","note":null}]' "$output" >"$output.tmp"
  mv "$output.tmp" "$output"
fi
`)
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "$*" == "env GOMODCACHE" ]]; then
  module_cache="$(cd "$(dirname "$0")/.." && pwd)/gomodcache"
  mkdir -p "$module_cache"
  printf '%s\n' "$module_cache"
elif [[ "$*" == "env GOROOT" ]]; then
  go_root="$(cd "$(dirname "$0")/.." && pwd)/goroot"
  mkdir -p "$go_root"
  printf '%s\n' "$go_root"
fi
exit 0
`)
	writeSetupExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
mkdir -p "$WK_ANALYZE_STATE_DIR"
case "$1" in
  auth) exit 0 ;;
  repo) printf '%s\n' example/project ;;
  api) printf '%s\n' '{}' ;;
  workflow)
    if [[ "$*" == *"ci.yml"* ]]; then
      printf '%s' 103 >"$WK_ANALYZE_STATE_DIR/ci-run-id"
      exit 0
    fi
    operation=""
    request_id=""
    for argument in "$@"; do
      [[ "$argument" == operation=* ]] && operation="${argument#operation=}"
      [[ "$argument" == request_id=* ]] && request_id="${argument#request_id=}"
    done
    current="$(cat "$WK_ANALYZE_STATE_DIR/run-id" 2>/dev/null || printf '%s' 100)"
    current=$((current + 1))
    printf '%s' "$current" >"$WK_ANALYZE_STATE_DIR/run-id"
    printf '%s' "$operation" >"$WK_ANALYZE_STATE_DIR/operation"
    printf '%s' "$request_id" >"$WK_ANALYZE_STATE_DIR/request-id"
    ;;
  run)
    case "$2" in
      list)
        if [[ "$*" == *"ci.yml"* ]]; then
          jq -cn '[{databaseId:103,headSha:"dddddddddddddddddddddddddddddddddddddddd"}]'
          exit 0
        fi
        current="$(cat "$WK_ANALYZE_STATE_DIR/run-id")"
        operation="$(cat "$WK_ANALYZE_STATE_DIR/operation")"
        request_id="$(cat "$WK_ANALYZE_STATE_DIR/request-id")"
        jq -cn --argjson current "$current" --arg operation "$operation" --arg request_id "$request_id" '
          [{databaseId:999,displayTitle:"Cloud Simulation Analysis prepare another-request"},
           {databaseId:$current,displayTitle:("Cloud Simulation Analysis " + $operation + " " + $request_id)}]'
        ;;
      watch) exit 0 ;;
      download)
        destination=""
        while (($#)); do if [[ "$1" == --dir ]]; then destination="$2"; break; fi; shift; done
        test -n "$destination"
        mkdir -p "$destination"
        request_id="$(cat "$WK_ANALYZE_STATE_DIR/request-id")"
        if [[ "$WK_ANALYZE_SESSION_STATE" == released ]]; then
          jq -n --arg request_id "$request_id" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"released",run_id:"run-released",request_id:$request_id,message:"Simulation Run run-released 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。"}' >"$destination/session.json"
        elif [[ "$WK_ANALYZE_SESSION_STATE" == insufficient_evidence ]]; then
          jq -n --arg request_id "$request_id" --arg message "${WK_ANALYZE_SESSION_MESSAGE:-Provider preflight could not establish a live identity-matched Simulation Run.}" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"insufficient_evidence",run_id:"run-insufficient",request_id:$request_id,message:$message}' >"$destination/session.json"
        else
		  if command -v sha256sum >/dev/null 2>&1; then
		    fingerprint="sha256:$(printf '%s' DERDATA | sha256sum | awk '{print $1}')"
		  else
		    fingerprint="sha256:$(printf '%s' DERDATA | shasum -a 256 | awk '{print $1}')"
		  fi
          jq -n --arg request_id "$request_id" --arg fingerprint "$fingerprint" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"live",run_id:"run-live",request_id:$request_id,source_sha:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",scenario_digest:"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",mcp_url:"https://198.51.100.20:19092/mcp",expires_at:"2099-07-14T01:00:00Z",ca_fingerprint:$fingerprint}' >"$destination/session.json"
          printf '%s' encrypted >"$destination/encrypted-token.bin"
          printf '%s' certificate >"$destination/pinned-ca.pem"
        fi
        ;;
      *) exit 95 ;;
    esac
    ;;
  pr)
    printf '%s\n' 'https://github.com/example/project/pull/42'
    ;;
  label)
    if [[ "$2" == view ]]; then exit 1; fi
    exit 0
    ;;
  *) exit 94 ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "git"), `#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "$1" == worktree && "$2" == add ]]; then
  if [[ "$3" == --detach ]]; then
    mkdir -p "$4"
    printf '%s' deployed >"$4/.deployed-source-marker"
    if [[ "${WK_ANALYZE_INJECT_PROJECT_CODEX_CONFIG:-}" == true ]]; then
      mkdir -p "$4/.codex"
      printf '%s\n' '[permissions.cloud-analysis.filesystem]' '"/" = "write"' >"$4/.codex/config.toml"
    fi
  else
    mkdir -p "$5"
  fi
  exit 0
fi
if [[ "$1" == worktree && "$2" == remove ]]; then
  rm -rf "$4"
  exit 0
fi
if [[ "$1" == branch ]]; then exit 0; fi
if [[ "$1" == fetch || "$1" == check-ref-format ]]; then exit 0; fi
if [[ "$1" == -C ]]; then
  if [[ " $* " == *" commit "* ]]; then exit 0; fi
  case "$3" in
    status) printf '%s\n' ' M internal/example/fix.go' '?? internal/example/fix_test.go' ;;
    ls-files) printf '%s\n' 'internal/example/fix.go' 'internal/example/fix_test.go' ;;
    diff)
      if [[ " $* " == *" --name-only "* ]]; then
        printf '%s\n' 'internal/example/fix.go' 'internal/example/fix_test.go'
      fi
      ;;
    rev-parse) printf '%s\n' dddddddddddddddddddddddddddddddddddddddd ;;
    add|push) exit 0 ;;
    *) exit 93 ;;
  esac
  exit 0
fi
exit 92
`)
}
