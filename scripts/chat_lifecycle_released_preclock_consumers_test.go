package scripts_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type releasedPreclockConsumerStep struct {
	Name            string            `yaml:"name"`
	ID              string            `yaml:"id"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	With            map[string]string `yaml:"with"`
}

func releasedPreclockConsumerSteps(t *testing.T, workflow, job string) []releasedPreclockConsumerStep {
	t.Helper()
	var parsed struct {
		Jobs map[string]struct {
			Steps []releasedPreclockConsumerStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(readWorkflow(t, workflow), &parsed); err != nil {
		t.Fatal(err)
	}
	steps := parsed.Jobs[job].Steps
	if len(steps) == 0 {
		t.Fatalf("missing job %s", job)
	}
	return steps
}

func releasedPreclockConsumerStepAt(t *testing.T, steps []releasedPreclockConsumerStep, nameOrID string) (int, releasedPreclockConsumerStep) {
	t.Helper()
	for i, step := range steps {
		if step.ID == nameOrID || step.Name == nameOrID {
			return i, step
		}
	}
	t.Fatalf("missing consumer step %s", nameOrID)
	return -1, releasedPreclockConsumerStep{}
}

func TestChatLifecycleReleasedPreclockConsumersFormalProcurementGate(t *testing.T) {
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-formal.yml", "orchestrate")
	i, auth := releasedPreclockConsumerStepAt(t, steps, "Authenticate transition, bundle, aggregate ledger, and SSH identities")
	j, _ := releasedPreclockConsumerStepAt(t, steps, "Materialize, quote, acquire, deploy, and hand off remote formal ownership")
	if auth.ContinueOnError || i >= j {
		t.Fatal("continuation authentication must be a mandatory procurement predecessor")
	}
	for _, want := range []string{
		"authenticate-released-preclock.py validate",
		`--directory "$WK_CHAT_TRANSITION_DIR"`,
		`--run-plan "$WK_CHAT_TRANSITION_DIR/run-plan.json"`,
		`test "$(( prior + rehearsal_cost ))" = "$(jq -er .committed_micros "$WK_CHAT_TRANSITION_DIR/transition.json")"`,
	} {
		if !strings.Contains(auth.Run, want) {
			t.Fatalf("formal admission is missing %q", want)
		}
	}
	if strings.Contains(auth.Run, "proof.json\" ]]") || strings.Contains(auth.Run, "|| true") {
		t.Fatal("formal consumer must validate missing-proof cases instead of treating proof as optional validation")
	}
}

func TestChatLifecycleReleasedPreclockConsumersCleanupIndependence(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		t.Run(stage, func(t *testing.T) {
			steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-"+stage+"-finalize.yml", "finalize")
			releaseIndex, release := releasedPreclockConsumerStepAt(t, steps, "release")
			cleanupIndex, _ := releasedPreclockConsumerStepAt(t, steps, "cleanup")
			authIndex, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
			failureIndex, failure := releasedPreclockConsumerStepAt(t, steps, "continuation_failure")
			deleteIndex, _ := releasedPreclockConsumerStepAt(t, steps, "Delete released encrypted deployment handoff")
			_, complete := releasedPreclockConsumerStepAt(t, steps, "complete")
			if releaseIndex >= cleanupIndex || cleanupIndex >= authIndex || !auth.ContinueOnError {
				t.Fatal("sidecar validation must be isolated after exact Lease cleanup")
			}
			if strings.Contains(release.If, "continuation") || failureIndex <= deleteIndex || failure.If != "steps.continuation.outcome == 'failure'" || failure.Run != "exit 1" {
				t.Fatal("sidecar failure may only fail the job after release proof publication and handoff deletion")
			}
			if !strings.Contains(complete.If, "steps.continuation.outcome == 'success'") {
				t.Fatal("qualified complete evidence must require independent continuation authentication")
			}
			if !strings.Contains(auth.Run, "authenticate-released-preclock.py validate") || !strings.Contains(auth.If, "steps.cleanup.outputs.zero == 'true'") {
				t.Fatal("missing post-cleanup continuation authority check")
			}
			_, upload := releasedPreclockConsumerStepAt(t, steps, "complete_upload")
			if upload.With["retention-days"] != "90" {
				t.Fatal("complete evidence retention changed")
			}
			if stage == "rehearsal" {
				for _, name := range []string{"Enable formal continuation before transition publication", "transition"} {
					_, step := releasedPreclockConsumerStepAt(t, steps, name)
					if !strings.Contains(step.If, "steps.continuation.outcome == 'success'") {
						t.Fatalf("%s can advance an unauthenticated continuation", name)
					}
				}
				_, transition := releasedPreclockConsumerStepAt(t, steps, "transition")
				if !strings.Contains(transition.Run, `cp -a "$final_dir/continuation" "$WK_CHAT_TRANSITION_DIR/"`) || !strings.Contains(transition.Run, "committed_micros=$(( prior + rehearsal_cost ))") {
					t.Fatal("formal transition must preserve continuation bytes and prior plus current cost")
				}
				if strings.Count(auth.Run, "authenticate-released-preclock.py validate") != 2 || !strings.Contains(auth.Run, `--directory "$final_dir"`) || !strings.Contains(auth.Run, `--directory "$WK_CHAT_HANDOFF_DIR"`) {
					t.Fatal("both original handoff and selected terminal require independent authentication")
				}
			} else {
				if !strings.Contains(auth.Run, `--directory "$WK_CHAT_HANDOFF_DIR/rehearsal"`) || !strings.Contains(auth.Run, `--run-plan "$WK_CHAT_HANDOFF_DIR/rehearsal/run-plan.json"`) || !strings.Contains(auth.Run, ".lease_plan.budget.committed_micros") || !strings.Contains(auth.Run, "rehearsal/transition.json") {
					t.Fatal("formal evidence must bind its prior to the authenticated original rehearsal plan and transition")
				}
				if !strings.Contains(complete.Run, `cp -a "$WK_CHAT_HANDOFF_DIR/rehearsal/." "$WK_CHAT_COMPLETE_DIR/rehearsal/"`) {
					t.Fatal("complete evidence drops original rehearsal continuation")
				}
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockConsumersTerminalPreservation(t *testing.T) {
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-rehearsal-finalize.yml", "finalize")
	captureIndex, capture := releasedPreclockConsumerStepAt(t, steps, "continuation_capture")
	uploadIndex, _ := releasedPreclockConsumerStepAt(t, steps, "final_upload")
	if captureIndex >= uploadIndex || !capture.ContinueOnError || !strings.Contains(capture.If, "steps.collect.outputs.diagnosis_pending") || !strings.Contains(capture.If, "steps.credential_evidence.outputs.ready") {
		t.Fatal("all new terminal/diagnosis paths must preserve continuation without blocking cleanup")
	}
	if !strings.Contains(capture.Run, `cp -a "$WK_CHAT_HANDOFF_DIR/continuation" "$WK_CHAT_FINAL_DIR/"`) {
		t.Fatal("terminal artifact must preserve the complete sidecar and original evidence tree")
	}
	_, issue := releasedPreclockConsumerStepAt(t, steps, "Advance the exact tracking Issue")
	if !strings.Contains(issue.Run, `dedupe="${dedupe}-producer-${{ matrix.handoff_run_id }}"`) {
		t.Fatal("new rehearsal producer must not reuse the previous failed attempt's Issue dedupe key")
	}
}
