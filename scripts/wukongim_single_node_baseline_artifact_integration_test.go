//go:build integration

package scripts_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSingleNodeBaselineNoStartShortRunCannotAuthorize(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	prepared := prepareFakeSingleNodeSealedBaseline(t, false, "# fake effective single-node cluster config\n", nil)
	output, err := prepared.command.CombinedOutput()
	runDir := prepared.runDir
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 6 {
		t.Fatalf("single-node diagnostic exit = %v, want 6\n%s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(runDir, "local-baseline.json")); statErr != nil {
		t.Fatalf("preflight completion marker missing: %v\n%s", statErr, output)
	}
	result := readSingleNodeBaselineResult(t, runDir)
	if result.Outcome != "insufficient_evidence" || result.Reason != "artifact_seal_verification_failed" {
		t.Fatalf("diagnostic result = %#v, want typed artifact failure; output:\n%s", result, output)
	}
	if result.ReviewedContractSatisfied || result.AuthorizesThreeNodeDiagnostic {
		t.Fatalf("--no-start short run bypassed reviewed authorization: %#v", result)
	}
	if result.ArtifactSealValid {
		t.Fatalf("external cluster without a sealed wukongim binary claimed a complete reproduction seal: %#v", result)
	}
	if _, statErr := os.Stat(filepath.Join(runDir, "bin", "wukongim")); !os.IsNotExist(statErr) {
		t.Fatalf("no-start run unexpectedly fabricated a tested server binary: %v", statErr)
	}
}

func TestSingleNodeBaselineRestartsOneProductGenerationPerRateAndPreservesData(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	prepared := prepareFakeSingleNodeSealedBaseline(t, true, "# fake effective single-node cluster config\n", nil)
	calls := filepath.Join(prepared.callsDir, "cluster-generations.log")
	for index := 0; index+1 < len(prepared.command.Args); index++ {
		if prepared.command.Args[index] == "--qps" {
			prepared.command.Args[index+1] = "100,200"
			break
		}
	}
	output, _ := prepared.command.CombinedOutput()
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read product generation calls: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("product generation calls = %q, want two\n%s", lines, output)
	}
	if !strings.Contains(lines[0], "args=--clean ") || strings.Contains(lines[0], "--no-build") {
		t.Fatalf("first generation must clean and build once: %q", lines[0])
	}
	if strings.Contains(lines[1], "--clean") || !strings.Contains(lines[1], "args=--no-build ") {
		t.Fatalf("second generation must preserve data and reuse the sealed binary: %q", lines[1])
	}
	if !strings.Contains(lines[0], "durable_before=false") || !strings.Contains(lines[1], "durable_before=true") {
		t.Fatalf("durable state did not survive the intentional generation restart: %q", lines)
	}
	for _, tag := range []string{"000100", "000200"} {
		for _, name := range []string{"node1.log", "cluster-start.log"} {
			path := filepath.Join(prepared.runDir, "reports", tag+"-qps", "logs", "product", name)
			if info, statErr := os.Stat(path); statErr != nil || info.Size() == 0 {
				t.Fatalf("sealed generation log missing for qps=%s: %s (%v)", tag, path, statErr)
			}
		}
	}
}

func TestSingleNodeBaselineFreezesExternalConfigAcrossProductGenerations(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	const originalConfig = "snapshot-generation=original\nsecret-canary=runtime-config-never-retained\n"
	snapshotParent := t.TempDir()
	prepared := prepareFakeSingleNodeSealedBaseline(t, true, originalConfig, []string{"TMPDIR=" + snapshotParent})
	observed := filepath.Join(prepared.callsDir, "generation-config-sha.log")
	writeFakeSingleNodeSealStart(t, prepared.startScript, prepared.wukongimBin,
		filepath.Join(prepared.callsDir, "cluster-generations.log"), observed, prepared.configPath)
	for index := 0; index+1 < len(prepared.command.Args); index++ {
		if prepared.command.Args[index] == "--qps" {
			prepared.command.Args[index+1] = "100,200"
			break
		}
	}
	output, err := prepared.command.CombinedOutput()
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	lines := strings.Fields(readFile(t, observed))
	if len(lines) != 2 || lines[0] != lines[1] {
		t.Fatalf("product generations used different runtime configs: %q\n%s", lines, output)
	}
	originalDigest := sha256.Sum256([]byte(originalConfig))
	if want := hex.EncodeToString(originalDigest[:]); lines[0] != want {
		t.Fatalf("product runtime config digest = %q, want frozen original %q", lines[0], want)
	}
	if got := readFile(t, prepared.configPath); !strings.Contains(got, "snapshot-generation=mutated") {
		t.Fatalf("test did not mutate external source between generations: %q", got)
	}
	identity := readFile(t, filepath.Join(prepared.runDir, "artifact-identity.tsv"))
	if got, want := tsvValue(identity, "original_config_sha256"), hex.EncodeToString(originalDigest[:]); got != want {
		t.Fatalf("identity config digest = %q, want frozen original %q", got, want)
	}
	for _, tag := range []string{"000100", "000200"} {
		attestation := readFile(t, filepath.Join(prepared.runDir, "reports", tag+"-qps", "evidence", "product-executable.tsv"))
		if got := tsvValue(attestation, "source_config_sha256"); got != lines[0] {
			t.Fatalf("%s source config attestation = %q, want %q", tag, got, lines[0])
		}
	}
	err = filepath.Walk(prepared.runDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !info.Mode().IsRegular() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "runtime-config-never-retained") {
			return fmt.Errorf("runtime config plaintext leaked into retained artifact %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(snapshotParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private runtime config snapshot survived completion: %v", entries)
	}
}

func TestSingleNodeBaselineStopsWritersBeforeSealAndDetectsTampering(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	runDir, output, err := runFakeSingleNodeSealedBaseline(t, true)
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	result := readSingleNodeBaselineResult(t, runDir)
	if result.Outcome != "clean" || !result.ArtifactSealValid {
		t.Fatalf("sealed diagnostic result = %#v; output:\n%s", result, output)
	}
	if result.ReviewedContractSatisfied || result.AuthorizesThreeNodeDiagnostic {
		t.Fatalf("custom short run bypassed reviewed authorization: %#v", result)
	}

	finalLogPath := filepath.Join(runDir, "logs", "after", "node1.log")
	finalLog := readFile(t, finalLogPath)
	if !strings.Contains(finalLog, "term-log-flushed") {
		t.Fatalf("final sealed log missed the fake node's TERM flush:\n%s", finalLog)
	}
	manifest := readFile(t, filepath.Join(runDir, "checksums.sha256"))
	for _, required := range []string{
		"config/effective-wukongim.toml",
		"logs/after/node1.log",
		"artifact-identity.tsv",
		"bin/wukongim",
		"bin/wkbench",
	} {
		if !strings.Contains(manifest, "  "+required+"\n") {
			t.Fatalf("checksum manifest missing %q:\n%s", required, manifest)
		}
	}
	identity := readFile(t, filepath.Join(runDir, "artifact-identity.tsv"))
	if got := tsvValue(identity, "source_capture"); got != "binary_identity_only" {
		t.Fatalf("external binary source capture = %q, want binary_identity_only", got)
	}
	if got := tsvValue(identity, "wukongim_binary"); got != "bin/wukongim" {
		t.Fatalf("sealed wukongim identity path = %q", got)
	}
	if got := tsvValue(identity, "wkbench_binary"); got != "bin/wkbench" {
		t.Fatalf("sealed wkbench identity path = %q", got)
	}
	for _, key := range []string{"wukongim_binary_sha256", "wkbench_binary_sha256"} {
		value := tsvValue(identity, key)
		if len(value) != 64 {
			t.Fatalf("%s identity = %q, want sha256", key, value)
		}
	}
	if dataDir := tsvValue(identity, "canonical_data_dir"); !filepath.IsAbs(dataDir) {
		t.Fatalf("canonical data directory = %q, want absolute path", dataDir)
	}
	if device := tsvValue(identity, "data_filesystem_device"); device == "" || device == "unavailable" {
		t.Fatalf("data filesystem device = %q, want observed identity", device)
	}
	if blocks := tsvValue(identity, "data_filesystem_total_blocks"); blocks == "" || blocks == "0" {
		t.Fatalf("data filesystem blocks = %q, want observed geometry", blocks)
	}
	if blockSize := tsvValue(identity, "data_filesystem_block_size"); blockSize != "1024" {
		t.Fatalf("data filesystem block size = %q, want POSIX df -Pk 1024", blockSize)
	}
	if err := verifySingleNodeChecksumManifest(runDir); err != nil {
		t.Fatalf("fresh checksum manifest invalid: %v", err)
	}

	if strings.Contains(manifest, "  local-baseline.json\n") {
		t.Fatal("atomic completion marker must remain outside the immutable payload manifest")
	}
	originalLog := readFile(t, finalLogPath)
	if err := os.WriteFile(finalLogPath, []byte(originalLog+"tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySingleNodeChecksumManifest(runDir); err == nil {
		t.Fatal("tampered final node log still passed checksum verification")
	}
}

func TestSingleNodeBaselineExternalBinariesRemainAuditableAfterSourcesChange(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	runDir, _, externalWukongIM, externalWKBench, output, err := runFakeSingleNodeSealedBaselineWithBinaries(t, true)
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	sealedWukongIM := readFile(t, filepath.Join(runDir, "bin", "wukongim"))
	sealedWKBench := readFile(t, filepath.Join(runDir, "bin", "wkbench"))
	if err := os.WriteFile(externalWukongIM, []byte("mutated-external-wukongim\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalWKBench, []byte("mutated-external-wkbench\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(runDir, "bin", "wukongim")); got != sealedWukongIM {
		t.Fatal("external wukongim mutation changed sealed binary")
	}
	if got := readFile(t, filepath.Join(runDir, "bin", "wkbench")); got != sealedWKBench {
		t.Fatal("external wkbench mutation changed sealed binary")
	}
	if err := verifySingleNodeChecksumManifest(runDir); err != nil {
		t.Fatalf("sealed binary copies no longer audit after external mutation: %v", err)
	}
}

func TestSingleNodeBaselineExternalBinariesStayBinaryIdentityOnlyForCleanSource(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	cleanRepo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", cleanRepo},
		{"-C", cleanRepo, "config", "user.name", "wkbench-test"},
		{"-C", cleanRepo, "config", "user.email", "wkbench-test@example.invalid"},
		{"-C", cleanRepo, "commit", "--allow-empty", "-q", "-m", "clean"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, output)
		}
	}
	runDir, _, _, _, output, err := runFakeSingleNodeSealedBaselineWithConfigEnv(t, true,
		"# fake effective single-node cluster config\n",
		[]string{"GIT_DIR=" + filepath.Join(cleanRepo, ".git"), "GIT_WORK_TREE=" + cleanRepo})
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	identity := readFile(t, filepath.Join(runDir, "artifact-identity.tsv"))
	if got := tsvValue(identity, "source_dirty"); got != "false" {
		t.Fatalf("clean source dirty identity = %q", got)
	}
	if got := tsvValue(identity, "source_rebuildable_from_revision"); got != "false" {
		t.Fatalf("external binaries claimed source rebuildability: %q", got)
	}
	if got := tsvValue(identity, "source_capture"); got != "binary_identity_only" {
		t.Fatalf("external binaries source capture = %q", got)
	}
}

func TestSingleNodeBaselineGitObservationFailureFailsSourceIdentityClosed(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	runDir, _, _, _, output, err := runFakeSingleNodeSealedBaselineWithConfigEnv(t, true,
		"# fake effective single-node cluster config\n",
		[]string{"GIT_DIR=" + filepath.Join(t.TempDir(), "missing.git")})
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	identity := readFile(t, filepath.Join(runDir, "artifact-identity.tsv"))
	if got := tsvValue(identity, "source_revision"); got != "unknown" {
		t.Fatalf("failed Git revision = %q, want unknown", got)
	}
	if got := tsvValue(identity, "source_dirty"); got != "true" {
		t.Fatalf("failed Git dirty state = %q, want true", got)
	}
	if got := tsvValue(identity, "source_rebuildable_from_revision"); got != "false" {
		t.Fatalf("failed Git source rebuildability = %q, want false", got)
	}
}

func TestSingleNodeBaselineMissingOrTamperedSealedBinaryFailsClosed(t *testing.T) {
	for _, name := range []string{"missing", "tampered"} {
		t.Run(name, func(t *testing.T) {
			runHeavyShellScriptTestInParallel(t)
			runDir, _, _, _, output, err := runFakeSingleNodeSealedBaselineWithBinaries(t, true)
			if err != nil {
				t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
			}
			path := filepath.Join(runDir, "bin", "wukongim")
			if name == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("tampered\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := verifySingleNodeChecksumManifest(runDir); err == nil {
				t.Fatalf("%s sealed binary passed verification", name)
			}
		})
	}
}

func TestSingleNodeBaselineStoragePreflightResultIsSealedWithoutStartingProcesses(t *testing.T) {
	root := repoRoot(t)
	runDir := t.TempDir()
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(config, []byte("# preflight config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh", "--no-start", "--qps", "250", "--out-dir", runDir)
	command.Dir = root
	command.Env = append(envWithout("WK_BENCH_MINIMUM_FREE_PERCENT", "WK_WUKONGIM_SINGLE_NODE_CONFIG", "WK_WUKONGIM_SINGLE_NODE_DATA_DIR"),
		"WK_BENCH_MINIMUM_FREE_PERCENT=100", "WK_WUKONGIM_SINGLE_NODE_CONFIG="+config,
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR="+filepath.Join(t.TempDir(), "node-data"))
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("storage preflight exit = %v, want 2\n%s", err, output)
	}
	result := readSingleNodeBaselineResult(t, runDir)
	if result.Outcome != "storage_confounded" || !result.ArtifactSealValid {
		t.Fatalf("sealed storage preflight = %#v\n%s", result, output)
	}
	if err := verifySingleNodeChecksumManifest(runDir); err != nil {
		t.Fatalf("storage preflight seal invalid: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(runDir, "bin", "wukongim")); !os.IsNotExist(err) {
		t.Fatalf("storage preflight unexpectedly built wukongim: %v", err)
	}
	for _, unexpected := range []string{"cluster-start.log", "logs", "worker-state"} {
		if _, err := os.Stat(filepath.Join(runDir, unexpected)); !os.IsNotExist(err) {
			t.Fatalf("storage preflight unexpectedly started runtime evidence %s: %v", unexpected, err)
		}
	}
}

func TestSingleNodeBaselineOwnedWorkerBuildDoesNotReuseOrOverwriteExistingBinary(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	runDir := t.TempDir()
	binDir := t.TempDir()
	callsDir := t.TempDir()
	staleBinary := filepath.Join(t.TempDir(), "wkbench")
	if err := os.WriteFile(staleBinary, []byte("#!/usr/bin/env bash\necho stale-worker-must-not-run >&2\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(config, []byte("[bench]\napi_token = \"source-token-canary\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "` + callsDir + `/go.calls"
exec "` + realGo + `" "$@"
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
		"--no-start", "--qps", "250", "--out-dir", runDir, "--wkbench-bin", staleBinary)
	command.Dir = root
	command.Env = append(envWithout("WK_WUKONGIM_SINGLE_NODE_CONFIG", "WK_BENCH_MINIMUM_FREE_PERCENT", "WK_WUKONGIM_SINGLE_NODE_DATA_DIR"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_WUKONGIM_SINGLE_NODE_CONFIG="+config,
		"WK_BENCH_MINIMUM_FREE_PERCENT=100",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR="+filepath.Join(t.TempDir(), "node-data"),
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("storage preflight exit = %v, want 2\n%s", err, output)
	}
	if calls := readFile(t, filepath.Join(callsDir, "go.calls")); !strings.Contains(calls, "build -o ") || !strings.Contains(calls, " ./cmd/wkbench") {
		t.Fatalf("owned worker did not rebuild from current source:\n%s", calls)
	}
	if stale := readFile(t, staleBinary); !strings.Contains(stale, "stale-worker-must-not-run") {
		t.Fatalf("caller-provided existing binary was overwritten:\n%s", stale)
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".wkbench-build.") {
			t.Fatalf("temporary owned worker build survived preflight seal: %s", entry.Name())
		}
	}
	if err := verifySingleNodeChecksumManifest(runDir); err != nil {
		t.Fatalf("preflight manifest invalid: %v\n%s", err, output)
	}
}

func TestSingleNodeBaselineRedactionFailureRemovesUnsealedBuildBeforeChecksum(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	runDir := t.TempDir()
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(config, []byte("[unknown]\nsecret = \"must-not-leak\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "scripts/bench-wukongim-single-node-1000ch.sh",
		"--no-start", "--qps", "250", "--out-dir", runDir)
	command.Dir = root
	command.Env = append(envWithout("WK_WUKONGIM_SINGLE_NODE_CONFIG", "WK_BENCH_MINIMUM_FREE_PERCENT", "WK_WUKONGIM_SINGLE_NODE_DATA_DIR"),
		"WK_WUKONGIM_SINGLE_NODE_CONFIG="+config,
		"WK_BENCH_MINIMUM_FREE_PERCENT=100",
		"GOWORK=off",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR="+filepath.Join(t.TempDir(), "node-data"),
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 6 {
		t.Fatalf("redaction failure exit = %v, want 6\n%s", err, output)
	}
	result := readSingleNodeBaselineResult(t, runDir)
	if result.Outcome != "insufficient_evidence" || result.Reason != "artifact_seal_verification_failed" {
		t.Fatalf("redaction failure result = %#v\n%s", result, output)
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".wkbench-build.") {
			t.Fatalf("unsealed worker build survived redaction failure: %s", entry.Name())
		}
	}
	if _, err := verifySingleNodeChecksumEntries(runDir); err != nil {
		t.Fatalf("reconstructable failure checksum invalid: %v\n%s", err, output)
	}
	err = filepath.Walk(runDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || !info.Mode().IsRegular() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "must-not-leak") {
			return fmt.Errorf("redaction failure leaked source secret to %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSingleNodeBaselineDefaultProfileDoesNotCreatePprofArtifacts(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	runDir, output, err := runFakeSingleNodeSealedBaseline(t, true)
	if err != nil {
		t.Fatalf("single-node diagnostic failed: %v\n%s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(runDir, "pprof")); !os.IsNotExist(statErr) {
		t.Fatalf("PROFILE_SECONDS=0 created pprof artifacts: %v", statErr)
	}
}

func TestSingleNodeBaselineCrashBeforeAtomicCompletionDoesNotPublishResult(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	ready := filepath.Join(t.TempDir(), "completion.ready")
	release := filepath.Join(t.TempDir(), "completion.release")
	prepared := prepareFakeSingleNodeSealedBaseline(t, true, "# fake effective single-node cluster config\n", nil)
	writeBlockingSingleNodePublishWkbench(t, prepared.wkbenchBin, ready, release)
	prepared.command.Env = append(prepared.command.Env,
		"WK_TEST_COMPLETION_MARKER_PATH="+filepath.Join(prepared.runDir, "local-baseline.json"))
	var output bytes.Buffer
	prepared.command.Stdout = &output
	prepared.command.Stderr = &output
	if err := prepared.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.command.Process.Kill() })
	waitForChatLifecycleFile(t, ready, 20*time.Second)
	if _, err := os.Lstat(filepath.Join(prepared.runDir, "local-baseline.json")); !os.IsNotExist(err) {
		t.Fatalf("completion marker was visible before its atomic rename: %v", err)
	}
	seen, err := verifySingleNodeChecksumEntries(prepared.runDir)
	if err != nil {
		t.Fatalf("pre-publication manifest is invalid: %v\n%s", err, output.String())
	}
	if seen["local-baseline.json"] {
		t.Fatal("the referenced artifact manifest contains its later atomic completion marker")
	}
	if !seen["reports/local-baseline-authorization.json"] {
		t.Fatal("pre-publication manifest omits the typed authorization")
	}
	if err := prepared.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = prepared.command.Wait()
	if _, err := os.Lstat(filepath.Join(prepared.runDir, "local-baseline.json")); !os.IsNotExist(err) {
		t.Fatalf("crashed run left a consumable completion marker: %v", err)
	}
}

func writeBlockingSingleNodePublishWkbench(t *testing.T, path, ready, release string) {
	t.Helper()
	blocked := filepath.Join(filepath.Dir(path), "wkbench-original")
	if err := os.Rename(path, blocked); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == report && "${2:-}" == local-single-node-publish ]]; then
  : > "` + ready + `"
  for ((attempt = 0; attempt < 2000; attempt++)); do
    [[ -f "` + release + `" ]] && break
    sleep 0.01
  done
  [[ -f "` + release + `" ]] || exit 75
fi
exec "` + blocked + `" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestSingleNodeBaselineRedactsSecretsAndUsesPrivateModes(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	const joinToken = "single-node-join-token-canary"
	const jwtSecret = "single-node-jwt-secret-canary"
	const password = "single-node-password-canary"
	const apiToken = "single-node-api-token-canary"
	config := `[cluster]
join_token = "` + joinToken + `"
[manager]
jwt_secret = "` + jwtSecret + `"
users = [
  { username = "admin", password = "` + password + `" },
]
[bench]
api_token = "` + apiToken + `"
`
	prepared := prepareFakeSingleNodeSealedBaseline(t, true, config, nil)
	output, err := prepared.command.CombinedOutput()
	runDir, configPath := prepared.runDir, prepared.configPath
	if err != nil {
		calls, _ := os.ReadFile(filepath.Join(prepared.callsDir, "wkbench.calls"))
		var artifacts []string
		_ = filepath.Walk(runDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr == nil && info.Mode().IsRegular() {
				relative, _ := filepath.Rel(runDir, path)
				artifacts = append(artifacts, relative)
			}
			return nil
		})
		t.Fatalf("single-node diagnostic failed: %v\n%s\nwkbench calls:\n%s\nartifacts:\n%s", err, output, calls, strings.Join(artifacts, "\n"))
	}
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(originalConfig)
	identity := readFile(t, filepath.Join(runDir, "artifact-identity.tsv"))
	if got, want := tsvValue(identity, "original_config_sha256"), hex.EncodeToString(originalDigest[:]); got != want {
		t.Fatalf("original config identity = %q, want %q", got, want)
	}
	redacted := readFile(t, filepath.Join(runDir, "config", "effective-wukongim.toml"))
	if !strings.Contains(redacted, "******") || !strings.Contains(redacted, "users = []") {
		t.Fatalf("effective config was not usefully redacted:\n%s", redacted)
	}
	for _, want := range []string{
		"[local_single_node_runtime]",
		"topology_environment_overrides_rejected = true",
		"initial_slot_count = 12",
		"hash_slot_count = 256",
		"slot_replica_n = 1",
		"channel_replica_n = 1",
		`commit_coordinator_flush_window = "200us"`,
		"commit_coordinator_shards = 1",
		"commit_coordinator_sync = true",
	} {
		if !strings.Contains(redacted, want) {
			t.Fatalf("effective config omitted resolved runtime value %q:\n%s", want, redacted)
		}
	}
	canaries := []string{joinToken, jwtSecret, password, apiToken}
	err = filepath.Walk(runDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, canary := range canaries {
			if strings.Contains(string(data), canary) {
				return fmt.Errorf("secret canary leaked into %s", path)
			}
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("artifact %s has group/other permissions %04o", path, info.Mode().Perm())
		}
		relative, _ := filepath.Rel(runDir, path)
		if !strings.HasPrefix(relative, "bin"+string(os.PathSeparator)) && info.Mode().Perm()&0o177 != 0 {
			return fmt.Errorf("non-binary artifact %s is wider than 0600: %04o", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type singleNodeBaselineResult struct {
	Outcome                       string `json:"outcome"`
	Reason                        string `json:"reason"`
	ReviewedContractSatisfied     bool   `json:"reviewed_contract_satisfied"`
	AuthorizesThreeNodeDiagnostic bool   `json:"authorizes_three_node_diagnostic"`
	ArtifactSealValid             bool   `json:"artifact_seal_valid"`
}

func readSingleNodeBaselineResult(t *testing.T, runDir string) singleNodeBaselineResult {
	t.Helper()
	var result singleNodeBaselineResult
	data := []byte(readFile(t, filepath.Join(runDir, "local-baseline.json")))
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode local-baseline.json: %v\n%s", err, data)
	}
	return result
}

func runFakeSingleNodeSealedBaseline(t *testing.T, startCluster bool) (string, []byte, error) {
	runDir, _, _, _, output, err := runFakeSingleNodeSealedBaselineWithBinaries(t, startCluster)
	return runDir, output, err
}

func runFakeSingleNodeSealedBaselineWithBinaries(t *testing.T, startCluster bool) (string, string, string, string, []byte, error) {
	return runFakeSingleNodeSealedBaselineWithConfig(t, startCluster, "# fake effective single-node cluster config\n")
}

func runFakeSingleNodeSealedBaselineWithConfig(t *testing.T, startCluster bool, config string) (string, string, string, string, []byte, error) {
	return runFakeSingleNodeSealedBaselineWithConfigEnv(t, startCluster, config, nil)
}

func runFakeSingleNodeSealedBaselineWithConfigEnv(t *testing.T, startCluster bool, config string, extraEnv []string) (string, string, string, string, []byte, error) {
	prepared := prepareFakeSingleNodeSealedBaseline(t, startCluster, config, extraEnv)
	output, err := prepared.command.CombinedOutput()
	return prepared.runDir, prepared.configPath, prepared.wukongimBin, prepared.wkbenchBin, output, err
}

type preparedFakeSingleNodeBaseline struct {
	runDir      string
	callsDir    string
	configPath  string
	wukongimBin string
	wkbenchBin  string
	startScript string
	command     *exec.Cmd
}

func prepareFakeSingleNodeSealedBaseline(t *testing.T, startCluster bool, config string, extraEnv []string) preparedFakeSingleNodeBaseline {
	t.Helper()
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	runDir := t.TempDir()
	dataDir := t.TempDir()
	logDir := t.TempDir()
	wukongimBin := filepath.Join(t.TempDir(), "wukongim")
	if err := os.WriteFile(wukongimBin, []byte("fake-external-wukongim-source\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	wkbenchBin := filepath.Join(binDir, "wkbench")
	writeFakeThreeNode1000Wkbench(t, wkbenchBin, callsDir, "sealed-single")
	writeFakeSingleNodeCompletionMove(t, filepath.Join(binDir, "mv"))
	writeFakeThreeNode1000Curl(t, filepath.Join(binDir, "curl"), callsDir)
	writeFakeActivatePgrep(t, filepath.Join(binDir, "pgrep"), callsDir)
	writeFakeActivatePS(t, filepath.Join(binDir, "ps"), callsDir)
	startScript := filepath.Join(t.TempDir(), "fake-single-node-start.sh")
	writeFakeSingleNodeSealStart(t, startScript, wukongimBin, filepath.Join(callsDir, "cluster-generations.log"), "", "")
	gatewayAddr := listenLocalTCP(t)

	args := []string{
		"scripts/bench-wukongim-single-node-1000ch.sh",
		"--no-worker",
		"--out-dir", runDir,
		"--wkbench-bin", wkbenchBin,
		"--start-script", startScript,
		"--qps", "100",
		"--channels", "10",
		"--users", "20",
		"--members", "2",
		"--duration", "1s",
		"--warmup", "0s",
		"--cooldown", "0s",
		"--resource-interval", "0",
		"--api", "http://127.0.0.1:5011",
		"--metrics", "http://127.0.0.1:5011",
		"--gateway", gatewayAddr,
	}
	if !startCluster {
		args = append(args[:1], append([]string{"--no-start"}, args[1:]...)...)
	}
	command := exec.Command("bash", args...)
	command.Dir = root
	command.Env = append(envWithout(
		"WK_WUKONGIM_SINGLE_NODE_CONFIG",
		"WK_WUKONGIM_SINGLE_NODE_BIN",
		"WK_WUKONGIM_SINGLE_NODE_LOG_DIR",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR",
		"WK_BENCH_MINIMUM_FREE_PERCENT",
		"WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS",
		"WK_FAKE_LOCAL_STORAGE_EVIDENCE",
		"WK_FAKE_WUKONGIM_SOURCE_BIN",
		"WK_FAKE_WKBENCH_SUCCESS_TOTAL",
		"WK_FAKE_WKBENCH_CONNECT_SUCCESS",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_WUKONGIM_SINGLE_NODE_CONFIG="+configPath,
		"WK_WUKONGIM_SINGLE_NODE_BIN="+wukongimBin,
		"WK_WUKONGIM_SINGLE_NODE_LOG_DIR="+logDir,
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR="+dataDir,
		"WK_FAKE_WUKONGIM_SOURCE_BIN="+wukongimBin,
		"WK_BENCH_MINIMUM_FREE_PERCENT=1",
		"WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS=15",
		"WK_FAKE_LOCAL_STORAGE_EVIDENCE=1",
		"WK_FAKE_WKBENCH_SUCCESS_TOTAL=100",
		"WK_FAKE_WKBENCH_CONNECT_SUCCESS=20",
		"WK_FAKE_SINGLE_NODE_TERMINAL_CUT=1",
	)
	command.Env = append(command.Env, extraEnv...)
	return preparedFakeSingleNodeBaseline{
		runDir: runDir, callsDir: callsDir, configPath: configPath, wukongimBin: wukongimBin, wkbenchBin: wkbenchBin,
		startScript: startScript,
		command:     command,
	}
}

func writeFakeSingleNodeCompletionMove(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
destination="${@: -1}"
if [[ -n "${WK_TEST_COMPLETION_MARKER_READY:-}" && "$destination" == "${WK_TEST_COMPLETION_MARKER_PATH:-}" ]]; then
  : > "$WK_TEST_COMPLETION_MARKER_READY"
  for ((attempt = 0; attempt < 2000; attempt++)); do
    [[ -f "${WK_TEST_COMPLETION_MARKER_RELEASE:-}" ]] && break
    sleep 0.01
  done
  [[ -f "${WK_TEST_COMPLETION_MARKER_RELEASE:-}" ]] || exit 75
fi
exec /bin/mv "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFakeSingleNodeSealStart(t *testing.T, path string, sourceBinary string, generationCalls string, observedConfigDigests string, externalConfig string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "--dry-run" ]]; then
  printf 'fake_single_node_plan=true\n'
  exit 0
fi
: "${WK_WUKONGIM_SINGLE_NODE_DATA_DIR:?}"
: "${WK_WUKONGIM_SINGLE_NODE_LOG_DIR:?}"
: "${WK_WUKONGIM_SINGLE_NODE_BIN:?}"
clean=false
for argument in "$@"; do
  [[ "$argument" == "--clean" ]] && clean=true
done
if [[ "$clean" == true ]]; then
  rm -rf "$WK_WUKONGIM_SINGLE_NODE_DATA_DIR" "$WK_WUKONGIM_SINGLE_NODE_LOG_DIR"
fi
mkdir -p "$WK_WUKONGIM_SINGLE_NODE_DATA_DIR" "$WK_WUKONGIM_SINGLE_NODE_LOG_DIR" "$(dirname "$WK_WUKONGIM_SINGLE_NODE_BIN")"
durable_before=false
[[ -f "$WK_WUKONGIM_SINGLE_NODE_DATA_DIR/durable-marker" ]] && durable_before=true
: > "$WK_WUKONGIM_SINGLE_NODE_DATA_DIR/durable-marker"
printf 'args=%s durable_before=%s\n' "$*" "$durable_before" >> ` + fmt.Sprintf("%q", generationCalls) + `
if [[ -n ` + fmt.Sprintf("%q", observedConfigDigests) + ` ]]; then
  shasum -a 256 "$WK_WUKONGIM_SINGLE_NODE_CONFIG" | awk '{print $1}' >> ` + fmt.Sprintf("%q", observedConfigDigests) + `
  if [[ "$durable_before" == false ]]; then
    printf 'snapshot-generation=mutated\n' > ` + fmt.Sprintf("%q", externalConfig) + `
  fi
fi
cp ` + fmt.Sprintf("%q", sourceBinary) + ` "$WK_WUKONGIM_SINGLE_NODE_BIN"
chmod 0755 "$WK_WUKONGIM_SINGLE_NODE_BIN"
node_log="$WK_WUKONGIM_SINGLE_NODE_LOG_DIR/node1.log"
printf 'runtime-started\n' >"$node_log"
printf '[fake-single] node pid=%s\n' "$$"
flush_and_exit() {
  printf 'term-log-flushed\n' >>"$node_log"
  exit 0
}
trap flush_and_exit TERM INT
while true; do sleep 0.05; done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func verifySingleNodeChecksumManifest(runDir string) error {
	seen, err := verifySingleNodeChecksumEntries(runDir)
	if err != nil {
		return err
	}
	requiredPaths := []string{
		"config/effective-wukongim.toml",
		"artifact-identity.tsv",
	}
	if seen["local-baseline.json"] {
		return fmt.Errorf("manifest must exclude the later atomic completion marker")
	}
	identity, err := os.ReadFile(filepath.Join(runDir, "artifact-identity.tsv"))
	if err != nil {
		return err
	}
	scope := tsvValue(string(identity), "seal_scope")
	if scope == "measured" {
		requiredPaths = append(requiredPaths, "logs/after/node1.log", "bin/wukongim", "bin/wkbench")
	} else if scope != "preflight" {
		return fmt.Errorf("unsupported seal scope %q", scope)
	}
	for _, required := range requiredPaths {
		if !seen[required] {
			return fmt.Errorf("manifest omits %s", required)
		}
	}
	return nil
}

func verifySingleNodeChecksumEntries(runDir string) (map[string]bool, error) {
	manifest, err := os.ReadFile(filepath.Join(runDir, "checksums.sha256"))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || parts[1] == "" {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		expected, err := hex.DecodeString(parts[0])
		if err != nil {
			return nil, fmt.Errorf("decode checksum for %s: %w", parts[1], err)
		}
		if filepath.IsAbs(parts[1]) || strings.HasPrefix(parts[1], "../") || strings.Contains(parts[1], "/../") {
			return nil, fmt.Errorf("unsafe checksum path %q", parts[1])
		}
		data, err := os.ReadFile(filepath.Join(runDir, filepath.FromSlash(parts[1])))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", parts[1], err)
		}
		actual := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(actual[:]), hex.EncodeToString(expected)) {
			return nil, fmt.Errorf("checksum mismatch for %s", parts[1])
		}
		seen[parts[1]] = true
	}
	return seen, nil
}

func tsvValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[0] == key {
			return parts[1]
		}
	}
	return ""
}
