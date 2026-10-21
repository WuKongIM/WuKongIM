//go:build integration

package scripts_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleReleasedPreclockConsumersCopyActualWorkflowShell(t *testing.T) {
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-rehearsal-finalize.yml", "finalize")
	_, capture := releasedPreclockConsumerStepAt(t, steps, "continuation_capture")
	for _, continuation := range []bool{false, true} {
		name := "first_rehearsal"
		if continuation {
			name = "released_preclock_continuation"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			handoff, final := filepath.Join(root, "handoff"), filepath.Join(root, "final")
			for _, dir := range []string{handoff, final} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string][]byte{
				"proof.json":                  []byte("{\"classification\":\"unclassified_before_accepted_run_start\",\"reserve_micros\":74477600}\n"),
				"evidence-manifest.json":      []byte("{\"fixture\":true}\n"),
				"evidence/prior-failure.json": []byte("{\"original_failure\":\"unknown\"}\n"),
			}
			if continuation {
				for path, data := range files {
					target := filepath.Join(handoff, "continuation", path)
					if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			cmd := exec.Command("bash", "-c", capture.Run)
			cmd.Env = append(os.Environ(), "WK_CHAT_HANDOFF_DIR="+handoff, "WK_CHAT_FINAL_DIR="+final)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("terminal preservation failed: %v: %s", err, output)
			}
			if !continuation {
				if _, err := os.Stat(filepath.Join(final, "continuation")); !os.IsNotExist(err) {
					t.Fatal("first rehearsal unexpectedly acquired a continuation")
				}
				return
			}
			for path, want := range files {
				got, err := os.ReadFile(filepath.Join(final, "continuation", path))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("original evidence changed: %s: %v", path, err)
				}
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockConsumersValidationActualWorkflowShell(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-"+stage+"-finalize.yml", "finalize")
		_, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
		for _, previous := range []string{"false", "true"} {
			for _, status := range []string{"0", "19"} {
				t.Run(stage+"/previous_"+previous+"/validator_"+status, func(t *testing.T) {
					root := t.TempDir()
					handoff, final := filepath.Join(root, "handoff"), filepath.Join(root, "final")
					bin, log := filepath.Join(root, "bin"), filepath.Join(root, "calls")
					for _, dir := range []string{handoff, final, bin, filepath.Join(root, "previous-final"), filepath.Join(handoff, "rehearsal")} {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
					}
					// Only the authority helper is stubbed; run the checked-in workflow shell unchanged.
					stub := "#!/bin/bash\nset -eu\nprintf '%s\\n' \"$*\" >>\"$CONSUMER_CALL_LOG\"\nexit \"$CONSUMER_VALIDATOR_STATUS\"\n"
					if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(stub), 0700); err != nil {
						t.Fatal(err)
					}
					for _, path := range []string{filepath.Join(handoff, "run-plan.json"), filepath.Join(final, "run-plan.json"), filepath.Join(root, "previous-final", "run-plan.json")} {
						if err := os.WriteFile(path, []byte(`{"lease_plan":{"budget":{"committed_micros":104477600}}}`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(filepath.Join(handoff, "rehearsal", "transition.json"), []byte(`{"committed_micros":104477600}`), 0600); err != nil {
						t.Fatal(err)
					}
					script := strings.ReplaceAll(auth.Run, "${{ steps.existing.outputs.ready }}", previous)
					cmd := exec.Command("bash", "-c", script)
					cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "WK_CHAT_HANDOFF_DIR="+handoff,
						"WK_CHAT_FINAL_DIR="+final, "RUNNER_TEMP="+root, "CONSUMER_CALL_LOG="+log, "CONSUMER_VALIDATOR_STATUS="+status)
					output, err := cmd.CombinedOutput()
					if status == "0" && err != nil {
						t.Fatalf("valid continuation: %v: %s", err, output)
					}
					if status != "0" {
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != 19 {
							t.Fatalf("authority rejection was swallowed: %v: %s", err, output)
						}
					}
					calls, err := os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					directory := handoff
					if stage == "formal" {
						directory = filepath.Join(handoff, "rehearsal")
					}
					want := "scripts/chat-lifecycle/authenticate-released-preclock.py validate --directory " + directory + " --run-plan " + filepath.Join(directory, "run-plan.json") + "\n"
					if status == "0" && stage == "rehearsal" {
						directory = final
						if previous == "true" {
							directory = filepath.Join(root, "previous-final")
						}
						want += "scripts/chat-lifecycle/authenticate-released-preclock.py validate --directory " + directory + " --run-plan " + filepath.Join(directory, "run-plan.json") + "\n"
					}
					if string(calls) != want {
						t.Fatalf("wrong evidence authority arguments: got %q, want %q", calls, want)
					}
				})
			}
		}
	}
}
