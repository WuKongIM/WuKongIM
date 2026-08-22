package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleRepairWorkflowOwnsOneReusableLeaseUntilQualification(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "chat-lifecycle-repair.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, fragment := range []string{
		"Agent Tool - Start Chat Lifecycle Repair",
		"configs/cloud/chat-lifecycle/repair-v1.json",
		"WK_CHAT_STAGE: repair",
		"scripts/chat-lifecycle/stage-orchestrate.sh",
		"chat-lifecycle-repair-result-",
		"chat-lifecycle-repair-cleanup-",
		"create-paid-cloud-lease",
		"discover-active-repair-handoffs.sh",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("repair workflow missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"chat-lifecycle-formal.yml", "chat-lifecycle-rehearsal.yml", "schedule:", "push:", "pull_request:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("repair workflow unexpectedly contains %q", forbidden)
		}
	}

	orchestrator, err := os.ReadFile(filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	orchestration := string(orchestrator)
	for _, fragment := range []string{
		"repair)", "repair-monitor.sh", "deployment_purpose=repair", "rebuild_repair_bundle",
		"repair-qualified.json", "wait_for_deployment_repair_revision", "release_current",
		"chat-lifecycle-repair-handoff.yml", "publish-chat-lifecycle-repair-handoff",
	} {
		if !strings.Contains(orchestration, fragment) {
			t.Fatalf("repair orchestration missing %q", fragment)
		}
	}
	if count := strings.Count(orchestration, `candidate_sha="$(wait_for_deployment_repair_revision`); count != 3 {
		t.Fatalf("repair orchestration captures %d protected-main candidates, want all three repair waits", count)
	}
	if count := strings.Count(orchestration, `rebuild_repair_bundle "$candidate_sha"`); count != 3 {
		t.Fatalf("repair orchestration rebuilds %d protected-main candidates, want every repair wait", count)
	}
	for _, name := range []string{"chat-lifecycle-repair-handoff.yml", "chat-lifecycle-repair-finalize.yml"} {
		body, readErr := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		workflowBody := string(body)
		if !strings.Contains(workflowBody, "release-selector.json") {
			t.Fatalf("%s lacks durable selector-bound repair handoff", name)
		}
	}
	handoffBody := string(readWorkflow(t, "chat-lifecycle-repair-handoff.yml"))
	for _, fragment := range []string{
		"chat-lifecycle-repair-handoff-", ".receipt.provenance.source_sha",
		".receipt.provenance.bundle_digest", "repair-terminal-cut.json",
		"reconstructed-observation.json", "terminal-cut/status-${worker}.json",
	} {
		if !strings.Contains(handoffBody, fragment) {
			t.Fatalf("repair handoff workflow missing %q", fragment)
		}
	}
	finalizer, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "chat-lifecycle-repair-finalize.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(finalizer); !strings.Contains(text, "schedule:") ||
		!strings.Contains(text, "release-until-zero.sh") || !strings.Contains(text, "discover-active-repair-handoffs.sh") {
		t.Fatal("repair finalizer is not an independent scheduled zero-inventory owner")
	}
	for _, fragment := range []string{"repair-owner.json", "parent_run_id", "expires_epoch - 7200"} {
		if !strings.Contains(string(finalizer), fragment) {
			t.Fatalf("repair finalizer lacks Acquire/handoff race guard %q", fragment)
		}
	}
	provision := string(readWorkflow(t, "cloud-lease-provision.yml"))
	for _, fragment := range []string{"repair_parent_run_id", "repair_acquire_owner/v1", "repair-parent-run.json"} {
		if !strings.Contains(provision, fragment) {
			t.Fatalf("Cloud Lease Provision does not bind exact repair owner %q", fragment)
		}
	}
	discovery, err := os.ReadFile(filepath.Join(root, "scripts", "chat-lifecycle", "discover-active-repair-handoffs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"cleanup_artifact_id", "zero-inventory.json", "account_id_hash", "release-selector.json"} {
		if !strings.Contains(string(discovery), fragment) {
			t.Fatalf("repair handoff discovery does not authenticate cleanup field %q", fragment)
		}
	}
	for _, fragment := range []string{"cloud-lease-provision-", "handoff_kind:\"acquire\"", "receipt.provenance.source_sha"} {
		if !strings.Contains(string(discovery), fragment) {
			t.Fatalf("repair discovery lacks paid Acquire recovery seam %q", fragment)
		}
	}
	if !strings.Contains(orchestration, "2880 + 3780 + readiness_timeout") {
		t.Fatal("repair expiry reserve omits one candidate bundle build")
	}
	if !strings.Contains(orchestration, "lease_expires_epoch - repair_finalizer_safety_seconds - repair_reserve_seconds") {
		t.Fatal("repair candidate deadline is not bounded by the independent finalizer cutoff")
	}
	for _, name := range []string{"chat-lifecycle-rehearsal.yml", "chat-lifecycle-formal.yml", "chat-lifecycle-repair.yml"} {
		body, readErr := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(body), "discover-active-repair-handoffs.sh") {
			t.Fatalf("%s can procure while an earlier repair Lease lacks zero proof", name)
		}
	}
}
