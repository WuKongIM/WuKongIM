package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

func TestRenderJSONL(t *testing.T) {
	var buf bytes.Buffer
	result := inspect.Result{
		Rows: []inspect.Row{{"uid": "u1"}, {"uid": "u2"}},
		Stats: inspect.Stats{
			ReturnedRows: 2,
			HasMore:      true,
			NextCursor:   "next",
		},
	}
	if err := renderResult(&buf, "jsonl", result); err != nil {
		t.Fatalf("renderResult(): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"uid":"u1"`) || !strings.Contains(lines[2], `"type":"stats"`) || !strings.Contains(lines[2], `"next_cursor":"next"`) {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestRenderJSONUsesStableFieldNames(t *testing.T) {
	var buf bytes.Buffer
	result := inspect.Result{
		Rows: []inspect.Row{{"payload": []byte("one")}},
		Stats: inspect.Stats{
			ScanMode:         "local-bounded",
			ScannedHashSlots: []uint16{1, 2},
			ScannedRows:      3,
			ReturnedRows:     1,
			HasMore:          true,
			NextCursor:       "next",
		},
	}
	if err := renderResult(&buf, "json", result); err != nil {
		t.Fatalf("renderResult(): %v", err)
	}
	output := buf.String()
	for _, want := range []string{`"rows"`, `"stats"`, `"scan_mode"`, `"scanned_hash_slots"`, `"payload": "b25l"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %s", output, want)
		}
	}
}

func TestRenderTableEmptyIncludesStatsFooter(t *testing.T) {
	var buf bytes.Buffer
	result := inspect.Result{Stats: inspect.Stats{ScanMode: "local-bounded", ScannedRows: 7, HasMore: true, NextCursor: "next"}}
	if err := renderResult(&buf, "table", result); err != nil {
		t.Fatalf("renderResult(): %v", err)
	}
	output := buf.String()
	for _, want := range []string{"rows=0", "has_more=true", "scan_mode=local-bounded", "scanned_rows=7", "next_cursor=next"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %s", output, want)
		}
	}
}
