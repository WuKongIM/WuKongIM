package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

func renderResult(w io.Writer, format string, result inspect.Result) error {
	switch format {
	case "json":
		return renderJSON(w, result)
	case "jsonl":
		return renderJSONL(w, result)
	case "table":
		return renderTable(w, result)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

func renderJSON(w io.Writer, result inspect.Result) error {
	normalized := renderedResult{
		Rows:  make([]inspect.Row, 0, len(result.Rows)),
		Stats: renderStats(result.Stats),
	}
	for _, row := range result.Rows {
		normalized.Rows = append(normalized.Rows, normalizeRow(row))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(normalized)
}

type renderedResult struct {
	Rows  []inspect.Row `json:"rows"`
	Stats renderedStats `json:"stats"`
}

type renderedStats struct {
	ScanMode         string   `json:"scan_mode"`
	ScannedHashSlots []uint16 `json:"scanned_hash_slots"`
	ScannedRows      int      `json:"scanned_rows"`
	ReturnedRows     int      `json:"returned_rows"`
	HasMore          bool     `json:"has_more"`
	NextCursor       string   `json:"next_cursor"`
}

func renderStats(stats inspect.Stats) renderedStats {
	return renderedStats{
		ScanMode:         stats.ScanMode,
		ScannedHashSlots: append([]uint16(nil), stats.ScannedHashSlots...),
		ScannedRows:      stats.ScannedRows,
		ReturnedRows:     stats.ReturnedRows,
		HasMore:          stats.HasMore,
		NextCursor:       stats.NextCursor,
	}
}

func renderJSONL(w io.Writer, result inspect.Result) error {
	enc := json.NewEncoder(w)
	for _, row := range result.Rows {
		if err := enc.Encode(normalizeRow(row)); err != nil {
			return err
		}
	}
	return enc.Encode(renderedJSONLStats{Type: "stats", Stats: renderStats(result.Stats)})
}

type renderedJSONLStats struct {
	Type  string        `json:"type"`
	Stats renderedStats `json:"stats"`
}

func normalizeRow(row inspect.Row) inspect.Row {
	out := make(inspect.Row, len(row))
	for key, value := range row {
		if bytes, ok := value.([]byte); ok {
			out[key] = base64.StdEncoding.EncodeToString(bytes)
			continue
		}
		out[key] = value
	}
	return out
}

func renderTable(w io.Writer, result inspect.Result) error {
	if len(result.Rows) == 0 {
		return renderTableFooter(w, result.Stats)
	}
	columns := sortedColumns(result.Rows)
	if err := renderTableHeader(w, columns); err != nil {
		return err
	}
	for _, row := range result.Rows {
		normalized := normalizeRow(row)
		for i, column := range columns {
			if i > 0 {
				if _, err := fmt.Fprint(w, "\t"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, normalized[column]); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return renderTableFooter(w, result.Stats)
}

func renderTableHeader(w io.Writer, columns []string) error {
	for i, column := range columns {
		if i > 0 {
			if _, err := fmt.Fprint(w, "\t"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, column); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func sortedColumns(rows []inspect.Row) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		for key := range row {
			seen[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

func renderTableFooter(w io.Writer, stats inspect.Stats) error {
	_, err := fmt.Fprintf(
		w,
		"rows=%d has_more=%v scan_mode=%s scanned_rows=%d next_cursor=%s\n",
		stats.ReturnedRows,
		stats.HasMore,
		stats.ScanMode,
		stats.ScannedRows,
		stats.NextCursor,
	)
	return err
}
