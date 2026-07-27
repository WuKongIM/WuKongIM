package log

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppLogReaderSourcesReportsFixedFiles(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "app one\napp two\n")
	writeAppLogTestFile(t, dir, "error.log", "error one\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Sources(context.Background(), AppLogSourcesRequest{NodeID: 7})
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if resp.NodeID != 7 {
		t.Fatalf("NodeID = %d, want 7", resp.NodeID)
	}

	if len(resp.Sources) != 4 {
		t.Fatalf("source count = %d, want 4", len(resp.Sources))
	}
	gotSources := make([]string, 0, len(resp.Sources))
	for _, source := range resp.Sources {
		gotSources = append(gotSources, source.Name)
		if source.Path != "" {
			t.Fatalf("source %s exposed path %q", source.Name, source.Path)
		}
	}
	wantSources := []string{
		AppLogSourceApp,
		AppLogSourceWarn,
		AppLogSourceError,
		AppLogSourceDebug,
	}
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("sources = %v, want %v", gotSources, wantSources)
	}

	app := resp.Sources[0]
	if app.File != "app.log" || !app.Available || app.SizeBytes == 0 || app.ModifiedAt.IsZero() {
		t.Fatalf("app source = %+v, want available app.log with size and modified time", app)
	}
	warn := resp.Sources[1]
	if warn.File != "warn.log" || warn.Available || warn.SizeBytes != 0 || !warn.ModifiedAt.IsZero() {
		t.Fatalf("warn source = %+v, want unavailable warn.log with zero metadata", warn)
	}
}

func TestAppLogReaderRejectsUnknownSource(t *testing.T) {
	reader := NewAppLogReader(AppLogReaderOptions{Dir: t.TempDir()})

	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{Source: "server"})
	if !errors.Is(err, ErrAppLogInvalidSource) {
		t.Fatalf("Entries() error = %v, want ErrAppLogInvalidSource", err)
	}
}

func TestAppLogReaderMissingFile(t *testing.T) {
	reader := NewAppLogReader(AppLogReaderOptions{Dir: t.TempDir()})

	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{Source: AppLogSourceWarn})
	if !errors.Is(err, ErrAppLogNotFound) {
		t.Fatalf("Entries() error = %v, want ErrAppLogNotFound", err)
	}
}

func TestAppLogReaderRejectsIncompleteCursor(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "")
	sourceOnlyCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"source":"app"}`))

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{Cursor: sourceOnlyCursor})
	if !errors.Is(err, ErrAppLogInvalidCursor) {
		t.Fatalf("Entries() error = %v, want ErrAppLogInvalidCursor", err)
	}

	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Entries() empty file error = %v", err)
	}
	if resp.Cursor == "" {
		t.Fatal("empty file cursor is empty")
	}
	next, err := reader.Entries(context.Background(), AppLogEntriesRequest{Cursor: resp.Cursor, Limit: 10})
	if err != nil {
		t.Fatalf("Entries() generated empty file cursor error = %v", err)
	}
	if next.Rotated || len(next.Items) != 0 {
		t.Fatalf("generated empty file cursor response = %+v, want no rotation and no items", next)
	}
}

func TestAppLogReaderTailAndForwardCursor(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "one\ntwo\nthree\nfour\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	first, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 9, Limit: 2})
	if err != nil {
		t.Fatalf("Entries() tail error = %v", err)
	}
	if first.NodeID != 9 || first.Source != AppLogSourceApp {
		t.Fatalf("response identity = node %d source %q, want node 9 source app", first.NodeID, first.Source)
	}
	if got := rawAppLogLines(first.Items); !reflect.DeepEqual(got, []string{"three", "four"}) {
		t.Fatalf("tail entries = %v, want [three four]", got)
	}
	if got := entrySeqsAndOffsets(first.Items); !reflect.DeepEqual(got, [][2]uint64{{8, 8}, {14, 14}}) {
		t.Fatalf("tail seq/offset = %v, want [[8 8] [14 14]]", got)
	}
	if first.Cursor == "" {
		t.Fatal("tail cursor is empty")
	}

	appendAppLogTestFile(t, dir, "app.log", "five\nsix\n")
	next, err := reader.Entries(context.Background(), AppLogEntriesRequest{Cursor: first.Cursor, Limit: 10})
	if err != nil {
		t.Fatalf("Entries() forward error = %v", err)
	}
	if got := rawAppLogLines(next.Items); !reflect.DeepEqual(got, []string{"five", "six"}) {
		t.Fatalf("forward entries = %v, want [five six]", got)
	}
	if got := entrySeqsAndOffsets(next.Items); !reflect.DeepEqual(got, [][2]uint64{{19, 19}, {24, 24}}) {
		t.Fatalf("forward seq/offset = %v, want [[19 19] [24 24]]", got)
	}
	if next.Rotated {
		t.Fatal("forward read reported rotation")
	}
}

func TestAppLogReaderReturnsContextAroundCursor(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "one\ntwo\nthree\nfour\nfive\n")
	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	cursor := mustAppLogTestCursor(t, dir, int64(len("one\ntwo\nthree\n")))

	result, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Source: AppLogSourceApp, Cursor: cursor, Before: 2, After: 1,
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if got := rawAppLogLines(result.Items); !reflect.DeepEqual(got, []string{"two", "three", "four"}) {
		t.Fatalf("context entries = %v, want [two three four]", got)
	}
}

func TestAppLogReaderContextRequiresCursor(t *testing.T) {
	reader := NewAppLogReader(AppLogReaderOptions{Dir: t.TempDir()})
	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{Before: 1, After: 1})
	if !errors.Is(err, ErrAppLogInvalidCursor) {
		t.Fatalf("Entries() error = %v, want ErrAppLogInvalidCursor", err)
	}
}

func TestAppLogReaderDetectsRotation(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "old-one\nold-two\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	first, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Entries() tail error = %v", err)
	}

	writeAppLogTestFile(t, dir, "app.log", "new-one\n")
	next, err := reader.Entries(context.Background(), AppLogEntriesRequest{Cursor: first.Cursor, Limit: 10})
	if err != nil {
		t.Fatalf("Entries() after rotation error = %v", err)
	}
	if !next.Rotated {
		t.Fatal("Rotated = false, want true")
	}
	if got := rawAppLogLines(next.Items); !reflect.DeepEqual(got, []string{"new-one"}) {
		t.Fatalf("rotation entries = %v, want [new-one]", got)
	}
	if got := entrySeqsAndOffsets(next.Items); !reflect.DeepEqual(got, [][2]uint64{{0, 0}}) {
		t.Fatalf("rotation seq/offset = %v, want [[0 0]]", got)
	}
}

func TestAppLogReaderTailSeqUsesOffsetWithoutEarlierContent(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "prefix-one\nprefix-two\nvisible-one\nvisible-two\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxTailScanBytes: int64(len("visible-one\nvisible-two\n"))})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 2})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if got := rawAppLogLines(resp.Items); !reflect.DeepEqual(got, []string{"visible-one", "visible-two"}) {
		t.Fatalf("tail entries = %v, want visible tail only", got)
	}
	if got := entrySeqsAndOffsets(resp.Items); !reflect.DeepEqual(got, [][2]uint64{{22, 22}, {34, 34}}) {
		t.Fatalf("tail seq/offset = %v, want offsets without line-count scan", got)
	}
}

func TestAppLogReaderParsesJSONEntry(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", `{"time":"2026-06-17 12:00:00.000","level":"INFO","module":"cluster","caller":"app/server.go:10","msg":"started","nodeID":1,"ready":true}`+"\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	wantTime := time.Date(2026, 6, 17, 12, 0, 0, 0, time.Local)
	if !entry.Time.Equal(wantTime) ||
		entry.Level != "INFO" ||
		entry.Module != "cluster" ||
		entry.Caller != "app/server.go:10" ||
		entry.Message != "started" {
		t.Fatalf("parsed entry = %+v, want time %v", entry, wantTime)
	}
	if entry.Fields["nodeID"] != float64(1) || entry.Fields["ready"] != true {
		t.Fatalf("remaining fields = %#v, want nodeID and ready", entry.Fields)
	}
}

func TestAppLogReaderParsesOversizedJSONBeforeTruncatingRaw(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", `{"time":"2026-06-17 12:00:00.000","level":"ERROR","module":"cluster","caller":"app/server.go:10","msg":"oversized json still parses","payload":"`+strings.Repeat("x", 128)+`"}`+"\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxLineBytes: 32})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Limit:  10,
		Levels: []string{"ERROR"},
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	if !entry.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if len(entry.Raw) != 32 {
		t.Fatalf("raw length = %d, want 32", len(entry.Raw))
	}
	if entry.Level != "ERROR" || entry.Message != "oversized json still parses" {
		t.Fatalf("parsed fields = level %q message %q, want ERROR and message from full line", entry.Level, entry.Message)
	}
}

func TestAppLogReaderParsesConsoleEntryWithoutModule(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "2026-06-17 12:00:00.000\tINFO\tapp/server.go:10\tstarted\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	if entry.Level != "INFO" ||
		entry.Module != "" ||
		entry.Caller != "app/server.go:10" ||
		entry.Message != "started" {
		t.Fatalf("parsed console entry = %+v, want empty module with caller and message", entry)
	}
}

func TestAppLogReaderParsesConsoleEntryWithoutModuleWithFields(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "2026-06-17 12:00:00.000\tINFO\tapp/server.go:10\tstarted\t{\"node\":1}\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	if entry.Level != "INFO" ||
		entry.Module != "" ||
		entry.Caller != "app/server.go:10" ||
		entry.Message != "started" {
		t.Fatalf("parsed console entry = %+v, want empty module with caller and message", entry)
	}
	if entry.Fields["extra"] != `{"node":1}` {
		t.Fatalf("extra field = %#v, want structured console fields", entry.Fields["extra"])
	}
}

func TestAppLogReaderParsesOversizedConsoleBeforeTruncatingRaw(t *testing.T) {
	dir := t.TempDir()
	message := "oversized console still parses " + strings.Repeat("x", 96)
	writeAppLogTestFile(t, dir, "app.log", "2026-06-17 12:00:00.000\tWARN\tapp/server.go:10\t"+message+"\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxLineBytes: 32})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Limit:  10,
		Levels: []string{"WARN"},
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	if !entry.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if len(entry.Raw) != 32 {
		t.Fatalf("raw length = %d, want 32", len(entry.Raw))
	}
	if entry.Level != "WARN" ||
		entry.Caller != "app/server.go:10" ||
		entry.Message != message {
		t.Fatalf("parsed console entry = %+v, want fields from full line", entry)
	}
}

func TestAppLogReaderBlankLevelFilterIsIgnored(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", "2026-06-17 12:00:00.000\tINFO\tapp/server.go:10\tstarted\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Limit:  10,
		Levels: []string{" "},
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if got := rawAppLogLines(resp.Items); !reflect.DeepEqual(got, []string{"2026-06-17 12:00:00.000\tINFO\tapp/server.go:10\tstarted"}) {
		t.Fatalf("entries = %v, want unfiltered INFO line", got)
	}
}

func TestAppLogReaderLongUnterminatedLineUsesScanBudget(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", strings.Repeat("x", 64))

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxTailScanBytes: 16, MaxLineBytes: 8})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Cursor: mustAppLogTestCursor(t, dir, 0),
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}
	entry := resp.Items[0]
	if entry.Raw != "xxxxxxxx" || !entry.Truncated {
		t.Fatalf("entry = %+v, want bounded truncated raw", entry)
	}
	if got := mustDecodeAppLogTestCursorOffset(t, resp.Cursor); got != 16 {
		t.Fatalf("cursor offset = %d, want 16", got)
	}
}

func TestAppLogReaderSparseFilterAdvancesByScanBudget(t *testing.T) {
	dir := t.TempDir()
	writeAppLogTestFile(t, dir, "app.log", strings.Repeat("INFO no match\n", 8))

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxTailScanBytes: 24})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Cursor:  mustAppLogTestCursor(t, dir, 0),
		Limit:   10,
		Keyword: "missing",
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("entry count = %d, want 0", len(resp.Items))
	}
	if got := mustDecodeAppLogTestCursorOffset(t, resp.Cursor); got != 24 {
		t.Fatalf("cursor offset = %d, want 24", got)
	}
}

func TestAppLogReaderKeywordMatchesFullLineBeyondTruncatedRaw(t *testing.T) {
	dir := t.TempDir()
	keyword := "needle-beyond-raw"
	message := "oversized keyword search " + strings.Repeat("x", 96) + keyword
	writeAppLogTestFile(t, dir, "app.log", "2026-06-17 12:00:00.000\tINFO\tapp/server.go:10\t"+message+"\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir, MaxLineBytes: 32})
	resp, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		Limit:   10,
		Keyword: keyword,
	})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("entry count = %d, want 1", len(resp.Items))
	}

	entry := resp.Items[0]
	if !entry.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if strings.Contains(entry.Raw, keyword) {
		t.Fatalf("Raw contains keyword %q despite truncation: %q", keyword, entry.Raw)
	}
	if entry.Message != message {
		t.Fatalf("Message = %q, want full message", entry.Message)
	}
}

func writeAppLogTestFile(t *testing.T, dir, filename, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", filename, err)
	}
}

func appendAppLogTestFile(t *testing.T, dir, filename, contents string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(dir, filename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%s) error = %v", filename, err)
	}
	defer file.Close()
	if _, err := file.WriteString(contents); err != nil {
		t.Fatalf("WriteString(%s) error = %v", filename, err)
	}
}

func mustAppLogTestCursor(t *testing.T, dir string, offset int64) string {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("Open(app.log) error = %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("Stat(app.log) error = %v", err)
	}
	digestStart, digest, err := digestAppLogWindow(file, offset)
	if err != nil {
		t.Fatalf("digestAppLogWindow() error = %v", err)
	}
	cursor, err := encodeAppLogCursor(appLogCursor{
		Source:      AppLogSourceApp,
		Offset:      offset,
		Identity:    fileIdentity(info),
		DigestStart: digestStart,
		Digest:      digest,
	})
	if err != nil {
		t.Fatalf("encodeAppLogCursor() error = %v", err)
	}
	return cursor
}

func mustDecodeAppLogTestCursorOffset(t *testing.T, cursor string) int64 {
	t.Helper()
	decoded, err := decodeAppLogCursor(cursor)
	if err != nil {
		t.Fatalf("decodeAppLogCursor() error = %v", err)
	}
	return decoded.Offset
}

func rawAppLogLines(entries []AppLogEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.Raw)
	}
	return lines
}

func entrySeqsAndOffsets(entries []AppLogEntry) [][2]uint64 {
	values := make([][2]uint64, 0, len(entries))
	for _, entry := range entries {
		values = append(values, [2]uint64{entry.Seq, entry.Offset})
	}
	return values
}
