package localbaseline

import (
	"strings"
	"testing"
)

const completeStorageSummaryFixture = "tag\tnode\tevidence\tcommit_queue_depth_max\tphysical_commits_delta\tlogical_requests_delta\trecords_delta\tbytes_delta\tavg_requests_per_commit\tavg_records_per_commit\tcollect_avg_ms\tbuild_avg_ms\tcommit_avg_ms\tpublish_avg_ms\ttotal_avg_ms\trequest_count_delta\trequest_avg_ms\trequest_ok_delta\trequest_ok_avg_ms\trequest_timeout_delta\trequest_timeout_avg_ms\trequest_canceled_delta\trequest_canceled_avg_ms\trequest_error_delta\trequest_error_avg_ms\tleader_append_request_delta\tleader_append_request_avg_ms\tfollower_apply_request_delta\tfollower_apply_request_avg_ms\tmessage_append_request_delta\tmessage_append_request_avg_ms\twal_bytes_in_delta\twal_bytes_written_delta\twal_write_amplification\tflush_bytes_delta\tflush_count_delta\tcompaction_bytes_read_delta\tcompaction_bytes_written_delta\tcompaction_count_delta\tsstable_size_max\tcompaction_debt_max\tcompactions_in_progress_max\tread_amplification_max\tdisk_usage_max\tavg_bytes_per_commit\trequests_per_commit_p50\trequests_per_commit_p95\trequests_per_commit_p99\trecords_per_commit_p50\trecords_per_commit_p95\trecords_per_commit_p99\tbytes_per_commit_p50\tbytes_per_commit_p95\tbytes_per_commit_p99\n" +
	"001000\t127_0_0_1_5001\tcomplete\t3\t100\t300000\t300000\t614400000\t3000.000000\t3000.000000\t0.100000\t0.200000\t0.300000\t0.400000\t1.000000\t300000\t0.500000\t299997\t0.500000\t1\t0.750000\t1\t0.800000\t1\t0.900000\t100000\t0.400000\t100000\t0.500000\t100000\t0.600000\t614400000\t620000000\t1.009115\t2048\t2\t4096\t8192\t1\t1048576\t0\t1\t2\t1073741824\t6144000.000000\t1000.000000\t3000.000000\t5000.000000\t1000.000000\t3000.000000\t5000.000000\t2048000.000000\t6144000.000000\t10240000.000000\n"

const completeHostIOSummaryFixture = "tag\thost\tevidence\tphysical_device\tiops_available\tiops_max\tbytes_per_second_available\tbytes_per_second_max\tutilization_available\tutilization_percent_max\tservice_time_available\tservice_time_milliseconds_max\tread_write_split_available\n" +
	"001000\thost-local\tcomplete\tnvme0n1\t1\t1200.000000\t1\t80000000.000000\t1\t72.000000\t1\t1.250000\t1\n"

func TestParseStorageMetricsSummaryRequiresClosedAccountingAndDistribution(t *testing.T) {
	evidence, err := ParseStorageMetricsSummary(strings.NewReader(completeStorageSummaryFixture), "001000", "127_0_0_1_5001")
	if err != nil {
		t.Fatalf("ParseStorageMetricsSummary() error = %v", err)
	}
	if !storageMetricsEvidenceComplete(evidence, 1000) {
		t.Fatalf("evidence = %+v, want complete storage accounting", evidence)
	}

	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "result partition", old: "\t299997\t0.500000\t1\t0.750000", new: "\t299996\t0.500000\t1\t0.750000"},
		{name: "lane partition", old: "\t100000\t0.400000\t100000\t0.500000", new: "\t99999\t0.400000\t100000\t0.500000"},
		{name: "distribution absent", old: "\t1000.000000\t3000.000000\t5000.000000\t1000.000000", new: "\t0.000000\t3000.000000\t5000.000000\t1000.000000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := strings.Replace(completeStorageSummaryFixture, test.old, test.new, 1)
			got, parseErr := ParseStorageMetricsSummary(strings.NewReader(mutated), "001000", "127_0_0_1_5001")
			if parseErr != nil {
				t.Fatalf("syntactically valid incomplete row should remain publishable: %v", parseErr)
			}
			if storageMetricsEvidenceComplete(got, 1000) {
				t.Fatalf("evidence = %+v, want incomplete accounting", got)
			}
		})
	}
}

func TestParseStorageMetricsSummaryRejectsMalformedClosedSchema(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "wrong header", text: strings.Replace(completeStorageSummaryFixture, "physical_commits_delta", "commits", 1)},
		{name: "extra row", text: completeStorageSummaryFixture + strings.Split(completeStorageSummaryFixture, "\n")[1] + "\n"},
		{name: "non finite", text: strings.Replace(completeStorageSummaryFixture, "0.100000", "NaN", 1)},
		{name: "fractional count", text: strings.Replace(completeStorageSummaryFixture, "\t100\t300000", "\t100.5\t300000", 1)},
		{name: "wrong tag", text: strings.Replace(completeStorageSummaryFixture, "001000\t", "000999\t", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseStorageMetricsSummary(strings.NewReader(test.text), "001000", "127_0_0_1_5001"); err == nil {
				t.Fatal("ParseStorageMetricsSummary() error = nil")
			}
		})
	}
}

func TestParseHostIOSummaryAcceptsCompleteOrExplicitPlatformUnavailable(t *testing.T) {
	for _, fixture := range []string{
		completeHostIOSummaryFixture,
		strings.Replace(completeHostIOSummaryFixture,
			"complete\tnvme0n1\t1\t1200.000000\t1\t80000000.000000\t1\t72.000000\t1\t1.250000\t1",
			"unavailable\tnvme0n1\t0\tunavailable\t0\tunavailable\t0\tunavailable\t0\tunavailable\t0", 1),
	} {
		evidence, err := ParseHostIOSummary(strings.NewReader(fixture), "001000", "host-local")
		if err != nil {
			t.Fatalf("ParseHostIOSummary() error = %v", err)
		}
		if !hostIOEvidenceComplete(evidence, 1000) {
			t.Fatalf("evidence = %+v, want explicit complete/unavailable evidence", evidence)
		}
	}
}

func TestParseHostIOSummaryRejectsMissingOrContradictoryEvidence(t *testing.T) {
	tests := []string{
		strings.Replace(completeHostIOSummaryFixture, "complete\tnvme0n1", "missing\tnvme0n1", 1),
		strings.Replace(completeHostIOSummaryFixture, "\t1\t1200.000000", "\t0\t1200.000000", 1),
		strings.Replace(completeHostIOSummaryFixture, "\t1\t1200.000000\t1", "\t1\tNaN\t1", 1),
		strings.Replace(completeHostIOSummaryFixture, "001000\thost-local", "001000\thost-other", 1),
	}
	for _, fixture := range tests {
		if evidence, err := ParseHostIOSummary(strings.NewReader(fixture), "001000", "host-local"); err == nil && hostIOEvidenceComplete(evidence, 1000) {
			t.Fatalf("evidence = %+v, error = %v; want fail closed", evidence, err)
		}
	}
}

func TestEvaluateSingleNodeClusterStepRequiresStorageAndHostSummaries(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*StepEvidence)
		wantReason Reason
	}{
		{name: "storage absent", mutate: func(e *StepEvidence) { e.StorageMetrics = StorageMetricsEvidence{} }, wantReason: ReasonStorageMetrics},
		{name: "storage requests zero", mutate: func(e *StepEvidence) { e.StorageMetrics.LogicalRequests = 0 }, wantReason: ReasonStorageMetrics},
		{name: "host absent", mutate: func(e *StepEvidence) { e.HostIO = HostIOEvidence{} }, wantReason: ReasonHostIO},
		{name: "host missing", mutate: func(e *StepEvidence) { e.HostIO.Status = "missing" }, wantReason: ReasonHostIO},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			test.mutate(&evidence)
			result := EvaluateStep(evidence)
			if result.Clean || result.Outcome != OutcomeInsufficientEvidence || !containsReason(result.Reasons, test.wantReason) {
				t.Fatalf("result = %+v, want closed insufficient-evidence reason %q", result, test.wantReason)
			}
		})
	}
}
