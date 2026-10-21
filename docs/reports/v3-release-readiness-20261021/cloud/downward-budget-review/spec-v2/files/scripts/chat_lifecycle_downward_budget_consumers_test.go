package scripts_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestChatLifecycleDownwardBudgetConsumerWorkflowAuthority(t *testing.T) {
	for _, name := range []string{"chat-lifecycle-formal.yml", "chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		var workflow struct {
			Permissions map[string]string `yaml:"permissions"`
		}
		if err := yaml.Unmarshal(readWorkflow(t, name), &workflow); err != nil {
			t.Fatal(err)
		}
		if len(workflow.Permissions) != 3 || workflow.Permissions["contents"] != "read" || workflow.Permissions["actions"] != "write" || workflow.Permissions["issues"] != "write" {
			t.Fatalf("%s changed consumer authority: %v", name, workflow.Permissions)
		}
	}
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-formal.yml", "orchestrate")
	_, auth := releasedPreclockConsumerStepAt(t, steps, "Authenticate transition, bundle, aggregate ledger, and SSH identities")
	validate, budget := strings.Index(auth.Run, "authenticate-released-preclock.py validate"), strings.Index(auth.Run, "authenticate-released-preclock.py budget")
	if validate < 0 || budget < validate || !strings.Contains(auth.Run, "WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS=") {
		t.Fatal("formal procurement must derive and export the authenticated rehearsal budget after sidecar validation")
	}
	if strings.Contains(auth.Run, "1350000000") || strings.Contains(auth.Run, "1500000000") {
		t.Fatal("formal procurement must not default missing authenticated caps")
	}
}

func TestChatLifecycleDownwardBudgetFinalizerWorkflowCleanupPrecedesQualification(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-"+stage+"-finalize.yml", "finalize")
		releaseIndex, release := releasedPreclockConsumerStepAt(t, steps, "release")
		cleanupIndex, _ := releasedPreclockConsumerStepAt(t, steps, "cleanup")
		authIndex, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
		if releaseIndex >= cleanupIndex || cleanupIndex >= authIndex || !auth.ContinueOnError || strings.Contains(release.If, "continuation") {
			t.Fatalf("%s budget validation can block owned Lease cleanup", stage)
		}
		if !strings.Contains(auth.Run, "authenticate-released-preclock.py budget") || !strings.Contains(auth.Run, "--deployment-plan") || !strings.Contains(auth.Run, "--matches-run-plan") {
			t.Fatalf("%s does not compare handoff, selected terminal, and deployment budget evidence", stage)
		}
		publishName := "Upload formal zero-inventory proof"
		if stage == "rehearsal" {
			publishName = "Upload zero-inventory proof"
		}
		_, publish := releasedPreclockConsumerStepAt(t, steps, publishName)
		if publish.If != "always() && steps.cleanup.outputs.zero == 'true'" {
			t.Fatalf("%s budget validation can suppress zero proof publication", stage)
		}
	}
}

// Actions adds success() to conditions without an explicit status function.
// A zero proof must still publish after a terminal budget gate rejects the run.
func TestChatLifecycleDownwardBudgetZeroUploadSurvivesQualificationFailure(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		t.Run(stage, func(t *testing.T) {
			steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-"+stage+"-finalize.yml", "finalize")
			publishName := "Upload formal zero-inventory proof"
			if stage == "rehearsal" {
				publishName = "Upload zero-inventory proof"
			}
			uploadIndex, upload := releasedPreclockConsumerStepAt(t, steps, publishName)
			if upload.If != "always() && steps.cleanup.outputs.zero == 'true'" {
				t.Fatalf("after a failed budget qualification, Actions implicitly skips zero proof without an explicit status guard: %q", upload.If)
			}
			rejectionSeen := false
			for _, step := range steps[:uploadIndex] {
				if strings.Contains(step.Run, "committed_micros < operational_stop_micros") || strings.Contains(step.Run, "aggregate_conservative_micros <= request_budget_limit_micros") {
					if step.ContinueOnError || !strings.Contains(step.Run, ")) || exit 1") {
						t.Fatal("budget rejection must remain a failing qualification gate")
					}
					rejectionSeen = true
				}
			}
			if !rejectionSeen {
				t.Fatal("missing budget rejection before zero publication")
			}
		})
	}
}
