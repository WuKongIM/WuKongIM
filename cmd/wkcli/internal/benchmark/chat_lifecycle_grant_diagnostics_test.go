package benchmark

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

// Failure cases: the compatibility line changes; failed workers or sequence
// disappear; unknown phase/reason or raw error/header/token data reach the log;
// invalid worker/sequence/attempt/status values gain authority; the extra line
// grows with history; or diagnostic data changes the existing verdict/exit.
func TestChatLifecycleGrantDeliverySummaryAppendsBoundedEvidenceAfterOriginalLine(t *testing.T) {
	result := grantDiagnosticCLIResult(t, `[
		{},
		{"worker_id":1,"sequence":173,"attempts":1,"phase":"response","reason":"http_status","http_status":409},
		{}
	]`)
	verdict := chatLifecycleCoordinatorVerdict(result)
	summary := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	const original = "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=delivery worker_runtime_code= observer_code= preflight_code= report=unavailable"
	lines := strings.Split(strings.TrimSuffix(summary, "\n"), "\n")
	if len(lines) != 2 || lines[0] != original {
		t.Fatalf("grant diagnostics changed or replaced the compatibility line: %q", summary)
	}
	rows := grantDiagnosticCLIRows(t, lines[1])
	if len(rows) != 1 || rows[0].WorkerID != 1 || rows[0].Sequence != 173 || rows[0].Attempts != 1 || rows[0].Phase != "response" || rows[0].Reason != "http_status" || rows[0].HTTPStatus != 409 {
		t.Fatalf("CLI lost the failed grant boundary: %+v", rows)
	}
	if chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleHarness {
		t.Fatal("grant diagnostic changed the harness exit code")
	}
	result.Outcome, result.Code = chatlifecycle.CoordinatorProductFailure, chatlifecycle.CoordinatorCodeObserver
	verdict = chatLifecycleCoordinatorVerdict(result)
	productSummary := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	if !strings.HasPrefix(productSummary, "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer ") || chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleProduct {
		t.Fatal("grant diagnostics replaced a concurrent product failure")
	}
	if !strings.Contains(productSummary, "\nchat-lifecycle grant_delivery=") {
		t.Fatal("concurrent observer classification discarded grant delivery evidence")
	}
}

func TestChatLifecycleGrantDeliverySummarySanitizesUnknownValuesAndRejectsInvalidPositions(t *testing.T) {
	const private = "CANARY_private_header_token_body_error"
	result := grantDiagnosticCLIResult(t, `[
		{},
		{"worker_id":1,"sequence":9,"attempts":2,"phase":"`+private+`","reason":"`+private+`","http_status":9999,"deadline_source":"`+private+`"},
		{}
	]`)
	summary := chatLifecycleCoordinatorSummary(chatLifecycleCoordinatorVerdict(result), result, "unavailable")
	if strings.Contains(summary, private) || strings.Contains(summary, "9999") {
		t.Fatal("CLI exposed unbounded diagnostic input")
	}
	lines := strings.Split(strings.TrimSuffix(summary, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatal("unknown failure must remain an explicit bounded diagnostic")
	}
	rows := grantDiagnosticCLIRows(t, lines[1])
	if len(rows) != 1 || rows[0].Phase != "worker" || rows[0].Reason != "unknown" || rows[0].HTTPStatus != 0 {
		t.Fatalf("unproven failure gained attribution: %+v", rows)
	}
	for _, invalid := range []string{
		`[{},{"worker_id":4,"sequence":9,"attempts":2,"phase":"worker","reason":"unknown"},{}]`,
		`[{},{"worker_id":2,"sequence":9,"attempts":2,"phase":"worker","reason":"unknown"},{}]`,
		`[{},{"worker_id":1,"sequence":0,"attempts":2,"phase":"worker","reason":"unknown"},{}]`,
		`[{},{"worker_id":1,"sequence":9,"attempts":0,"phase":"worker","reason":"unknown"},{}]`,
		`[{},{"worker_id":1,"sequence":9,"attempts":3,"phase":"worker","reason":"unknown"},{}]`,
	} {
		candidate := grantDiagnosticCLIResult(t, invalid)
		summary := chatLifecycleCoordinatorSummary(chatLifecycleCoordinatorVerdict(candidate), candidate, "unavailable")
		if strings.Contains(summary, "grant_delivery=") {
			t.Fatal("invalid worker position, sequence or attempt count became retained evidence")
		}
	}
}

func TestChatLifecycleGrantDeliverySummaryHasOneLineForAllThreeWorkers(t *testing.T) {
	result := grantDiagnosticCLIResult(t, `[
		{"worker_id":0,"sequence":18446744073709551615,"attempts":2,"phase":"request","reason":"deadline","deadline_source":"round"},
		{"worker_id":1,"sequence":18446744073709551615,"attempts":2,"phase":"body","reason":"body_decode","http_status":503},
		{"worker_id":2,"sequence":18446744073709551615,"attempts":2,"phase":"worker","reason":"unknown"}
	]`)
	summary := chatLifecycleCoordinatorSummary(chatLifecycleCoordinatorVerdict(result), result, "unavailable")
	lines := strings.Split(strings.TrimSuffix(summary, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatal("grant failure evidence must append only one bounded line")
	}
	rows := grantDiagnosticCLIRows(t, lines[1])
	if len(rows) != 3 {
		t.Fatalf("CLI retained %d worker failures, want three", len(rows))
	}
	for position, row := range rows {
		if row.WorkerID != uint64(position) || row.Sequence != ^uint64(0) {
			t.Fatal("CLI changed deterministic worker order or full-width grant sequence")
		}
	}
}

func TestChatLifecycleGrantDeliveryEmptyEvidenceKeepsOriginalSummary(t *testing.T) {
	result := grantDiagnosticCLIResult(t, `[{}, {}, {}]`)
	summary := chatLifecycleCoordinatorSummary(chatLifecycleCoordinatorVerdict(result), result, "unavailable")
	const want = "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=delivery worker_runtime_code= observer_code= preflight_code= report=unavailable\n"
	if summary != want {
		t.Fatal("absent grant evidence changed the existing fallback summary")
	}
}

// JSON injection deliberately compiles against the frozen baseline, whose
// decoder ignores GrantFailures. The actual missing line must make RED.
func grantDiagnosticCLIResult(t *testing.T, rows string) chatlifecycle.CoordinatorResult {
	t.Helper()
	result := chatlifecycle.CoordinatorResult{
		Outcome: chatlifecycle.CoordinatorHarnessInvalid, Code: chatlifecycle.CoordinatorCodeGrant,
		GrantFailure: chatlifecycle.CoordinatorGrantFailureDelivery,
	}
	if err := json.Unmarshal([]byte(`{"GrantFailures":`+rows+`}`), &result); err != nil {
		t.Fatal("invalid local diagnostic fixture")
	}
	return result
}

type grantDiagnosticCLIRow struct {
	WorkerID       uint64 `json:"worker_id"`
	Sequence       uint64 `json:"sequence"`
	Attempts       int    `json:"attempts"`
	Phase          string `json:"phase"`
	Reason         string `json:"reason"`
	HTTPStatus     int    `json:"http_status"`
	DeadlineSource string `json:"deadline_source"`
}

func grantDiagnosticCLIRows(t *testing.T, line string) []grantDiagnosticCLIRow {
	t.Helper()
	const prefix = "chat-lifecycle grant_delivery="
	if !strings.HasPrefix(line, prefix) || len(line)+1 > 1024 {
		t.Fatal("missing or oversized bounded grant diagnostic line")
	}
	var rows []grantDiagnosticCLIRow
	decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, prefix)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rows); err != nil || len(rows) > 3 {
		t.Fatal("grant diagnostic line has an unbounded or malformed shape")
	}
	for _, row := range rows {
		if row.HTTPStatus != 0 && (row.HTTPStatus < 100 || row.HTTPStatus > 599) {
			t.Fatal("grant diagnostic contains an invalid HTTP status")
		}
		switch row.DeadlineSource {
		case "", "round", "parent", "unknown":
		default:
			t.Fatal("grant diagnostic contains an unproven deadline source")
		}
	}
	return rows
}
