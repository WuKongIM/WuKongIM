package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	for _, fragment := range []string{
		"diagnosis_timeout_seconds", "2700", "wk_start_bounded",
		"trap 'handle_signal 129' HUP", "trap 'handle_signal 130' INT TERM",
		"resolve_codex_bin", "Codex 0.140.0 or newer",
	} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("analyze script missing bounded local Codex lifecycle %q", fragment)
		}
	}
}

func TestCloudSimulationAnalyzeUsesEncryptedSessionAndLocalChatGPT(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	sourceCodexHome := os.Getenv("CODEX_HOME")
	if sourceCodexHome == "" {
		t.Fatal("scripts test CODEX_HOME is not configured")
	}
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
	command.Env = append(analyzeFakeEnvironment(bin),
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
	if !strings.Contains(callText, "codex-home ") || strings.Contains(callText, "codex-home "+sourceCodexHome) {
		t.Fatalf("analysis did not isolate Codex state from the source CODEX_HOME:\n%s", calls)
	}
	cacheContent, err := os.ReadFile(filepath.Join(sourceCodexHome, "models_cache.json"))
	if err != nil || string(cacheContent) != "{\"client_version\":\"newer-incompatible\"}\n" {
		t.Fatalf("analysis mutated the source Codex model cache: err=%v content=%q", err, cacheContent)
	}
	if got := strings.Count(callText, "-f operation=inspect"); got != 1 {
		t.Fatalf("inspect dispatches = %d, want one exact provider preflight:\n%s", got, calls)
	}
	if got := strings.Count(callText, "-f operation=prepare"); got != 1 {
		t.Fatalf("prepare dispatches = %d, want one when same-host IPv4 matches public echo:\n%s", got, calls)
	}
	for _, fragment := range []string{
		"codex login status",
		"gh workflow run cloud-sim-analyze.yml --repo example/project --ref main -f operation=inspect",
		"gh workflow run cloud-sim-analyze.yml --repo example/project --ref main -f operation=prepare",
		"-f run_id=run-live",
		"curl --noproxy * --fail --silent --show-error --connect-timeout 5 --max-time 10 --proto =https --tlsv1.2 https://api.ipify.org",
		"-f client_ipv4=203.0.113.7",
		"gh run view 101 --repo example/project --json status,conclusion",
		"gh run view 102 --repo example/project --json status,conclusion",
		" worktree add --detach ",
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
		"gh run view 103 --repo example/project --json status,conclusion",
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
	if strings.Contains(callText, "gh run view 999") {
		t.Fatalf("analysis selected a concurrent decoy workflow run:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeBoundsCodexEnvironmentProbes(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name       string
		hangMode   string
		wantOutput string
	}{
		{name: "version", hangMode: "version", wantOutput: "Codex version probe timed out after 1 seconds"},
		{name: "login status", hangMode: "login", wantOutput: "Codex login status timed out after 1 seconds"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
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
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				"WK_ANALYZE_CODEX_HANG="+testCase.hangMode,
				// Keep ordinary fake tool startup tolerant of concurrent package
				// load while preserving the one-second Codex probe under test.
				"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=3",
				"WK_ANALYSIS_CODEX_PROBE_TIMEOUT_SECONDS=1",
			)
			started := time.Now()
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), testCase.wantOutput) {
				t.Fatalf("analyze error = %v, want bounded %s failure:\n%s", err, testCase.hangMode, output)
			}
			// The command itself must still hit the one-second deadline above. Keep
			// enough wall-clock slack for process-group teardown and the exact close
			// workflow when repository packages are running concurrently in CI.
			if elapsed := time.Since(started); elapsed > 8*time.Second {
				t.Fatalf("bounded %s probe took %s, want under 8s", testCase.hangMode, elapsed)
			}
			calls, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(calls), "-f operation=close") ||
				strings.Contains(string(calls), "codex exec") {
				t.Fatalf("bounded Codex probe did not close before exit:\n%s", calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeBoundsDiagnosisAndValidationProcessGroups(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name       string
		hangTarget string
		wantOutput string
	}{
		{name: "diagnosis Codex", hangTarget: "diagnosis", wantOutput: "local Codex diagnosis exceeded the Analysis Token deadline"},
		{name: "diagnosis validator", hangTarget: "validation", wantOutput: "Diagnosis Result validation timed out after 3 seconds"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := repoRoot(t)
			temp := t.TempDir()
			bin := filepath.Join(temp, "bin")
			stateDir := filepath.Join(temp, "state")
			callLog := filepath.Join(temp, "calls.log")
			descendantPIDFile := filepath.Join(temp, "descendant.pid")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeAnalyzeFakes(t, bin)
			if testCase.hangTarget == "diagnosis" {
				installAnalyzeCodexHang(t, bin, "diagnosis", descendantPIDFile)
			}

			command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
				"run-live", "--repository", "example/project")
			command.Dir = root
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				"WK_ANALYZE_HANG_PID_FILE="+descendantPIDFile,
				// Keep ordinary fake tool startup separate from the one-second
				// diagnosis deadline under concurrent repository test load.
				"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=3",
				"WK_ANALYSIS_DIAGNOSIS_TIMEOUT_SECONDS=1",
			)
			if testCase.hangTarget == "validation" {
				command.Env = append(command.Env, "WK_ANALYZE_GO_VALIDATE_HANG=true")
			}
			started := time.Now()
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), testCase.wantOutput) {
				t.Fatalf("analyze error = %v, want bounded %s failure:\n%s", err, testCase.hangTarget, output)
			}
			if elapsed := time.Since(started); elapsed > 12*time.Second {
				t.Fatalf("bounded %s took %s, want under 12s", testCase.hangTarget, elapsed)
			}
			assertAnalyzeDescendantGone(t, descendantPIDFile)
			calls, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(calls), "-f operation=close") {
				t.Fatalf("bounded %s failure leaked the Analysis window:\n%s", testCase.hangTarget, calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeSignalsCloseWindowAndKillDiagnosisGroup(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name     string
		signal   syscall.Signal
		exitCode int
	}{
		{name: "hup", signal: syscall.SIGHUP, exitCode: 129},
		{name: "interrupt", signal: syscall.SIGINT, exitCode: 130},
		{name: "term", signal: syscall.SIGTERM, exitCode: 130},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := repoRoot(t)
			temp := t.TempDir()
			bin := filepath.Join(temp, "bin")
			stateDir := filepath.Join(temp, "state")
			callLog := filepath.Join(temp, "calls.log")
			descendantPIDFile := filepath.Join(temp, "descendant.pid")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeAnalyzeFakes(t, bin)
			installAnalyzeCodexHang(t, bin, "diagnosis", descendantPIDFile)

			command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
				"run-live", "--repository", "example/project")
			command.Dir = root
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				"WK_ANALYZE_HANG_PID_FILE="+descendantPIDFile,
			)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(descendantPIDFile); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("analysis did not start the hanging diagnosis descendant:\n%s", output.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(testCase.signal); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }()
			select {
			case err := <-waited:
				exitError, ok := err.(*exec.ExitError)
				if !ok || exitError.ExitCode() != testCase.exitCode {
					t.Fatalf("analysis %s exit = %v, want %d:\n%s",
						testCase.name, err, testCase.exitCode, output.String())
				}
			case <-time.After(6 * time.Second):
				t.Fatalf("analysis %s did not reach bounded EXIT close", testCase.name)
			}
			assertAnalyzeDescendantGone(t, descendantPIDFile)
			calls, err := os.ReadFile(callLog)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(calls), "-f operation=close"); got != 1 {
				t.Fatalf("analysis %s close dispatches = %d, want exactly one:\n%s",
					testCase.name, got, calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeBoundsCryptoAndWorktreeProcessGroups(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name       string
		configure  func(t *testing.T, bin, pidFile string) []string
		wantOutput string
	}{
		{
			name: "token decryption",
			configure: func(_ *testing.T, _, pidFile string) []string {
				return []string{"WK_ANALYZE_OPENSSL_HANG=pkeyutl", "WK_ANALYZE_HANG_PID_FILE=" + pidFile}
			},
			wantOutput: "Analysis Token decryption failed or timed out",
		},
		{
			name: "analysis worktree add",
			configure: func(_ *testing.T, _, pidFile string) []string {
				return []string{"WK_ANALYZE_GIT_WORKTREE_ADD_HANG=true", "WK_ANALYZE_HANG_PID_FILE=" + pidFile}
			},
			wantOutput: "analysis source worktree checkout failed or timed out",
		},
		{
			name: "analysis worktree remove",
			configure: func(t *testing.T, bin, pidFile string) []string {
				installAnalyzeCodexHang(t, bin, "diagnosis", pidFile)
				return []string{
					"WK_ANALYZE_GIT_WORKTREE_REMOVE_HANG=true",
					"WK_ANALYSIS_DIAGNOSIS_TIMEOUT_SECONDS=1",
				}
			},
			wantOutput: "local Codex diagnosis exceeded the Analysis Token deadline",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := repoRoot(t)
			temp := t.TempDir()
			bin := filepath.Join(temp, "bin")
			stateDir := filepath.Join(temp, "state")
			callLog := filepath.Join(temp, "calls.log")
			descendantPIDFile := filepath.Join(temp, "descendant.pid")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeAnalyzeFakes(t, bin)
			extraEnvironment := testCase.configure(t, bin, descendantPIDFile)

			command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
				"run-live", "--repository", "example/project")
			command.Dir = root
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				"WK_ANALYZE_HANG_PID_FILE="+descendantPIDFile,
				"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=2",
			)
			command.Env = append(command.Env, extraEnvironment...)
			started := time.Now()
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), testCase.wantOutput) {
				t.Fatalf("analyze error = %v, want bounded %s failure:\n%s", err, testCase.name, output)
			}
			if elapsed := time.Since(started); elapsed > 20*time.Second {
				t.Fatalf("bounded %s took %s, want under 20s", testCase.name, elapsed)
			}
			assertAnalyzeDescendantGone(t, descendantPIDFile)
			calls, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			callText := string(calls)
			if !strings.Contains(callText, "-f operation=close") {
				t.Fatalf("bounded %s failure leaked the Analysis window:\n%s", testCase.name, calls)
			}
			if strings.Contains(testCase.name, "worktree") &&
				!strings.Contains(callText, "git-worktree-environment prompt=0 nosystem=1 global=/dev/null") {
				t.Fatalf("worktree command inherited interactive or global Git config:\n%s", calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeUsesLocalSourceWithoutFetching(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze local source: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), " fetch --quiet origin ") {
		t.Fatalf("locally available source triggered a network fetch:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeBoundsNonInteractiveSourceFetch(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_GIT_SOURCE_PRESENT=false",
		"WK_ANALYZE_GIT_FETCH_HANG=true",
		"WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS=1",
	)
	started := time.Now()
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "source revision fetch timed out after 1 seconds") {
		t.Fatalf("analyze error = %v, want bounded fetch failure:\n%s", err, output)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("bounded source fetch took %s, want under 10s", elapsed)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if !strings.Contains(callText, "git-fetch-terminal-prompt=0") ||
		!strings.Contains(callText, "-f operation=close") {
		t.Fatalf("bounded source fetch was interactive or leaked the Analysis window:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeRepreparesForSameHostObservedIPv4(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_OBSERVED_IPV4=198.51.100.8",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	if got := strings.Count(callText, "-f operation=prepare"); got != 2 {
		t.Fatalf("prepare dispatches = %d, want one initial prepare and one reprepare:\n%s", got, calls)
	}
	operations := make([]string, 0, 4)
	for _, line := range strings.Split(callText, "\n") {
		if !strings.HasPrefix(line, "gh workflow run cloud-sim-analyze.yml ") {
			continue
		}
		switch {
		case strings.Contains(line, "-f operation=prepare"):
			operations = append(operations, "prepare")
		case strings.Contains(line, "-f operation=close"):
			operations = append(operations, "close")
		}
	}
	if got := strings.Join(operations, ","); got != "prepare,close,prepare,close" {
		t.Fatalf("Analysis window operations = %q, want prepare,close,prepare,close:\n%s", got, calls)
	}
	if !strings.Contains(callText, "-rebind") {
		t.Fatalf("second prepare did not use a unique rebind request correlation:\n%s", calls)
	}
	first := strings.Index(callText, "-f client_ipv4=203.0.113.7")
	second := strings.LastIndex(callText, "-f client_ipv4=198.51.100.8")
	if first < 0 || second <= first {
		t.Fatalf("analysis did not replace public echo IPv4 with same-host observation:\n%s", calls)
	}
	if got := strings.Count(callText, "http://198.51.100.20:19443/cloud-view/status"); got != 2 {
		t.Fatalf("same-host IPv4 probes = %d, want one per prepared session:\n%s", got, calls)
	}
}

func TestCloudSimulationAnalyzeClosesAmbiguousRebindRequest(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_OBSERVED_IPV4=198.51.100.8",
		"WK_ANALYZE_REBIND_PREPARE_WATCH_EXIT=72",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot prepare and confirm the correlated Analysis Session") {
		t.Fatalf("ambiguous rebind error = %v:\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	operations := make([]string, 0, 4)
	for _, line := range strings.Split(string(calls), "\n") {
		if !strings.HasPrefix(line, "gh workflow run cloud-sim-analyze.yml ") {
			continue
		}
		switch {
		case strings.Contains(line, "-f operation=prepare"):
			operations = append(operations, "prepare")
		case strings.Contains(line, "-f operation=close"):
			operations = append(operations, "close")
		}
	}
	if got := strings.Join(operations, ","); got != "prepare,close,prepare,close" {
		t.Fatalf("rebind operations = %q, want prepare,close,prepare,close:\n%s", got, calls)
	}
	if got := strings.Count(string(calls), "request_id=local-"); got < 4 ||
		!strings.Contains(string(calls), "-rebind") {
		t.Fatalf("rebind close did not correlate the second request id:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeFallsBackWhenSameHostObservationIsUnavailable(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name string
		env  string
	}{
		{name: "public view closed", env: "WK_ANALYZE_CLOUD_VIEW_CURL_EXIT=7"},
		{name: "legacy status response", env: "WK_ANALYZE_OMIT_OBSERVED_IPV4=true"},
		{name: "malformed status response", env: "WK_ANALYZE_INVALID_CLOUD_VIEW_JSON=true"},
		{name: "unhealthy status persistence", env: "WK_ANALYZE_CLOUD_VIEW_PERSISTENCE_UNHEALTHY=true"},
		{name: "mismatched status identity", env: "WK_ANALYZE_CLOUD_VIEW_RUN_ID_MISMATCH=true"},
		{name: "invalid observed IPv4", env: "WK_ANALYZE_OBSERVED_IPV4=999.1.1.1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
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
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				testCase.env,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				calls, _ := os.ReadFile(callLog)
				t.Fatalf("analyze: %v\n%s\ncalls:\n%s", err, output, calls)
			}
			if !strings.Contains(string(output), "using public echo IPv4 203.0.113.7") {
				t.Fatalf("analysis output missing compatible fallback:\n%s", output)
			}
			calls, err := os.ReadFile(callLog)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(calls), "-f operation=prepare"); got != 1 {
				t.Fatalf("prepare dispatches = %d, want one fallback session:\n%s", got, calls)
			}
			if !strings.Contains(string(calls), "Cache-Control: no-cache") ||
				!strings.Contains(string(calls), "/cloud-view/status?request_id=local-") {
				t.Fatalf("same-host probe was cacheable:\n%s", calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeClosesWindowWhenPrepareHandoffFails(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name string
		env  string
	}{
		{name: "workflow watch", env: "WK_ANALYZE_PREPARE_WATCH_EXIT=72"},
		{name: "session artifact download", env: "WK_ANALYZE_PREPARE_DOWNLOAD_EXIT=71"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
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
			command.Env = append(analyzeFakeEnvironment(bin),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG="+callLog,
				"WK_ANALYZE_STATE_DIR="+stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
				testCase.env,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("analysis accepted a failed prepare handoff:\n%s", output)
			}
			calls, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			callText := string(calls)
			operations := make([]string, 0, 2)
			for _, line := range strings.Split(callText, "\n") {
				if !strings.HasPrefix(line, "gh workflow run cloud-sim-analyze.yml ") {
					continue
				}
				switch {
				case strings.Contains(line, "-f operation=prepare"):
					operations = append(operations, "prepare")
				case strings.Contains(line, "-f operation=close"):
					operations = append(operations, "close")
				}
			}
			if got := strings.Join(operations, ","); got != "prepare,close" {
				t.Fatalf("Analysis window operations = %q, want prepare,close:\n%s", got, calls)
			}
			if strings.Contains(callText, "codex login status") || strings.Contains(callText, "codex exec") {
				t.Fatalf("prepare handoff failure started Codex:\n%s", calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzeFailsClosedWhenSameHostIPv4ChangesAgain(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_OBSERVED_IPV4_SEQUENCE=198.51.100.8,192.0.2.44",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "same-host Analysis egress IPv4 changed again after one rebind") {
		t.Fatalf("analyze error = %v, want second-change failure:\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if got := strings.Count(callText, "-f operation=prepare"); got != 2 {
		t.Fatalf("prepare dispatches = %d, want bounded two attempts:\n%s", got, calls)
	}
	if !strings.Contains(callText, "-f operation=close") {
		t.Fatalf("failed rebind did not close the live Analysis window:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeFailsClosedOnInvalidSameHostIPv4(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_OBSERVED_IPV4_SEQUENCE=198.51.100.8,999.1.1.1",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot verify the same-host Analysis egress IPv4 after rebind: invalid_response") {
		t.Fatalf("analyze error = %v, want invalid-source failure:\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if got := strings.Count(callText, "-f operation=prepare"); got != 2 {
		t.Fatalf("prepare dispatches = %d, want bounded two attempts:\n%s", got, calls)
	}
	if !strings.Contains(callText, "-f operation=close") {
		t.Fatalf("invalid rebind did not close the live Analysis window:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeClassifiesHealthCurlFailure(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	writeSetupExecutable(t, filepath.Join(bin, "sleep"), `#!/usr/bin/env bash
exit 0
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_HEALTH_CURL_EXIT=28",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Analysis MCP health attempt 1/12 failed: timeout (curl exit 28)") ||
		!strings.Contains(string(output), "last failure: timeout") {
		t.Fatalf("analyze error = %v, want bounded curl failure classification:\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if got := strings.Count(callText, ":19092/healthz"); got != 12 {
		t.Fatalf("health attempts = %d, want 12:\n%s", got, calls)
	}
	if !strings.Contains(callText, "-f operation=close") {
		t.Fatalf("health failure did not close the live Analysis window:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeRecoversCorrelatedCloseAfterLocalGitHubErrors(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	for _, testCase := range []struct {
		name        string
		environment []string
		wantOutput  string
		wantView    bool
	}{
		{
			name:        "dispatch accepted before client error",
			environment: []string{"WK_ANALYZE_CLOSE_DISPATCH_EXIT_AFTER_ACCEPT=72"},
			wantOutput:  "dispatch returned gh exit 72, but the exact correlated workflow",
		},
		{
			name: "accepted dispatch survives transient list outage without redispatch",
			environment: []string{
				"WK_ANALYZE_CLOSE_DISPATCH_EXIT_AFTER_ACCEPT=72",
				"WK_ANALYZE_CLOSE_LIST_FAILURES=3",
			},
			wantOutput: "dispatch returned gh exit 72, but the exact correlated workflow",
		},
		{
			name:        "poll interrupted before terminal success",
			environment: []string{"WK_ANALYZE_CLOSE_VIEW_FAILURES=1"},
			wantOutput:  "completed successfully",
			wantView:    true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := repoRoot(t)
			temp := t.TempDir()
			bin := filepath.Join(temp, "bin")
			stateDir := filepath.Join(temp, "state")
			callLog := filepath.Join(temp, "calls.log")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeAnalyzeFakes(t, bin)
			writeSetupExecutable(t, filepath.Join(bin, "sleep"), `#!/usr/bin/env bash
exit 0
`)

			command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
				"run-live", "--repository", "example/project")
			command.Dir = root
			command.Env = append(analyzeFakeEnvironment(bin), append([]string{
				"PATH=" + bin + ":" + os.Getenv("PATH"),
				"WK_ANALYZE_CALL_LOG=" + callLog,
				"WK_ANALYZE_STATE_DIR=" + stateDir,
				"WK_ANALYZE_SESSION_STATE=live",
			}, testCase.environment...)...)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), testCase.wantOutput) {
				calls, _ := os.ReadFile(callLog)
				t.Fatalf("analyze close recovery: err=%v\n%s\ncalls:\n%s", err, output, calls)
			}
			calls, err := os.ReadFile(callLog)
			if err != nil {
				t.Fatal(err)
			}
			callText := string(calls)
			if got := strings.Count(callText, "-f operation=close"); got != 1 {
				t.Fatalf("close dispatches = %d, want one correlated dispatch:\n%s", got, calls)
			}
			if testCase.wantView && !strings.Contains(callText, "gh run view 103 --repo example/project --json status,conclusion") {
				t.Fatalf("transient poll did not query terminal workflow state:\n%s", calls)
			}
		})
	}
}

func TestCloudSimulationAnalyzePreservesPrimaryFailureWhenCloseCannotBeProven(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	writeSetupExecutable(t, filepath.Join(bin, "sleep"), `#!/usr/bin/env bash
exit 0
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
		"WK_ANALYZE_HEALTH_CURL_EXIT=28",
		"WK_ANALYZE_CLOSE_HANG=true",
		"WK_ANALYZE_HANG_PID_FILE="+filepath.Join(temp, "close-descendant.pid"),
		"WK_LOCAL_ANALYSIS_CLOSE_COMMAND_TIMEOUT_SECONDS=1",
	)
	started := time.Now()
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Analysis MCP did not become reachable") ||
		!strings.Contains(string(output), "Warning: Analysis close for request local-") {
		t.Fatalf("analysis did not preserve the primary failure and close warning: err=%v\n%s", err, output)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("EXIT close took %s, want under 10s", elapsed)
	}
	assertAnalyzeDescendantGone(t, filepath.Join(temp, "close-descendant.pid"))
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := strings.Count(string(calls), "-f operation=close"); got != 1 {
		t.Fatalf("EXIT close dispatches = %d, want one bounded attempt:\n%s", got, calls)
	}
}

func TestCloudSimulationAnalyzeClearsNonWorkloadObservationState(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
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
				Tool   string          `json:"tool"`
				State  json.RawMessage `json:"state"`
				Status json.RawMessage `json:"status"`
				Note   json.RawMessage `json:"note"`
			} `json:"observation_references"`
		} `json:"diagnosis"`
	}
	if err := json.Unmarshal(result, &outcome); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	for _, observation := range outcome.Diagnosis.Observations {
		if len(observation.State) == 0 || len(observation.Status) == 0 || len(observation.Note) == 0 {
			t.Fatalf("observation nullable keys must be present: %+v", observation)
		}
		if observation.Tool != "workload_inspect" && (string(observation.State) != "null" || string(observation.Status) != "null") {
			t.Fatalf("non-workload state/status must be null after canonicalization: %+v", observation)
		}
	}
}

func TestCloudSimulationAnalyzeDiscoversGoFromGOROOTOutsidePATH(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
		"jq", "ln", "mkdir", "mktemp", "mv", "perl", "rm", "sleep", "sort", "tr",
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
	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin,
		"GOROOT="+goRoot,
		"WK_GH_BIN="+filepath.Join(bin, "gh"),
		"WK_GO_BIN="+filepath.Join(goRoot, "bin", "go"),
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
	if !strings.Contains(string(calls), " run ./cmd/wkclouddiagnosis validate") {
		t.Fatalf("analysis did not use the discovered Go toolchain:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeUsesExplicitToolsOutsideStrippedPATH(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	toolBin := filepath.Join(temp, "tool-bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	for _, directory := range []string{bin, toolBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeAnalyzeFakes(t, bin)
	for _, tool := range []struct {
		name     string
		override string
	}{
		{name: "gh", override: "selected-gh"},
		{name: "go", override: "selected-go"},
	} {
		if err := os.Rename(filepath.Join(bin, tool.name), filepath.Join(toolBin, tool.override)); err != nil {
			t.Fatal(err)
		}
		writeSetupExecutable(t, filepath.Join(bin, tool.name), `#!/usr/bin/env bash
exit 98
`)
	}
	preservedCommands := []string{
		"awk", "base64", "bash", "cat", "chmod", "cp", "date", "dirname", "env", "grep",
		"jq", "ln", "mkdir", "mktemp", "mv", "perl", "rm", "sleep", "sort", "tr",
	}
	for _, name := range preservedCommands {
		source, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("find required test command %s: %v", name, err)
		}
		target := filepath.Join(bin, name)
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin,
		"WK_GH_BIN="+filepath.Join(toolBin, "selected-gh"),
		"WK_GO_BIN="+filepath.Join(toolBin, "selected-go"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze with explicit tools outside PATH: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"gh auth status", " run ./cmd/wkclouddiagnosis validate"} {
		if !strings.Contains(string(calls), required) {
			t.Fatalf("analysis did not use resolved tool %q:\n%s", required, calls)
		}
	}
}

func TestCloudSimulationAnalyzeRejectsProjectCodexOverridesBeforeExec(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
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
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
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
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	// A released provider preflight is terminal evidence and must remain
	// verifiable without any live-session client material or local Go toolchain.
	for _, name := range []string{"curl", "openssl"} {
		if err := os.Remove(filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		"awk", "bash", "cat", "chmod", "date", "dirname", "env", "jq", "ln", "mkdir",
		"mktemp", "mv", "perl", "rm", "tr",
	} {
		source, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("find released-path command %s: %v", name, err)
		}
		target := filepath.Join(bin, name)
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}
	resultFile := filepath.Join(temp, "released-result.json")

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-released", "--repository", "example/project", "--result-file", resultFile)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin,
		"WK_GH_BIN="+filepath.Join(bin, "gh"),
		"WK_GO_BIN="+filepath.Join(temp, "missing-go"),
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
	if !strings.Contains(string(result), `"state": "released"`) || !strings.Contains(string(result), `"diagnosis": null`) ||
		!strings.Contains(string(result), `"provider"`) || !strings.Contains(string(result), `"resources": []`) {
		t.Fatalf("released result is not structured:\n%s", result)
	}
	if temporaryResults, err := filepath.Glob(resultFile + ".tmp.*"); err != nil || len(temporaryResults) != 0 {
		t.Fatalf("released result left temporary publication files: matches=%v err=%v", temporaryResults, err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	if !strings.Contains(callText, "-f operation=inspect") || strings.Contains(callText, "-f operation=prepare") ||
		strings.Contains(callText, "codex login status") || strings.Contains(callText, "codex exec") ||
		strings.Contains(callText, "operation=close") || strings.Contains(callText, "curl ") ||
		strings.Contains(callText, "openssl ") || strings.Contains(callText, "go ") {
		t.Fatalf("released analysis continued after terminal state:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeRefusesLiveRunWithoutClientMaterialTools(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	temp := t.TempDir()
	bin := filepath.Join(temp, "bin")
	stateDir := filepath.Join(temp, "state")
	callLog := filepath.Join(temp, "calls.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnalyzeFakes(t, bin)
	if err := os.Remove(filepath.Join(bin, "openssl")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"awk", "bash", "cat", "chmod", "date", "dirname", "env", "jq", "ln", "mkdir",
		"mktemp", "mv", "perl", "rm", "sleep", "tr",
	} {
		source, lookErr := exec.LookPath(name)
		if lookErr != nil {
			t.Fatalf("find live-path command %s: %v", name, lookErr)
		}
		target := filepath.Join(bin, name)
		if _, statErr := os.Lstat(target); statErr == nil {
			continue
		}
		if linkErr := os.Symlink(source, target); linkErr != nil {
			t.Fatal(linkErr)
		}
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "cloud-sim", "analyze.sh"),
		"run-live", "--repository", "example/project")
	command.Dir = root
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin,
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "openssl is required for a live Analysis Session") {
		t.Fatalf("live analysis accepted missing client material tool: err=%v\n%s", err, output)
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	callText := string(calls)
	if !strings.Contains(callText, "-f operation=inspect") || strings.Contains(callText, "-f operation=prepare") ||
		strings.Contains(callText, "-f operation=close") {
		t.Fatalf("live missing-material path opened an Analysis Session:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeReportsBrokerInsufficientEvidenceBeforeCodex(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
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
	callText := string(calls)
	if !strings.Contains(callText, "-f operation=inspect") ||
		strings.Contains(callText, "-f operation=prepare") ||
		strings.Contains(callText, "-f operation=close") {
		t.Fatalf("read-only broker preflight opened an Analysis access window:\n%s", calls)
	}
}

func TestCloudSimulationAnalyzeDefersRemediationUntilProviderRelease(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
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
	command.Env = append(analyzeFakeEnvironment(bin),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"WK_ANALYZE_CALL_LOG="+callLog,
		"WK_ANALYZE_STATE_DIR="+stateDir,
		"WK_ANALYZE_SESSION_STATE=live",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		calls, _ := os.ReadFile(callLog)
		t.Fatalf("analyze deferred remediation: %v\n%s\ncalls:\n%s", err, output, calls)
	}
	if !strings.Contains(string(output), `"verdict": "product_defect"`) ||
		!strings.Contains(string(output), "Automatic remediation is deferred") {
		t.Fatalf("analysis did not preserve diagnosis and explain deferral:\n%s", output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	callText := string(calls)
	if got := strings.Count(callText, "-f operation=close"); got != 1 {
		t.Fatalf("analysis close dispatches = %d, want exactly one:\n%s", got, calls)
	}
	for _, forbidden := range []string{
		"cloud-remediation",
		" worktree add -b ",
		" push --no-verify ",
		"gh workflow run ci.yml",
		"gh pr create",
	} {
		if strings.Contains(callText, forbidden) {
			t.Fatalf("analysis performed pre-release remediation operation %q:\n%s", forbidden, calls)
		}
	}
}

func installAnalyzeCodexHang(t *testing.T, bin, target, descendantPIDFile string) {
	t.Helper()
	base := filepath.Join(bin, "codex-base")
	if err := os.Rename(filepath.Join(bin, "codex"), base); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
base=` + strconv.Quote(base) + `
target=` + strconv.Quote(target) + `
pid_file=` + strconv.Quote(descendantPIDFile) + `
if [[ "${1:-}" == --version || "${1:-} ${2:-}" == "login status" ]]; then
  exec "$base" "$@"
fi
if [[ "${1:-}" == exec ]]; then
  is_remediation=false
  if [[ " $* " == *' default_permissions="cloud-remediation" '* ]]; then is_remediation=true; fi
  if [[ "$target" == diagnosis && "$is_remediation" == false ]] ||
    [[ "$target" == remediation && "$is_remediation" == true ]]; then
    trap '' HUP TERM
    /bin/sleep 30 &
    child=$!
    printf '%s\n' "$child" >"$pid_file"
    wait "$child"
  fi
fi
exec "$base" "$@"
`
	writeSetupExecutable(t, filepath.Join(bin, "codex"), script)
}

func assertAnalyzeDescendantGone(t *testing.T, pidFile string) {
	t.Helper()
	payload, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read hanging descendant pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil {
		t.Fatalf("parse hanging descendant pid: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("bounded command leaked descendant pid %d", pid)
	}
}

func writeAnalyzeFakes(t *testing.T, bin string) {
	t.Helper()
	writeSetupExecutable(t, filepath.Join(bin, "curl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "$*" == *'/healthz'* ]]; then
	if [[ -n "${WK_ANALYZE_HEALTH_CURL_EXIT:-}" ]]; then
		exit "$WK_ANALYZE_HEALTH_CURL_EXIT"
	fi
  printf '%s\n' '{"status":"ok","run_id":"run-live","run_state":"running"}'
  exit 0
fi
if [[ "$*" == *':19443/cloud-view/status'* ]]; then
	if [[ -n "${WK_ANALYZE_CLOUD_VIEW_CURL_EXIT:-}" ]]; then
		exit "$WK_ANALYZE_CLOUD_VIEW_CURL_EXIT"
	fi
	if [[ "${WK_ANALYZE_INVALID_CLOUD_VIEW_JSON:-}" == true ]]; then
		printf '%s\n' '{'
		exit 0
	fi
	if [[ "${WK_ANALYZE_OMIT_OBSERVED_IPV4:-}" == true ]]; then
		printf '%s\n' '{"run_id":"run-live","interactive":false,"operator_modified":false,"persistence_healthy":true}'
		exit 0
	fi
	status_run_id=run-live
	status_persistence_healthy=true
	if [[ "${WK_ANALYZE_CLOUD_VIEW_RUN_ID_MISMATCH:-}" == true ]]; then status_run_id=run-other; fi
	if [[ "${WK_ANALYZE_CLOUD_VIEW_PERSISTENCE_UNHEALTHY:-}" == true ]]; then status_persistence_healthy=false; fi
	observed_ipv4="${WK_ANALYZE_OBSERVED_IPV4:-203.0.113.7}"
	if [[ -n "${WK_ANALYZE_OBSERVED_IPV4_SEQUENCE:-}" ]]; then
		counter_file="$WK_ANALYZE_STATE_DIR/observed-ipv4-count"
		counter="$(cat "$counter_file" 2>/dev/null || printf '0')"
		IFS=, read -r -a observed_values <<<"$WK_ANALYZE_OBSERVED_IPV4_SEQUENCE"
		if ((counter >= ${#observed_values[@]})); then counter=$((${#observed_values[@]} - 1)); fi
		observed_ipv4="${observed_values[$counter]}"
		printf '%s' "$((counter + 1))" >"$counter_file"
	fi
  jq -cn --arg run_id "$status_run_id" --arg observed_ipv4 "$observed_ipv4" \
    --argjson persistence_healthy "$status_persistence_healthy" \
    '{run_id:$run_id,interactive:false,operator_modified:false,persistence_healthy:$persistence_healthy,observed_ipv4:$observed_ipv4}'
  exit 0
fi
printf '%s\n' '203.0.113.7'
`)
	writeSetupExecutable(t, filepath.Join(bin, "openssl"), `#!/usr/bin/env bash
set -euo pipefail
printf 'openssl %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "${WK_ANALYZE_OPENSSL_HANG:-}" == "${1:-}" ]]; then
  trap '' HUP TERM
  /bin/sleep 30 &
  child=$!
  printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
  wait "$child"
fi
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
  dgst)
    cat >/dev/null
    printf '%s\n' '494c9d9f353d3aa18a4ada2697e2a7bac90492022d9821ba0a5a6f4c3b15233a'
    ;;
  *) exit 97 ;;
esac
`)
	writeSetupExecutable(t, filepath.Join(bin, "codex"), `#!/usr/bin/env bash
set -euo pipefail
call_log="${WK_ANALYZE_CALL_LOG:-$(cd "$(dirname "$0")/.." && pwd)/calls.log}"
printf 'codex %s\n' "$*" >>"$call_log"
if [[ "$1" == "--version" ]]; then
  if [[ "${WK_ANALYZE_CODEX_HANG:-}" == version ]]; then /bin/sleep 30; fi
  printf '%s\n' 'codex-cli 999.0.0'
  exit 0
fi
printf 'codex-env home=%s source=%s auth=%s cache=%s\n' "${CODEX_HOME:-}" \
  "${WK_ANALYZE_SOURCE_CODEX_HOME:-}" "$([[ -f "${CODEX_HOME:-}/auth.json" ]] && printf yes || printf no)" \
  "$([[ -e "${CODEX_HOME:-}/models_cache.json" ]] && printf yes || printf no)" >>"$call_log"
LANG=C LC_ALL=C perl -e 'my @s = stat($ARGV[0]); printf "codex-auth-mode %o\n", ($s[2] & 0777)' \
  "$CODEX_HOME/auth.json" >>"$call_log"
test -n "${CODEX_HOME:-}"
case "$CODEX_HOME" in
  */wukongim-cloud-analysis.*/codex-home) ;;
  *) exit 90 ;;
esac
test -f "$CODEX_HOME/auth.json"
test ! -e "$CODEX_HOME/models_cache.json"
test ! -e "$CODEX_HOME/config.toml"
LANG=C LC_ALL=C perl -e 'my @s = stat($ARGV[0]); exit((($s[2] & 0777) == 0600) ? 0 : 1)' "$CODEX_HOME/auth.json"
printf 'codex-home %s\n' "$CODEX_HOME" >>"$call_log"
if [[ "$1 $2" == "login status" ]]; then
  if [[ "${WK_ANALYZE_CODEX_HANG:-}" == login ]]; then /bin/sleep 30; fi
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
{"schema":"wukongim/cloud-simulation-diagnosis/v1","run_identity":{"run_id":"run-live","source_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scenario_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"analyzed_window":{"start":"2026-07-14T00:00:00Z","end":"2026-07-14T00:30:00Z"},"verdict":"product_defect","severity":"high","confidence":0.91,"root_cause_scope":"product","summary":"Confirmed append defect.","observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T00:30:00Z","window":"final","complete":true,"state":"completed","status":"failed","note":null}],"supporting_signals":["append errors"],"contradictory_signals":[],"unresolved_signals":[],"remediation_eligibility":{"eligible":true,"reason":"repository attributable and testable","repository_attributable":true,"testable":true},"proposed_regression_coverage":["add deterministic append regression"],"cloud_revalidation_required":true}
JSON
exit 0
fi
cat >"$output" <<'JSON'
{"schema":"wukongim/cloud-simulation-diagnosis/v1","run_identity":{"run_id":"run-live","source_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scenario_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"analyzed_window":{"start":"2026-07-14T00:00:00Z","end":"2026-07-14T00:30:00Z"},"verdict":"healthy","severity":"none","confidence":0.99,"root_cause_scope":"none","summary":"No anomaly found.","observation_references":[{"tool":"workload_inspect","node":"sim","observed_at":"2026-07-14T00:30:00Z","window":"final","complete":true,"state":"completed","status":"passed","note":null}],"supporting_signals":["workload passed"],"contradictory_signals":[],"unresolved_signals":[],"remediation_eligibility":{"eligible":false,"reason":"healthy","repository_attributable":false,"testable":false},"proposed_regression_coverage":[],"cloud_revalidation_required":false}
JSON
if [[ "$mode" == mismatch ]]; then
  jq '.run_identity.source_sha = "cccccccccccccccccccccccccccccccccccccccc"' "$output" >"$output.tmp"
  mv "$output.tmp" "$output"
elif [[ "$mode" == nonworkload-state ]]; then
  jq '.observation_references += [
        {"tool":"run_inspect","node":"run","observed_at":"2026-07-14T00:30:00Z","window":"point-in-time","complete":true,"state":"in_progress","status":"passed"},
        {"tool":"logs_search","node":"node-1","observed_at":"2026-07-14T00:30:00Z","window":"bounded","complete":true}
      ]' "$output" >"$output.tmp"
  mv "$output.tmp" "$output"
fi
`)
	writeSetupExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ " $* " == *' run ./cmd/wkclouddiagnosis validate '* && "${WK_ANALYZE_GO_VALIDATE_HANG:-}" == true ]]; then
  trap '' HUP TERM
  /bin/sleep 30 &
  child=$!
  printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
  wait "$child"
fi
if [[ "$*" == "env GOVERSION" ]]; then
  printf '%s\n' go1.25.11
elif [[ "$*" == "env GOMODCACHE" ]]; then
  module_cache="$(cd "$(dirname "$0")/.." && pwd)/gomodcache"
  mkdir -p "$module_cache"
  printf '%s\n' "$module_cache"
elif [[ "$*" == "env GOROOT" ]]; then
  go_root="$WK_ANALYZE_STATE_DIR/goroot"
  mkdir -p "$go_root/bin"
  if [[ ! -e "$go_root/bin/go" ]]; then
    ln -s "$(cd "$(dirname "$0")" && pwd)/${0##*/}" "$go_root/bin/go"
  fi
  printf '%s\n' "$go_root"
fi
exit 0
`)
	writeSetupExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$WK_ANALYZE_CALL_LOG"
if [[ "${1:-}" == --version ]]; then
  printf '%s\n' 'gh version 2.96.0 (test)'
  exit 0
fi
mkdir -p "$WK_ANALYZE_STATE_DIR"
case "$1" in
  auth) exit 0 ;;
  repo) printf '%s\n' example/project ;;
  api) printf '%s\n' '{}' ;;
  workflow)
    if [[ "$*" == *"ci.yml"* ]]; then
      printf '%s' 104 >"$WK_ANALYZE_STATE_DIR/ci-run-id"
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
    if [[ "$operation" == close && "${WK_ANALYZE_CLOSE_HANG:-}" == true ]]; then
      trap '' HUP TERM
      /bin/sleep 30 &
      child=$!
      printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
      wait "$child"
    fi
    if [[ "$operation" == close && -n "${WK_ANALYZE_CLOSE_DISPATCH_EXIT_AFTER_ACCEPT:-}" ]]; then
      exit "$WK_ANALYZE_CLOSE_DISPATCH_EXIT_AFTER_ACCEPT"
    fi
    ;;
  run)
    case "$2" in
      list)
        if [[ "$*" == *"ci.yml"* ]]; then
          jq -cn '[{databaseId:104,headSha:"dddddddddddddddddddddddddddddddddddddddd"}]'
          exit 0
        fi
        operation="$(cat "$WK_ANALYZE_STATE_DIR/operation")"
        if [[ "$operation" == close && -n "${WK_ANALYZE_CLOSE_LIST_FAILURES:-}" ]]; then
          failure_count_file="$WK_ANALYZE_STATE_DIR/close-list-failure-count"
          failure_count="$(cat "$failure_count_file" 2>/dev/null || printf '0')"
          if ((failure_count < WK_ANALYZE_CLOSE_LIST_FAILURES)); then
            printf '%s' "$((failure_count + 1))" >"$failure_count_file"
            exit 75
          fi
        fi
        current="$(cat "$WK_ANALYZE_STATE_DIR/run-id")"
        request_id="$(cat "$WK_ANALYZE_STATE_DIR/request-id")"
        jq -cn --argjson current "$current" --arg operation "$operation" --arg request_id "$request_id" '
          [{databaseId:999,displayTitle:"Cloud Simulation Analysis prepare another-request"},
           {databaseId:$current,displayTitle:("Cloud Simulation Analysis " + $operation + " " + $request_id)}]'
        ;;
      watch)
        if [[ "$(cat "$WK_ANALYZE_STATE_DIR/operation")" == prepare &&
          "$(cat "$WK_ANALYZE_STATE_DIR/request-id")" == *-rebind &&
          -n "${WK_ANALYZE_REBIND_PREPARE_WATCH_EXIT:-}" ]]; then
          exit "$WK_ANALYZE_REBIND_PREPARE_WATCH_EXIT"
        fi
        if [[ -n "${WK_ANALYZE_PREPARE_WATCH_EXIT:-}" && "$(cat "$WK_ANALYZE_STATE_DIR/operation")" == prepare ]]; then
          exit "$WK_ANALYZE_PREPARE_WATCH_EXIT"
        fi
        if [[ "$(cat "$WK_ANALYZE_STATE_DIR/operation")" == close ]]; then
          if [[ -n "${WK_ANALYZE_CLOSE_WATCH_EXIT:-}" ]]; then
            exit "$WK_ANALYZE_CLOSE_WATCH_EXIT"
          fi
          if [[ -n "${WK_ANALYZE_CLOSE_CONCLUSION:-}" && "$WK_ANALYZE_CLOSE_CONCLUSION" != success ]]; then
            exit 1
          fi
        fi
        exit 0
        ;;
      view)
        operation="$(cat "$WK_ANALYZE_STATE_DIR/operation")"
        if [[ "$operation" == close && -n "${WK_ANALYZE_CLOSE_VIEW_FAILURES:-}" ]]; then
          failure_count_file="$WK_ANALYZE_STATE_DIR/close-view-failure-count"
          failure_count="$(cat "$failure_count_file" 2>/dev/null || printf '0')"
          if ((failure_count < WK_ANALYZE_CLOSE_VIEW_FAILURES)); then
            printf '%s' "$((failure_count + 1))" >"$failure_count_file"
            exit 73
          fi
        fi
        conclusion=success
        request_id="$(cat "$WK_ANALYZE_STATE_DIR/request-id")"
        if [[ "$operation" == prepare && "$request_id" == *-rebind &&
          -n "${WK_ANALYZE_REBIND_PREPARE_WATCH_EXIT:-}" ]]; then
          conclusion=failure
        elif [[ "$operation" == prepare && -n "${WK_ANALYZE_PREPARE_WATCH_EXIT:-}" ]]; then
          conclusion=failure
        elif [[ "$operation" == close && -n "${WK_ANALYZE_CLOSE_CONCLUSION:-}" ]]; then
          conclusion="$WK_ANALYZE_CLOSE_CONCLUSION"
        fi
        jq -cn --arg conclusion "$conclusion" '{status:"completed",conclusion:$conclusion}'
        ;;
      download)
        destination=""
        while (($#)); do if [[ "$1" == --dir ]]; then destination="$2"; break; fi; shift; done
        test -n "$destination"
        mkdir -p "$destination"
        request_id="$(cat "$WK_ANALYZE_STATE_DIR/request-id")"
        operation="$(cat "$WK_ANALYZE_STATE_DIR/operation")"
        if [[ "$operation" == prepare && -n "${WK_ANALYZE_PREPARE_DOWNLOAD_EXIT:-}" ]]; then
          exit "$WK_ANALYZE_PREPARE_DOWNLOAD_EXIT"
        fi
        if [[ "$operation" == inspect ]]; then
          if [[ "$WK_ANALYZE_SESSION_STATE" == released ]]; then
            jq -n --arg request_id "$request_id" '{schema:"wukongim/cloud-simulation-analysis-preflight/v1",state:"released",run_id:"run-released",request_id:$request_id,message:"Simulation Run run-released 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。",provider:{state:"released",resources:[]}}' >"$destination/preflight.json"
          elif [[ "$WK_ANALYZE_SESSION_STATE" == insufficient_evidence ]]; then
            jq -n --arg request_id "$request_id" --arg message "${WK_ANALYZE_SESSION_MESSAGE:-Provider preflight could not establish a live identity-matched Simulation Run.}" '{schema:"wukongim/cloud-simulation-analysis-preflight/v1",state:"insufficient_evidence",run_id:"run-insufficient",request_id:$request_id,message:$message,provider:null}' >"$destination/preflight.json"
          else
            jq -n --arg request_id "$request_id" '{schema:"wukongim/cloud-simulation-analysis-preflight/v1",state:"live",run_id:"run-live",request_id:$request_id,message:"Provider preflight confirmed a live identity-matched Simulation Run; client material is required to open a session.",provider:{state:"live",resources:[{kind:"instance",role:"sim"}]}}' >"$destination/preflight.json"
          fi
        elif [[ "$WK_ANALYZE_SESSION_STATE" == released ]]; then
          jq -n --arg request_id "$request_id" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"released",run_id:"run-released",request_id:$request_id,message:"Simulation Run run-released 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。",provider:{state:"released",resources:[]}}' >"$destination/session.json"
        elif [[ "$WK_ANALYZE_SESSION_STATE" == insufficient_evidence ]]; then
          jq -n --arg request_id "$request_id" --arg message "${WK_ANALYZE_SESSION_MESSAGE:-Provider preflight could not establish a live identity-matched Simulation Run.}" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"insufficient_evidence",run_id:"run-insufficient",request_id:$request_id,message:$message,provider:null}' >"$destination/session.json"
        else
		  fingerprint='sha256:494c9d9f353d3aa18a4ada2697e2a7bac90492022d9821ba0a5a6f4c3b15233a'
          jq -n --arg request_id "$request_id" --arg fingerprint "$fingerprint" '{schema:"wukongim/cloud-simulation-analysis-session/v1",state:"live",run_id:"run-live",request_id:$request_id,source_sha:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",scenario_digest:"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",mcp_url:"https://198.51.100.20:19092/mcp",expires_at:"2099-07-14T01:00:00Z",ca_fingerprint:$fingerprint}' >"$destination/session.json"
          printf '%s' encrypted >"$destination/encrypted-token.bin"
          printf '%s' certificate >"$destination/pinned-ca.pem"
        fi
        ;;
      *) exit 95 ;;
    esac
    ;;

  pr)
    if [[ "${2:-}" == create && -n "${WK_ANALYZE_PR_CREATE_EXIT:-}" ]]; then
      exit "$WK_ANALYZE_PR_CREATE_EXIT"
    fi
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
if [[ "$1" == -C && "$3" == cat-file ]]; then
  if [[ "${WK_ANALYZE_GIT_SOURCE_PRESENT:-true}" == true || -f "$WK_ANALYZE_STATE_DIR/source-present" ]]; then
    exit 0
  fi
  exit 128
fi
if [[ "$1" == -C && "$3" == fetch ]]; then
  printf 'git-fetch-terminal-prompt=%s\n' "${GIT_TERMINAL_PROMPT:-unset}" >>"$WK_ANALYZE_CALL_LOG"
  if [[ "${WK_ANALYZE_GIT_FETCH_HANG:-}" == true ]]; then /bin/sleep 30; fi
  mkdir -p "$WK_ANALYZE_STATE_DIR"
  : >"$WK_ANALYZE_STATE_DIR/source-present"
  exit 0
fi
if [[ "$1" == -C && "$3" == push ]]; then
  if [[ " $* " == *' --delete '* ]]; then
    printf 'git-delete-terminal-prompt=%s\n' "${GIT_TERMINAL_PROMPT:-unset}" >>"$WK_ANALYZE_CALL_LOG"
    if [[ "${WK_ANALYZE_GIT_DELETE_HANG:-}" == true ]]; then
      trap '' HUP TERM
      /bin/sleep 30 &
      child=$!
      printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
      wait "$child"
    fi
  else
    printf 'git-push-terminal-prompt=%s\n' "${GIT_TERMINAL_PROMPT:-unset}" >>"$WK_ANALYZE_CALL_LOG"
    if [[ "${WK_ANALYZE_GIT_PUSH_HANG:-}" == true ]]; then
      trap '' HUP TERM
      /bin/sleep 30 &
      child=$!
      printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
      wait "$child"
    fi
  fi
  exit 0
fi
if [[ "$1" == -C && "$3" == worktree ]]; then
  printf 'git-worktree-environment prompt=%s nosystem=%s global=%s\n' \
    "${GIT_TERMINAL_PROMPT:-unset}" "${GIT_CONFIG_NOSYSTEM:-unset}" "${GIT_CONFIG_GLOBAL:-unset}" \
    >>"$WK_ANALYZE_CALL_LOG"
  if [[ "$4" == add ]]; then
    if [[ "${WK_ANALYZE_GIT_WORKTREE_ADD_HANG:-}" == true ]]; then
      trap '' HUP TERM
      /bin/sleep 30 &
      child=$!
      printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
      wait "$child"
    fi
    if [[ "$5" == --detach ]]; then
      mkdir -p "$6"
      printf '%s' deployed >"$6/.deployed-source-marker"
      if [[ "${WK_ANALYZE_INJECT_PROJECT_CODEX_CONFIG:-}" == true ]]; then
        mkdir -p "$6/.codex"
        printf '%s\n' '[permissions.cloud-analysis.filesystem]' '"/" = "write"' >"$6/.codex/config.toml"
      fi
    else
      mkdir -p "$7"
    fi
    exit 0
  fi
  if [[ "$4" == remove ]]; then
    if [[ "${WK_ANALYZE_GIT_WORKTREE_REMOVE_HANG:-}" == true ]]; then
      trap '' HUP TERM
      /bin/sleep 30 &
      child=$!
      printf '%s\n' "$child" >"$WK_ANALYZE_HANG_PID_FILE"
      wait "$child"
    fi
    rm -rf "$6"
    exit 0
  fi
fi
if [[ "$1" == -C && "$3" == branch ]]; then exit 0; fi
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
    check-ref-format) exit 0 ;;
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

func analyzeFakeEnvironment(bin string) []string {
	return append(os.Environ(),
		"WK_GH_BIN="+filepath.Join(bin, "gh"),
		"WK_GO_BIN="+filepath.Join(bin, "go"),
	)
}
