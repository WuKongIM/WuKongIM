package scripts_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This public reconstruction matches the pre-kill unit digest from run 33.
// It deliberately has no final newline, as captured by the stage shell.
const originalActiveEmptyJob = "Result=success\nExecMainCode=0\nExecMainStatus=0\nLoadState=loaded\nActiveState=active\nSubState=running\nJob="
const originalActiveEmptyJobSHA = "57c3e320a832417c5e803553961f261771af23813115c207218162f214488524"

func TestChatLifecycleStageServiceStateContract(t *testing.T) {
	root := repoRoot(t)
	classifier := filepath.Join(root, "scripts/chat-lifecycle/classify-stage-service-state.sh")
	diagnostic := filepath.Join(root, "scripts/chat-lifecycle/pre-clock-diagnostic.py")
	for _, tc := range []struct {
		name, input, want string
		job               float64
	}{
		{"active empty job", "ActiveState=active\nJob=\n", "running\n", 0},
		{"activating empty job", "ActiveState=activating\nJob=\n", "running\n", 0},
		{"reloading zero job", "ActiveState=reloading\nJob=0\n", "running\n", 0},
		{"failed empty job", "ActiveState=failed\nJob=\n", "terminal\n", 0},
		{"deactivating queued job", "ActiveState=deactivating\nJob=88\n", "terminal\n", 88},
		{"inactive empty job", "ActiveState=inactive\nJob=\n", "terminal\n", 0},
		{"inactive zero", "ActiveState=inactive\nJob=0\n", "terminal\n", 0},
		{"inactive queued", "ActiveState=inactive\nJob=8841\n", "pending\n", 8841},
		{"uint32 max", "ActiveState=inactive\nJob=4294967295\n", "pending\n", 4294967295},
		{"decimal zero", "ActiveState=inactive\nJob=00\n", "terminal\n", 0},
		{"decimal not octal", "ActiveState=inactive\nJob=0000000088\n", "pending\n", 88},
		{"unterminated job", "ActiveState=inactive\nJob=0", "terminal\n", 0},
		{"reverse unterminated state", "Job=\nActiveState=active", "running\n", 0},
		{"missing job", "ActiveState=active\n", "", 0},
		{"missing active state", "Job=0\n", "", 0},
		{"empty active state", "ActiveState=\nJob=0\n", "", 0},
		{"duplicate active state", "ActiveState=active\nActiveState=active\n", "", 0},
		{"duplicate empty job", "ActiveState=active\nJob=\nJob=0\n", "", 0},
		{"secret nondecimal job", "ActiveState=active\nJob=secret-canary-do-not-publish\n", "", 0},
		{"negative job", "ActiveState=active\nJob=-1\n", "", 0},
		{"whitespace job", "ActiveState=active\nJob= 0\n", "", 0},
		{"uint32 overflow", "ActiveState=active\nJob=4294967296\n", "", 0},
		{"too many digits", "ActiveState=active\nJob=00000000000\n", "", 0},
		{"unknown field", "ActiveState=active\nUnexpected=0\n", "", 0},
		{"unterminated extra field", "ActiveState=active\nJob=0\nUnexpected=0", "", 0},
		{"blank extra line", "ActiveState=active\nJob=0\n\n", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command("bash", classifier)
			command.Stdin = strings.NewReader(tc.input)
			out, err := command.CombinedOutput()
			if tc.want != "" {
				if err != nil || string(out) != tc.want {
					t.Errorf("classification = %q, %v; want %q", out, err, tc.want)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 2 || len(out) != 0 {
					t.Errorf("invalid input must exit 2 without output: %q / %v", out, err)
				}
			}

			input := "LoadState=loaded\nSubState=running\nResult=success\nExecMainCode=0\nExecMainStatus=0\n" + tc.input
			command = exec.Command("python3", "-B", diagnostic, "state")
			command.Stdin = strings.NewReader(input)
			out, err = command.CombinedOutput()
			if err != nil {
				t.Fatalf("diagnostic: %v / %s", err, out)
			}
			var got struct {
				Valid      bool              `json:"valid"`
				Bytes      int               `json:"bytes"`
				SHA        string            `json:"sha256"`
				Properties map[string]any    `json:"properties"`
				FieldSHA   map[string]string `json:"field_sha256"`
				Missing    []string          `json:"missing_fields"`
				Invalid    []string          `json:"invalid_fields"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if got.Valid != (tc.want != "") || got.Bytes != len(input) || got.SHA != fmt.Sprintf("%x", sha256.Sum256([]byte(input))) {
				t.Errorf("wrong bounded diagnostic: %s", out)
			}
			if got.Valid && got.Properties["Job"] != tc.job {
				t.Errorf("Job must be typed decimal %v: %s", tc.job, out)
			}
			if strings.Contains(string(out), "secret-canary-do-not-publish") {
				t.Fatal("raw value escaped diagnostic boundary")
			}
			if strings.Contains(tc.input, "Job=\n") && got.FieldSHA["Job"] != fmt.Sprintf("%x", sha256.Sum256(nil)) {
				t.Errorf("empty Job raw digest changed: %s", out)
			}
			if tc.name == "missing job" && strings.Join(got.Missing, ",") != "Job" {
				t.Errorf("missing Job was confused with explicit empty Job: %s", out)
			}
			if tc.name == "duplicate empty job" && (strings.Join(got.Invalid, ",") != "Job" || got.Properties["Job"] != nil) {
				t.Errorf("duplicate empty Job must remain invalid: %s", out)
			}
		})
	}
}
