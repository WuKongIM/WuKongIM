//go:build integration

package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The fixture launches the real collector with fake SSH, never a product or cloud process.
func TestChatLifecycleTerminalEvidenceRetention(t *testing.T) {
	root := repoRoot(t)
	const artifactEnv = "CHAT_LIFECYCLE_TERMINAL_EVIDENCE_TEST_ARTIFACT"
	artifact := os.Getenv(artifactEnv)
	if artifact == "" {
		artifact = filepath.Join(t.TempDir(), "terminal-evidence-results.json")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", filepath.Join(root, "scripts", "testdata", "chat_lifecycle_terminal_evidence_test.py"))
	command.Dir = root
	command.Env = append(os.Environ(), artifactEnv+"="+artifact)
	if output, err := runPreClockFixture(command); err != nil {
		t.Errorf("terminal evidence fixture: %v\n%s", err, output)
	}
	var result struct {
		TestsRun     int  `json:"tests_run"`
		FailureCount int  `json:"failure_count"`
		ErrorCount   int  `json:"error_count"`
		FixtureOnly  bool `json:"fixture_only"`
	}
	body, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("fixture did not write its artifact through the non-product environment variable: %v", err)
	}
	if err := json.Unmarshal(body, &result); err != nil || result.TestsRun == 0 || !result.FixtureOnly || result.FailureCount != 0 || result.ErrorCount != 0 {
		t.Fatalf("invalid fixture artifact: %v / %+v", err, result)
	}
}

func TestChatLifecycleTerminalEvidenceCollectorRequiresTerminalAnchor(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	output := filepath.Join(directory, "evidence")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"timeout": "#!/bin/sh\nshift\nexec \"$@\"\n",
		"ssh": `#!/bin/sh
case "$*" in
  *api/v1/targets*) printf '{"status":"success","data":{"activeTargets":[]}}\n' ;;
  *api/v1/query_range*) printf '{"status":"success","data":{"result":[]}}\n' ;;
  *) printf 'bounded terminal evidence\n' ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host *\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-terminal-evidence.sh"), "rehearsal", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CLOUD_SSH_CONFIG="+sshConfig,
		"WK_CLOUD_SERVICE1_IP=10.0.0.1", "WK_CLOUD_SERVICE2_IP=10.0.0.2", "WK_CLOUD_SERVICE3_IP=10.0.0.3")
	if combined, err := command.CombinedOutput(); err == nil {
		t.Fatalf("terminal evidence collector accepted missing terminal anchor\n%s", combined)
	}
	var manifest struct {
		Schema          string `json:"schema"`
		Complete        bool   `json:"complete"`
		CaptureFailures int    `json:"capture_failures"`
		Captures        []struct {
			State string `json:"state"`
			Bytes int64  `json:"bytes"`
		} `json:"captures"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(output, "manifest.json"))), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "wukongim.chat_lifecycle.terminal_evidence/v1" || manifest.Complete || manifest.CaptureFailures != 1 || len(manifest.Captures) != 16 {
		t.Fatalf("terminal evidence manifest = %#v", manifest)
	}
	for _, capture := range manifest.Captures[:15] {
		if capture.State != "collected" || capture.Bytes <= 0 || capture.Bytes > 4<<20 {
			t.Fatalf("terminal evidence capture = %#v", capture)
		}
	}

	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nprintf 'not-prometheus-json\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	invalidOutput := filepath.Join(directory, "invalid-evidence")
	invalidCommand := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-terminal-evidence.sh"), "formal", invalidOutput)
	invalidCommand.Dir = root
	invalidCommand.Env = command.Env
	if combined, err := invalidCommand.CombinedOutput(); err == nil {
		t.Fatalf("terminal evidence collector accepted malformed Prometheus responses\n%s", combined)
	}
	var invalidManifest struct {
		Complete        bool `json:"complete"`
		CaptureFailures int  `json:"capture_failures"`
		Captures        []struct {
			State string `json:"state"`
		} `json:"captures"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(invalidOutput, "manifest.json"))), &invalidManifest); err != nil {
		t.Fatal(err)
	}
	if invalidManifest.Complete || invalidManifest.CaptureFailures != 2 {
		t.Fatalf("malformed Prometheus manifest = %#v", invalidManifest)
	}
}
