package chatlifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportJSONAndMarkdownContainVersionedEvidence(t *testing.T) {
	report := reportFixture(t)
	for _, format := range []ReportFormat{ReportFormatJSON, ReportFormatMarkdown} {
		body, err := MarshalReport(report, format)
		if err != nil {
			t.Fatalf("MarshalReport(%s): %v", format, err)
		}
		text := string(body)
		for _, want := range []string{
			ReportSchemaVersion, ReportThresholdVersion, ReportDesignProfile,
			report.ConfigDigest, report.Fence.RunHash, "thresholds", "logical_slot_groups", "worker_generations",
			"minimum_worker_uptime", "generated", "payload_bytes", "messages", "sync", "lifecycle",
			"meta_create", "latency", "resources", "data_filesystem_bytes", "cluster",
			"verdict", "capacity", string(ReportWarningShortLatencyBreach),
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s report does not contain %q:\n%s", format, want, text)
			}
		}
	}

	jsonBody, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(jsonBody, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != ReportSchemaVersion || decoded.ThresholdVersion != ReportThresholdVersion || decoded.DesignProfile != ReportDesignProfile {
		t.Fatalf("decoded versions = %+v", decoded)
	}
	if decoded.Thresholds != report.Thresholds || !decoded.Window.WarmupEnd.Equal(report.Window.WarmupEnd) ||
		!decoded.Window.QualificationAt.Equal(report.Window.QualificationAt) || !decoded.Window.FinalAt.Equal(report.Window.FinalAt) {
		t.Fatalf("decoded threshold/time contract = %+v", decoded)
	}
	if decoded.Verdict.Outcome != report.Verdict.Outcome || decoded.Verdict.Cause != report.Verdict.Cause || decoded.Verdict.Terminal != report.Verdict.Terminal {
		t.Fatalf("warning projection changed verdict: got %+v want %+v", decoded.Verdict, report.Verdict)
	}
}

func TestReportRedactsRawCredentialsIdentitiesChannelsAndPayloadMarkers(t *testing.T) {
	const (
		bearer  = "Bearer report-secret-token"
		rawUID  = "raw-user-000042"
		channel = "raw-user-000042@raw-user-000099"
		marker  = "wk-marker:run=secret/channel=raw"
	)
	cfg := FormalConfig()
	cfg.RunID = rawUID + "-" + bearer
	start := time.Unix(1_801_000_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: channel, Generation: 3}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	evidence := checkpointEvidenceFixture(false)
	evidence.Samples = []ReportSample{{Class: ReportSampleLifecycle, Index: 42, Hash: hashReportValue(marker)}}
	report, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []ReportFormat{ReportFormatJSON, ReportFormatMarkdown} {
		body, err := MarshalReport(report, format)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{bearer, "report-secret-token", rawUID, channel, marker} {
			if strings.Contains(string(body), secret) {
				t.Fatalf("%s report leaked %q:\n%s", format, secret, body)
			}
		}
	}
}

func TestWriteReportAtomicReplacesDestinationAndCleansSiblingTemporary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "checkpoint.json")
	if err := os.WriteFile(path, []byte("old-report"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := reportFixture(t)
	if err := WriteReportAtomic(path, report, ReportFormatJSON); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "old-report") || !strings.Contains(string(body), ReportSchemaVersion) {
		t.Fatalf("atomic destination = %s", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary sibling was retained: %+v", entries)
	}
}

func TestReportRejectsFreeTextAndMalformedHashesWithoutReplacingDestination(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "report.md")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := reportFixture(t)
	report.Samples[0].Hash = "raw-channel-id"
	if err := WriteReportAtomic(path, report, ReportFormatMarkdown); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("malformed report error = %v, want %v", err, ErrReportInvalid)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "preserve" {
		t.Fatalf("invalid report replaced destination: %q", body)
	}

	report = reportFixture(t)
	report.Warnings = append(report.Warnings, ReportWarningCode("Bearer injected"))
	if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("free-text warning error = %v", err)
	}

	report = reportFixture(t)
	report.Verdict.CleanupErrorCount = 1
	report.Verdict.CleanupErrors = []VerdictCleanupErrorCode{"Bearer nested-secret"}
	if _, err := MarshalReport(report, ReportFormatMarkdown); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("free-text cleanup error = %v", err)
	}
}

func TestReportConfigDigestIsDeterministicAndBindsThresholds(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "digest-run"
	start := time.Unix(1_803_000_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment", Generation: 1}
	first, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	if first.configDigest != second.configDigest || !validReportHash(first.configDigest) {
		t.Fatalf("deterministic digests = %q / %q", first.configDigest, second.configDigest)
	}
	local := LocalConfig()
	local.RunID = cfg.RunID
	localFence := WorkerFence{RunID: local.RunID, AssignmentID: fence.AssignmentID, Generation: fence.Generation}
	changed, err := NewCheckpointRecorder(local, localFence, start)
	if err != nil {
		t.Fatal(err)
	}
	if changed.configDigest == first.configDigest {
		t.Fatal("different effective threshold/profile config produced the same digest")
	}
}

func reportFixture(t *testing.T) Report {
	t.Helper()
	cfg := FormalConfig()
	cfg.RunID = "report-fixture"
	start := time.Unix(1_802_000_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "assignment", Generation: 5}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	report, err := captureCheckpoint(t, recorder, start.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 10, 24*time.Hour, 100), checkpointEvidenceFixture(false))
	if err != nil {
		t.Fatal(err)
	}
	return report
}
