package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestLocalTerminalQueueConvergenceCommandPublishesTypedResult(t *testing.T) {
	directory := t.TempDir()
	baselinePath := filepath.Join(directory, "post-warmup.prom")
	candidatePath := filepath.Join(directory, "candidate.prom")
	outputPath := filepath.Join(directory, "result.json")
	at := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	writeQueueFixture(t, baselinePath, 3, queueCutFixture("warmup", "run", at.Add(-time.Minute)))
	writeQueueFixture(t, candidatePath, 0, queueCutFixture("run", "cooldown", at))

	exit := runWithStderr([]string{
		"report", "local-single-node-queue-convergence",
		"--post-warmup", baselinePath,
		"--candidate", candidatePath,
		"--run-id", "run-1",
		"--assignment-id", "assignment-1",
		"--output", outputPath,
	}, &strings.Builder{})
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var result localbaseline.TerminalQueueConvergence
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != localbaseline.TerminalQueueConvergenceSchema || !result.EvidenceComplete || !result.Converged || result.CandidateCut.ObservedAt != at {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalTerminalQueueConvergenceCommandDistinguishesPendingFromInvalid(t *testing.T) {
	directory := t.TempDir()
	baselinePath := filepath.Join(directory, "post-warmup.prom")
	candidatePath := filepath.Join(directory, "candidate.prom")
	outputPath := filepath.Join(directory, "result.json")
	at := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	writeQueueFixture(t, baselinePath, 1, queueCutFixture("warmup", "run", at.Add(-time.Minute)))
	writeQueueFixture(t, candidatePath, 2, queueCutFixture("run", "cooldown", at))

	args := []string{
		"report", "local-single-node-queue-convergence",
		"--post-warmup", baselinePath, "--candidate", candidatePath,
		"--run-id", "run-1", "--assignment-id", "assignment-1", "--output", outputPath,
	}
	if exit := runWithStderr(args, &strings.Builder{}); exit != localTerminalQueuePendingExit {
		t.Fatalf("pending exit = %d, want %d", exit, localTerminalQueuePendingExit)
	}
	writeQueueFixture(t, candidatePath, 0, queueCutFixture("stopped", "", at))
	if exit := runWithStderr(args, &strings.Builder{}); exit != exitInternal {
		t.Fatalf("invalid exit = %d, want %d", exit, exitInternal)
	}
}

func queueCutFixture(phase, active string, at time.Time) localbaseline.ProductQueueCut {
	return localbaseline.ProductQueueCut{
		Schema: localbaseline.ProductQueueCutSchema, ObservedAt: at,
		RunID: "run-1", AssignmentID: "assignment-1", Phase: phase, ActivePhase: active,
		ReceiveDrainSHA256: strings.Repeat("a", 64),
	}
}

func writeQueueFixture(t *testing.T, path string, depth int, cut localbaseline.ProductQueueCut) {
	t.Helper()
	metadata, err := json.Marshal(cut)
	if err != nil {
		t.Fatal(err)
	}
	value := "0"
	if depth != 0 {
		value = string(rune('0' + depth))
	}
	body := "# wkbench_local_single_node_cut " + string(metadata) + "\n" +
		"wukongim_gateway_async_send_queue_depth " + value + "\n" +
		"wukongim_channelv2_reactor_mailbox_depth{reactor_id=\"0\",priority=\"normal\"} " + value + "\n" +
		"wukongim_channelv2_worker_queue_depth{pool=\"store_append\"} " + value + "\n" +
		"wukongim_runtime_pool_queue_depth{component=\"channel\",pool=\"append\"} " + value + "\n" +
		"wukongim_channelappend_writer_state_items{kind=\"pending_append\"} " + value + "\n" +
		"wukongim_channelappend_writer_state_items{kind=\"append_inflight\"} " + value + "\n" +
		"wukongim_channelappend_writer_state_items{kind=\"post_commit_backlog\"} " + value + "\n" +
		"wukongim_channelappend_post_commit_handoff_depth " + value + "\n" +
		"wukongim_channelappend_post_commit_retry_queue_depth " + value + "\n" +
		"wukongim_ants_pool_running{component=\"channelappend\",pool=\"advance\"} " + value + "\n" +
		"wukongim_ants_pool_running{component=\"channelappend\",pool=\"append_effect\"} " + value + "\n" +
		"wukongim_ants_pool_running{component=\"channelappend\",pool=\"post_commit\"} " + value + "\n" +
		"wukongim_storage_commit_queue_depth " + value + "\n" +
		"wukongim_delivery_recipient_worker_queue_depth " + value + "\n" +
		"wukongim_delivery_recipient_worker_inflight " + value + "\n" +
		"wukongim_delivery_ack_bindings " + value + "\n" +
		localSingleNodeProductResultCounterMetrics()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
