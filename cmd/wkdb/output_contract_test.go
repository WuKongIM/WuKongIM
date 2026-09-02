package main

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

func TestRenderDiffReportMachineFormatsPreserveTypedRecords(t *testing.T) {
	report := transfer.VerifyReport{
		Equal:         false,
		Mode:          transfer.VerifyModeFull,
		HashSlotCount: 16,
		Meta: []transfer.VerifyDatasetReport{{
			Name:         "meta.users",
			Equal:        false,
			SourceRows:   2,
			TargetRows:   1,
			SourceDigest: "source-meta",
			TargetDigest: "target-meta",
		}},
		Message: []transfer.VerifyDatasetReport{{
			Name:         "message.messages",
			Equal:        true,
			SourceRows:   3,
			TargetRows:   3,
			SourceDigest: "source-message",
			TargetDigest: "source-message",
		}},
		Mismatches: []transfer.VerifyMismatch{{Scope: "meta.users", Detail: "row count differs"}},
	}

	t.Run("json round trip", func(t *testing.T) {
		var output bytes.Buffer
		if err := renderDiffReport(&output, "json", report); err != nil {
			t.Fatalf("renderDiffReport(json): %v", err)
		}
		decoder := json.NewDecoder(&output)
		decoder.DisallowUnknownFields()
		var decoded transfer.VerifyReport
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatalf("Decode(json): %v", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			t.Fatalf("trailing JSON error = %v, want EOF", err)
		}
		if !reflect.DeepEqual(decoded, report) {
			t.Fatalf("decoded report = %+v, want %+v", decoded, report)
		}
	})

	t.Run("jsonl record identity", func(t *testing.T) {
		var output bytes.Buffer
		if err := renderDiffReport(&output, "jsonl", report); err != nil {
			t.Fatalf("renderDiffReport(jsonl): %v", err)
		}
		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		wantTypes := []string{"summary", "meta", "message", "mismatch"}
		if len(lines) != len(wantTypes) {
			t.Fatalf("JSONL lines = %d, want %d; output=%q", len(lines), len(wantTypes), output.String())
		}
		for i, line := range lines {
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				t.Fatalf("line %d decode: %v", i, err)
			}
			if envelope.Type != wantTypes[i] {
				t.Fatalf("line %d type = %q, want %q", i, envelope.Type, wantTypes[i])
			}
		}
		if !strings.Contains(lines[1], `"name":"meta.users"`) || !strings.Contains(lines[3], `"detail":"row count differs"`) {
			t.Fatalf("JSONL records lost report details: %q", output.String())
		}
	})
}

func TestRenderDiffReportRejectsUnknownFormat(t *testing.T) {
	err := renderDiffReport(io.Discard, "yaml", transfer.VerifyReport{})
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("renderDiffReport() error = %v, want unknown format", err)
	}
}

func TestRenderTableUsesStableColumnsAndBinaryEncoding(t *testing.T) {
	result := inspect.Result{
		Rows: []inspect.Row{{
			"zeta":    "last",
			"payload": []byte("hi"),
			"alpha":   "first",
		}},
		Stats: inspect.Stats{
			ScanMode:     "hash-slot",
			ScannedRows:  1,
			ReturnedRows: 1,
		},
	}

	var output bytes.Buffer
	if err := renderResult(&output, "table", result); err != nil {
		t.Fatalf("renderResult(table): %v", err)
	}
	want := "alpha\tpayload\tzeta\nfirst\taGk=\tlast\nrows=1 has_more=false scan_mode=hash-slot scanned_rows=1 next_cursor=\n"
	if output.String() != want {
		t.Fatalf("table output = %q, want %q", output.String(), want)
	}
}

func TestRenderResultRejectsUnknownFormat(t *testing.T) {
	err := renderResult(io.Discard, "yaml", inspect.Result{})
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("renderResult() error = %v, want unknown format", err)
	}
}
