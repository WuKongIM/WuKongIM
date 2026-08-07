package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleLocalStateCreatesPrivateIdentityAndDeletesOnlyAfterZeroProof(t *testing.T) {
	root := repoRoot(t)
	stateRoot := filepath.Join(t.TempDir(), "chat-lifecycle")
	script := filepath.Join(root, "scripts", "chat-lifecycle", "local-request-state.sh")
	requestID := "chat-20300102T030405Z-0123abcd"
	sourceSHA := strings.Repeat("a", 40)

	initCommand := exec.Command("bash", script, "init", requestID, sourceSHA)
	initCommand.Dir = root
	initCommand.Env = append(os.Environ(), "WK_CHAT_LIFECYCLE_STATE_ROOT="+stateRoot)
	output, err := initCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	var initialized struct {
		RequestID   string `json:"request_id"`
		StateDir    string `json:"state_dir"`
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(output, &initialized); err != nil {
		t.Fatal(err)
	}
	resolvedStateRoot, err := filepath.EvalSymlinks(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	requestDir := filepath.Join(resolvedStateRoot, requestID)
	if initialized.RequestID != requestID || initialized.StateDir != requestDir ||
		!strings.HasPrefix(initialized.PublicKey, "ssh-ed25519 ") || !strings.HasPrefix(initialized.Fingerprint, "SHA256:") {
		t.Fatalf("initialized state = %+v", initialized)
	}
	for path, mode := range map[string]os.FileMode{
		resolvedStateRoot: 0o700,
		requestDir:        0o700,
		filepath.Join(requestDir, "diagnostic_ed25519"): 0o600,
		filepath.Join(requestDir, "state.json"):         0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), mode)
		}
	}

	badProof := filepath.Join(t.TempDir(), "bad-zero.json")
	if err := os.WriteFile(badProof, []byte(`{"schema":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup := exec.Command("bash", script, "cleanup", requestID, badProof)
	cleanup.Dir = root
	cleanup.Env = append(os.Environ(), "WK_CHAT_LIFECYCLE_STATE_ROOT="+stateRoot)
	if err := cleanup.Run(); err == nil {
		t.Fatal("local state cleanup accepted an invalid zero-inventory proof")
	}
	if _, err := os.Stat(filepath.Join(requestDir, "diagnostic_ed25519")); err != nil {
		t.Fatalf("rejected cleanup removed diagnostic identity: %v", err)
	}

	zeroProof := filepath.Join(t.TempDir(), "zero-inventory.json")
	proof := `{"schema":"wukongim.cloud_lease.release/v1","result":{"zero_inventory":{"selector":{"request_id":"` + requestID +
		`"},"account_id_hash":"sha256:` + strings.Repeat("b", 64) + `","observed_at":"2030-01-02T03:04:05Z","scopes":["instances","disks","eips"]}}}`
	if err := os.WriteFile(zeroProof, []byte(proof), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup = exec.Command("bash", script, "cleanup", requestID, zeroProof)
	cleanup.Dir = root
	cleanup.Env = append(os.Environ(), "WK_CHAT_LIFECYCLE_STATE_ROOT="+stateRoot)
	if output, err := cleanup.CombinedOutput(); err != nil {
		t.Fatalf("cleanup local state: %v\n%s", err, output)
	}
	if _, err := os.Stat(requestDir); !os.IsNotExist(err) {
		t.Fatalf("request state still exists after zero proof: %v", err)
	}
	if _, err := os.Stat(resolvedStateRoot); err != nil {
		t.Fatalf("cleanup removed the shared state root: %v", err)
	}
}
