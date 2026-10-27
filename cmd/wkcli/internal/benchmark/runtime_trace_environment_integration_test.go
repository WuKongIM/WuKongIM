//go:build integration

package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This child runs the existing real worker CLI composition, not an alternate
// env parser. The original source compiles and goes behavior RED when it
// ignores the new explicit invalid/on opt-in.
func TestWorkerRuntimeTraceCLIHelper(t *testing.T) {
	if os.Getenv("WK_R28_RUNTIME_CLI_CHILD") != "1" {
		return
	}
	cfg := defaultWorkerCLIConfig()
	cfg.mode, cfg.listen = workerModeChatLifecycle, os.Getenv("WK_R28_LISTEN")
	cfg.server.ControlToken, cfg.server.WorkDir = "r28-control", os.Getenv("WK_R28_WORK_DIR")
	os.Exit(runWorkerConfig(cfg, os.Stdout))
}

func TestWorkerRuntimeTraceCLIEnvironmentStrictAndDefaultOff(t *testing.T) {
	tests := []struct {
		name, value             string
		on, invalid, missingDir bool
	}{
		{"unset", "", false, false, false}, {"zero", "0", false, false, false}, {"false", "false", false, false, false},
		{"one", "1", true, false, false}, {"true", "true", true, false, false},
		{"invalid_yes", "yes", false, true, false}, {"invalid_upper", "TRUE", false, true, false},
		{"invalid_space", " true", false, true, false}, {"invalid_two", "2", false, true, false},
		{"enabled_missing_workdir", "1", true, true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workDir := t.TempDir()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWorkerRuntimeTraceCLIHelper$", "-test.timeout=3s")
			for _, pair := range os.Environ() {
				if !strings.HasPrefix(pair, "WK_") {
					cmd.Env = append(cmd.Env, pair)
				}
			}
			dirArg := workDir
			if test.missingDir {
				dirArg = ""
			}
			cmd.Env = append(cmd.Env, "WK_R28_RUNTIME_CLI_CHILD=1", "WK_R28_LISTEN="+address,
				"WK_R28_WORK_DIR="+dirArg, "WK_BENCH_API_TOKEN=r28-bench-test", "WK_CHAT_LIFECYCLE_RUNTIME_TRACE="+test.value)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer func() {
				if !joined {
					_ = cmd.Process.Kill()
					select {
					case <-done:
						joined = true
					case <-time.After(time.Second):
						t.Error("worker CLI child did not join")
					}
				}
			}()
			if test.invalid {
				select {
				case childErr := <-done:
					joined = true
					exit, ok := childErr.(*exec.ExitError)
					if !ok || exit.ExitCode() != exitConfig {
						t.Fatal("invalid trace opt-in/work-dir did not return config failure")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("invalid explicit runtime trace configuration was ignored and worker kept serving")
				}
				return
			}
			client := &http.Client{Timeout: 200 * time.Millisecond}
			defer client.CloseIdleConnections()
			deadline := time.Now().Add(2 * time.Second)
			ready := false
			for time.Now().Before(deadline) {
				request, _ := http.NewRequest(http.MethodGet, "http://"+address+"/v1/info", nil)
				request.Header.Set("Authorization", "Bearer r28-control")
				response, requestErr := client.Do(request)
				if requestErr == nil {
					raw, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
					response.Body.Close()
					var info struct {
						ProtocolVersion int `json:"protocol_version"`
					}
					if readErr != nil || len(raw) > 65536 || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &info) != nil || info.ProtocolVersion != 15 {
						t.Fatal("trace configuration changed worker protocol")
					}
					ready = true
					break
				}
				select {
				case childErr := <-done:
					joined = true
					t.Fatalf("valid worker exited before readiness: %T", childErr)
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !ready {
				t.Fatal("worker CLI did not become ready within fixed test bound")
			}
			statusPath := filepath.Join(workDir, "runtime-trace", "status.json")
			if test.on {
				raw, err := os.ReadFile(statusPath)
				if err != nil || len(raw) > 4096 {
					t.Fatal("explicit opt-in did not create bounded pending runtime trace receipt")
				}
				var status struct {
					State        string `json:"state"`
					WriterJoined bool   `json:"writer_joined"`
				}
				if json.Unmarshal(raw, &status) != nil || status.State != "pending" || status.WriterJoined {
					t.Fatal("active recorder falsely claimed completed writer")
				}
			} else if _, err := os.Lstat(filepath.Join(workDir, "runtime-trace")); !os.IsNotExist(err) {
				t.Fatal("default-off trace created diagnostic artifact")
			}
			// TERM preserves the existing real CLI process lifetime. It must not
			// wait for diagnostic export or create a fabricated completion receipt.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				joined = true
			case <-time.After(time.Second):
				t.Fatal("diagnostic configuration delayed ordinary process termination")
			}
			if test.on {
				raw, _ := os.ReadFile(statusPath)
				if bytes.Contains(raw, []byte(`"state":"complete"`)) || bytes.Contains(raw, []byte(`"state": "complete"`)) {
					t.Fatal("untriggered terminated process fabricated complete trace")
				}
			}
		})
	}
}
