package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalMediumRCRevisionNeutralHarness(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "scripts", "cloud-sim", "local-medium-rc")
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	common := read("workload_test.go.overlay")
	for _, fragment := range []string{
		"localMediumRCPhysicalHashSlots = 256",
		"localMediumRCLogicalSlots      = 10",
		"localMediumRCRecipients        = 512",
		"localMediumRCTargetGroups      = 221",
		"localMediumRCOnlineRoutes      = 55",
		"preferredLeader = 2",
		"PreferredLeader/actual Leader mismatch",
		"TestLocalMediumRCWorkloadEquivalence",
		"BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256",
		"BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55",
		"fixture.pushAndAckCore(context.Background(), uint64(iteration+1))",
		"fixture.validatePushAndAck(before, uint64(b.N*localMediumRCOnlineRoutes))",
		"pending ack count before recvacks = %d, want %d",
		"pending ack count after recvacks = %d, want 0",
		"localMediumRCPresenceResolver",
		"localMediumRCPresenceTargetFromRecipient",
	} {
		if !strings.Contains(common, fragment) {
			t.Fatalf("common workload missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"presenceDirectoryAuthority", "channelAppendPresenceResolver"} {
		if strings.Contains(common, forbidden) {
			t.Fatalf("common workload contains revision-private presence adapter %q", forbidden)
		}
	}

	baseline := read("adapter_baseline_test.go.overlay")
	for _, fragment := range []string{"channelAppendRecipientResolver", "RouteKeys", "localOwnerPusher"} {
		if !strings.Contains(baseline, fragment) {
			t.Fatalf("baseline adapter missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"RouteAuthoritiesPartial", "internal/infra/delivery"} {
		if strings.Contains(baseline, forbidden) {
			t.Fatalf("baseline adapter contains candidate-only %q", forbidden)
		}
	}

	candidate := read("adapter_candidate_test.go.overlay")
	for _, fragment := range []string{"RouteAuthoritiesPartial", "NewRecipientAuthorityResolver", "NewLocalOwnerPusher"} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("candidate adapter missing %q", fragment)
		}
	}

	runnerPath := filepath.Join(dir, "build-smoke.sh")
	runner := read("build-smoke.sh")
	for _, fragment := range []string{
		"candidate-bound baseline lock",
		"baseline_lock:{path:$baseline_lock_path,sha256:$baseline_lock_sha",
		"common_workload:{path:$common_path,sha256:$common_sha}",
		"baseline:{path:$baseline_adapter_path,sha256:$baseline_adapter_sha}",
		"candidate:{path:$candidate_adapter_path,sha256:$candidate_adapter_sha}",
		"worktree add --detach",
		"TestLocalMediumRCWorkloadEquivalence",
		"selected_go_root",
		"compile:{path:$compile_path,sha256:$compile_sha}",
		"link:{path:$link_path,sha256:$link_sha}",
		"asm:{path:$asm_path,sha256:$asm_sha}",
	} {
		if !strings.Contains(runner, fragment) {
			t.Fatalf("build-smoke runner missing %q", fragment)
		}
	}
	baselineLock := read("baseline-lock.json")
	for _, fragment := range []string{
		"wukongim/local-medium-rc-baseline-lock/v1",
		"6e419a3cbd7b7ec203026ee7323a8dc09b4291fe",
		"gh-29889127179-1",
	} {
		if !strings.Contains(baselineLock, fragment) {
			t.Fatalf("baseline lock missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"cloud-sim-provision", "gh workflow run", "wkcloudsim create"} {
		if strings.Contains(runner, forbidden) {
			t.Fatalf("build-smoke runner contains cloud action %q", forbidden)
		}
	}
	command := exec.CommandContext(t.Context(), "bash", "-n", runnerPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bash -n build-smoke.sh: %v\n%s", err, output)
	}
}

func TestLocalMediumRCMicroABBAHarness(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "scripts", "cloud-sim", "local-medium-rc")
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	runner := read("run-micro-abba.sh")
	for _, fragment := range []string{
		"RUN_REVISION_NEUTRAL_MICRO_ABBA",
		"for leg in A1 B1 B2 A2",
		"BENCHTIME_SECONDS",
		"SAMPLES_PER_LEG",
		"require_clean_host",
		"COOLDOWN_SECONDS",
		"GOMAXPROCS=4",
		"external-high-cpu.tsv",
		"build-smoke-manifest.json",
		"cleanup_active_test",
		"active_child_identity_matches",
		"CHILD_TERM_GRACE_SECONDS=10",
		"kill -KILL",
		"track_active_child \"$pid\" cooldown",
		"working harness does not match candidate commit",
		"executor_bundle",
		"WK_LOCAL_RC_EXPECTED_BASELINE_SHA",
		"binary embedded Go version differs",
		"verify_equivalence baseline",
		"candidate-bound baseline lock SHA-256 mismatch",
		"recorded Go $name SHA-256 mismatch",
	} {
		if !strings.Contains(runner, fragment) {
			t.Fatalf("micro A-B-B-A runner missing %q", fragment)
		}
	}

	evaluator := read("evaluate-micro-abba.sh")
	for _, fragment := range []string{
		"baseline commit is unavailable",
		"candidate commit is unavailable",
		"binary SHA-256 mismatch",
		"benchmark names differ from the frozen contract",
		"sample count differs",
		"cooldown is shorter",
		"duration is shorter",
		"host.os == $os and .host.arch == $arch",
		"executor bundle SHA-256 mismatch",
		"WK_LOCAL_RC_EXPECTED_CANDIDATE_SHA",
		"common workload SHA-256 differs",
		"equivalence rerun differs",
		"micro-verdict.json",
		"evaluation_started",
		"trap record_unexpected_error ERR",
		"trap finish_evaluation EXIT",
		"EVALUATION_COMPLETE=1",
		"candidate-bound baseline lock SHA-256 mismatch",
		"recorded Go $name SHA-256 mismatch",
	} {
		if !strings.Contains(evaluator, fragment) {
			t.Fatalf("micro evaluator missing %q", fragment)
		}
	}

	policy := read("evaluate-micro-abba.jq")
	for _, fragment := range []string{
		"maximum_pair_drift_ratio",
		"maximum_authority_ns_ratio",
		"maximum_non_regression_ratio",
		"authority_allocs_no_regression",
		"owner_allocs_no_regression",
		"cloud_authorized: false",
		"decision = (if .micro_pass then \"micro_pass\" else \"micro_fail\" end)",
	} {
		if !strings.Contains(policy, fragment) {
			t.Fatalf("micro policy missing %q", fragment)
		}
	}

	manifestPolicy := read("validate-build-smoke-manifest.jq")
	for _, fragment := range []string{
		"(.baseline_lock | keys | sort)",
		"baseline-lock.json",
		"(.toolchain.tools | keys | sort) == [\"asm\", \"compile\", \"link\"]",
		".toolchain.gotoolchain == \"local\"",
		"(.adapters | keys | sort) == [\"baseline\", \"candidate\"]",
		"(.equivalence | keys | sort) == [\"baseline\", \"candidate\"]",
		"workload_test.go.overlay",
		"adapter_baseline_test.go.overlay",
		"adapter_candidate_test.go.overlay",
	} {
		if !strings.Contains(manifestPolicy, fragment) {
			t.Fatalf("build-smoke manifest policy missing %q", fragment)
		}
	}

	for _, name := range []string{"run-micro-abba.sh", "evaluate-micro-abba.sh"} {
		content := read(name)
		for _, forbidden := range []string{"gh workflow run", "wkcloudsim create", "wkcloudsim provision", "cloud-sim-provision"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s contains cloud mutation %q", name, forbidden)
			}
		}
	}

	command := exec.CommandContext(t.Context(), "bash", filepath.Join(dir, "test-micro-abba-static.sh"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("micro A-B-B-A static test: %v\n%s", err, output)
	}
}
