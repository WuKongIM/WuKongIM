# wukongimv2 App Log Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a lightweight web manager view that tails ordinary `cmd/wukongimv2` application logs for one selected node at a time.

**Architecture:** Add a fixed-source node-local reader in `internalv2/log`, expose it through `internalv2/usecase/management`, route local and remote reads through manager node RPC, and serve JSON pages plus NDJSON streaming through the manager HTTP API. The web Diagnostics page gets an `app-logs` tab that loads an initial page and then streams new lines with `fetch` using the existing manager JWT flow.

**Tech Stack:** Go, gin, clusterv2 typed RPC, zap/lumberjack log files, React, TypeScript, Vite, Vitest.

---

## File Structure

Backend:

- Create `internalv2/log/app_reader.go` for source validation, cursor encoding, bounded tail reads, forward reads, rotation detection, and line parsing.
- Create `internalv2/log/app_reader_test.go` for file reader behavior.
- Create `internalv2/usecase/management/app_logs.go` for manager usecase request/response types and validation.
- Create `internalv2/usecase/management/app_logs_test.go` for usecase validation.
- Modify `internalv2/usecase/management/nodes.go` to add the `ApplicationLogs` port to `Options` and `App`.
- Modify `internalv2/access/manager/server.go` to add application log methods to the `Management` interface and register routes guarded by `cluster.log:r`.
- Create `internalv2/access/manager/app_logs.go` for JSON page and NDJSON stream handlers.
- Create `internalv2/access/manager/app_logs_test.go` for manager route parsing and response mapping.
- Create `internalv2/access/node/manager_app_logs_codec.go` for stable node RPC encoding.
- Create `internalv2/access/node/manager_app_logs_rpc.go` for node RPC handler and client methods.
- Create `internalv2/access/node/manager_app_logs_rpc_test.go` for RPC round trips and status mapping.
- Modify `internalv2/access/node/presence_rpc.go` to add the manager application log reader to `Options` and `Adapter`.
- Modify `internalv2/access/node/FLOW.md` for the new manager application log RPC flow.
- Modify `pkg/clusterv2/net/ids.go` and `pkg/clusterv2/net/ids_test.go` to add `RPCManagerAppLogs`.
- Create `internalv2/infra/cluster/management_app_logs.go` for local/remote selected-node routing.
- Create `internalv2/infra/cluster/management_app_logs_test.go` for local and remote routing.
- Modify `internalv2/infra/cluster/FLOW.md` to document selected-node application log reads.
- Create `internalv2/app/app_logs.go` for the composition-root adapter from `internalv2/log` DTOs to management DTOs.
- Modify `internalv2/app/wiring.go` to wire the local reader, remote reader, manager usecase, and node RPC.
- Modify `internalv2/app/FLOW.md` if the composition-root flow changes.

Web:

- Modify `web/src/lib/manager-api.types.ts` to add application log DTOs.
- Modify `web/src/lib/manager-api.ts` to add source/page/stream helpers.
- Modify `web/src/lib/manager-api.test.ts` for new helper URLs and headers.
- Create `web/src/pages/app-logs/page.tsx` for the App Logs panel.
- Create `web/src/pages/app-logs/page.test.tsx` for initial load, streaming, pause/resume, and rotation rendering.
- Modify `web/src/pages/cluster/diagnostics/page.tsx` to add the `app-logs` tab.
- Modify `web/src/app/router.tsx` and `web/src/lib/navigation.ts` to add `/app-logs` legacy redirect.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts` for labels.
- Modify `web/README.md` to update the page/API matrix.

---

### Task 1: Node-Local Application Log Reader

**Files:**
- Create: `internalv2/log/app_reader.go`
- Create: `internalv2/log/app_reader_test.go`
- Existing context: `internalv2/log/FLOW.md`

- [ ] **Step 1: Write failing tests for fixed-source validation and missing files**

Create `internalv2/log/app_reader_test.go` with:

```go
package log

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppLogReaderSourcesReportsFixedFiles(t *testing.T) {
	dir := t.TempDir()
	requireWriteFile(t, filepath.Join(dir, "app.log"), "one\n")
	requireWriteFile(t, filepath.Join(dir, "error.log"), "boom\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	resp, err := reader.Sources(context.Background(), AppLogSourcesRequest{NodeID: 1})
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}

	requireSource(t, resp.Sources, "app", true)
	requireSource(t, resp.Sources, "warn", false)
	requireSource(t, resp.Sources, "error", true)
	requireSource(t, resp.Sources, "debug", false)
}

func TestAppLogReaderRejectsUnknownSource(t *testing.T) {
	reader := NewAppLogReader(AppLogReaderOptions{Dir: t.TempDir()})
	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		NodeID: 1,
		Source: "raft",
		Limit: 10,
	})
	if !errors.Is(err, ErrAppLogInvalidSource) {
		t.Fatalf("Entries() error = %v, want ErrAppLogInvalidSource", err)
	}
}

func TestAppLogReaderMissingFile(t *testing.T) {
	reader := NewAppLogReader(AppLogReaderOptions{Dir: t.TempDir()})
	_, err := reader.Entries(context.Background(), AppLogEntriesRequest{
		NodeID: 1,
		Source: AppLogSourceDebug,
		Limit: 10,
	})
	if !errors.Is(err, ErrAppLogNotFound) {
		t.Fatalf("Entries() error = %v, want ErrAppLogNotFound", err)
	}
}

func requireWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireSource(t *testing.T, sources []AppLogSource, name string, available bool) {
	t.Helper()
	for _, source := range sources {
		if source.Name == name {
			if source.Available != available {
				t.Fatalf("source %s available = %v, want %v", name, source.Available, available)
			}
			if source.Path != "" {
				t.Fatalf("source %s Path = %q, want hidden path", name, source.Path)
			}
			return
		}
	}
	t.Fatalf("missing source %s in %#v", name, sources)
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
go test ./internalv2/log -run 'TestAppLogReader(SourcesReportsFixedFiles|RejectsUnknownSource|MissingFile)' -count=1
```

Expected: FAIL because `NewAppLogReader`, request/response types, and errors are undefined.

- [ ] **Step 3: Add reader types, source validation, and source metadata**

Create `internalv2/log/app_reader.go` with:

```go
package log

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// AppLogSourceApp is the primary application log containing info and above.
	AppLogSourceApp = "app"
	// AppLogSourceWarn is the warning-only application log.
	AppLogSourceWarn = "warn"
	// AppLogSourceError is the error application log.
	AppLogSourceError = "error"
	// AppLogSourceDebug is the debug application log created when debug logging is enabled.
	AppLogSourceDebug = "debug"

	defaultAppLogPageLimit      = 200
	maxAppLogPageLimit          = 1000
	defaultAppLogMaxLineBytes   = 64 * 1024
	defaultAppLogTailScanBytes  = 8 * 1024 * 1024
	appLogCursorPrefix          = "v1:"
)

var (
	// ErrAppLogInvalidSource reports an unknown fixed application log source.
	ErrAppLogInvalidSource = errors.New("internalv2/log: invalid application log source")
	// ErrAppLogNotFound reports that the selected fixed application log file does not exist.
	ErrAppLogNotFound = errors.New("internalv2/log: application log not found")
	// ErrAppLogInvalidCursor reports an unreadable or mismatched application log cursor.
	ErrAppLogInvalidCursor = errors.New("internalv2/log: invalid application log cursor")
)

// AppLogReaderOptions configures node-local fixed-source application log reads.
type AppLogReaderOptions struct {
	// Dir is the WK_LOG_DIR directory containing app.log, warn.log, error.log, and debug.log.
	Dir string
	// MaxLineBytes bounds one returned log line.
	MaxLineBytes int
	// MaxTailScanBytes bounds bytes scanned to hydrate an initial tail page.
	MaxTailScanBytes int64
}

// AppLogReader reads fixed application log files from one node-local log directory.
type AppLogReader struct {
	dir              string
	maxLineBytes     int
	maxTailScanBytes int64
}

// AppLogSourcesRequest selects source metadata for one node.
type AppLogSourcesRequest struct {
	// NodeID is echoed for manager selected-node responses.
	NodeID uint64
}

// AppLogSourcesResponse describes fixed application log sources for one node.
type AppLogSourcesResponse struct {
	// NodeID is the selected node.
	NodeID uint64
	// Sources contains fixed source metadata sorted by display order.
	Sources []AppLogSource
}

// AppLogSource describes one fixed application log source.
type AppLogSource struct {
	// Name is the fixed source identifier: app, warn, error, or debug.
	Name string
	// File is the fixed filename under WK_LOG_DIR.
	File string
	// Available reports whether the current active file exists.
	Available bool
	// SizeBytes is the current active file size.
	SizeBytes int64
	// ModifiedAt is the current active file modification time.
	ModifiedAt time.Time
	// Path is intentionally empty for the manager API; it prevents accidental path disclosure.
	Path string
}

// AppLogEntriesRequest selects a bounded application log page.
type AppLogEntriesRequest struct {
	// NodeID is echoed for manager selected-node responses.
	NodeID uint64
	// Source is the fixed source identifier.
	Source string
	// Limit bounds returned entries. Zero uses the default.
	Limit int
	// Cursor is an opaque cursor returned by a previous page.
	Cursor string
	// Keyword filters returned raw lines after the bounded read.
	Keyword string
	// Levels filters parsed log levels after the bounded read.
	Levels []string
}

// AppLogEntriesResponse is one bounded application log page.
type AppLogEntriesResponse struct {
	// NodeID is the selected node.
	NodeID uint64
	// Source is the selected fixed source.
	Source string
	// Cursor is the cursor at the end of this page.
	Cursor string
	// Rotated reports whether the supplied cursor no longer matched the active file.
	Rotated bool
	// Items contains log entries in file order.
	Items []AppLogEntry
}

// AppLogEntry is one manager-facing application log line.
type AppLogEntry struct {
	// Seq is the byte offset where this line starts. It is stable within the active file.
	Seq uint64
	// Offset is the byte offset where this line starts.
	Offset uint64
	// Time is parsed from structured logs when available.
	Time time.Time
	// Level is the parsed log level when available.
	Level string
	// Module is the parsed zap logger name when available.
	Module string
	// Caller is the parsed caller when available.
	Caller string
	// Message is the parsed message when available.
	Message string
	// Fields contains parsed structured fields other than common display columns.
	Fields map[string]any
	// Raw is the original line without the trailing newline.
	Raw string
	// Truncated reports whether Raw was shortened to MaxLineBytes.
	Truncated bool
}

type appLogCursor struct {
	Source    string `json:"source"`
	Offset    int64  `json:"offset"`
	Size      int64  `json:"size"`
	Modified  int64  `json:"modified"`
}

// NewAppLogReader creates a node-local fixed-source application log reader.
func NewAppLogReader(opts AppLogReaderOptions) *AppLogReader {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir = defaultDir
	}
	maxLineBytes := opts.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultAppLogMaxLineBytes
	}
	maxTailScanBytes := opts.MaxTailScanBytes
	if maxTailScanBytes <= 0 {
		maxTailScanBytes = defaultAppLogTailScanBytes
	}
	return &AppLogReader{dir: dir, maxLineBytes: maxLineBytes, maxTailScanBytes: maxTailScanBytes}
}

// Sources returns metadata for the fixed application log sources.
func (r *AppLogReader) Sources(ctx context.Context, req AppLogSourcesRequest) (AppLogSourcesResponse, error) {
	if err := ctx.Err(); err != nil {
		return AppLogSourcesResponse{}, err
	}
	out := AppLogSourcesResponse{NodeID: req.NodeID}
	for _, name := range fixedAppLogSources() {
		file := appLogFileForSource(name)
		info, err := os.Stat(r.pathForSource(name))
		source := AppLogSource{Name: name, File: file}
		if err == nil {
			source.Available = true
			source.SizeBytes = info.Size()
			source.ModifiedAt = info.ModTime()
		} else if !os.IsNotExist(err) {
			return AppLogSourcesResponse{}, err
		}
		out.Sources = append(out.Sources, source)
	}
	return out, nil
}

func fixedAppLogSources() []string {
	return []string{AppLogSourceApp, AppLogSourceWarn, AppLogSourceError, AppLogSourceDebug}
}

func appLogFileForSource(source string) string {
	switch source {
	case AppLogSourceApp:
		return "app.log"
	case AppLogSourceWarn:
		return "warn.log"
	case AppLogSourceError:
		return "error.log"
	case AppLogSourceDebug:
		return "debug.log"
	default:
		return ""
	}
}

func normalizeAppLogSource(source string) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = AppLogSourceApp
	}
	if appLogFileForSource(source) == "" {
		return "", ErrAppLogInvalidSource
	}
	return source, nil
}

func (r *AppLogReader) pathForSource(source string) string {
	return filepath.Join(r.dir, appLogFileForSource(source))
}
```

- [ ] **Step 4: Run source tests and confirm they pass**

Run:

```bash
go test ./internalv2/log -run 'TestAppLogReader(SourcesReportsFixedFiles|RejectsUnknownSource|MissingFile)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing tests for tail, forward cursor, rotation, and JSON parsing**

Append to `internalv2/log/app_reader_test.go`:

```go
func TestAppLogReaderTailAndForwardCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	requireWriteFile(t, path, "first\nsecond\nthird\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	firstPage, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 1, Source: AppLogSourceApp, Limit: 2})
	if err != nil {
		t.Fatalf("tail Entries() error = %v", err)
	}
	if got := rawLines(firstPage.Items); len(got) != 2 || got[0] != "second" || got[1] != "third" {
		t.Fatalf("tail lines = %#v, want second/third", got)
	}
	if firstPage.Cursor == "" {
		t.Fatal("tail cursor is empty")
	}

	appendFile(t, path, "fourth\n")
	nextPage, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 1, Source: AppLogSourceApp, Limit: 10, Cursor: firstPage.Cursor})
	if err != nil {
		t.Fatalf("forward Entries() error = %v", err)
	}
	if got := rawLines(nextPage.Items); len(got) != 1 || got[0] != "fourth" {
		t.Fatalf("forward lines = %#v, want fourth", got)
	}
}

func TestAppLogReaderDetectsRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	requireWriteFile(t, path, "before\n")
	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	page, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 1, Source: AppLogSourceApp, Limit: 10})
	if err != nil {
		t.Fatalf("initial Entries() error = %v", err)
	}

	requireWriteFile(t, path, "after\n")
	rotated, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 1, Source: AppLogSourceApp, Limit: 10, Cursor: page.Cursor})
	if err != nil {
		t.Fatalf("rotated Entries() error = %v", err)
	}
	if !rotated.Rotated {
		t.Fatal("Rotated = false, want true")
	}
	if got := rawLines(rotated.Items); len(got) != 1 || got[0] != "after" {
		t.Fatalf("rotated lines = %#v, want after", got)
	}
}

func TestAppLogReaderParsesJSONEntry(t *testing.T) {
	dir := t.TempDir()
	requireWriteFile(t, filepath.Join(dir, "app.log"), `{"time":"2026-06-17 10:15:30.123","level":"INFO","module":"access.gateway","caller":"gateway/handler.go:42","msg":"session started","event":"internalv2.access.gateway.session_started"}`+"\n")

	reader := NewAppLogReader(AppLogReaderOptions{Dir: dir})
	page, err := reader.Entries(context.Background(), AppLogEntriesRequest{NodeID: 1, Source: AppLogSourceApp, Limit: 10})
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(page.Items))
	}
	item := page.Items[0]
	if item.Level != "INFO" || item.Module != "access.gateway" || item.Message != "session started" {
		t.Fatalf("parsed item = %#v", item)
	}
	if item.Fields["event"] != "internalv2.access.gateway.session_started" {
		t.Fatalf("event field = %#v", item.Fields["event"])
	}
}

func rawLines(items []AppLogEntry) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Raw)
	}
	return out
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}
```

- [ ] **Step 6: Run tests and confirm they fail**

Run:

```bash
go test ./internalv2/log -run 'TestAppLogReader(TailAndForwardCursor|DetectsRotation|ParsesJSONEntry)' -count=1
```

Expected: FAIL because `Entries` and parsing helpers are still missing before Step 7.

- [ ] **Step 7: Implement bounded tail, cursor, forward reads, and parsing**

Append to `internalv2/log/app_reader.go`:

```go
// Entries returns a bounded page from a fixed application log source.
func (r *AppLogReader) Entries(ctx context.Context, req AppLogEntriesRequest) (AppLogEntriesResponse, error) {
	if err := ctx.Err(); err != nil {
		return AppLogEntriesResponse{}, err
	}
	source, err := normalizeAppLogSource(req.Source)
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	limit := normalizeAppLogLimit(req.Limit)
	path := r.pathForSource(source)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AppLogEntriesResponse{}, ErrAppLogNotFound
		}
		return AppLogEntriesResponse{}, err
	}

	var cursor appLogCursor
	var start int64
	var rotated bool
	if strings.TrimSpace(req.Cursor) == "" {
		start, err = r.tailStart(path, info.Size(), limit)
		if err != nil {
			return AppLogEntriesResponse{}, err
		}
	} else {
		cursor, err = decodeAppLogCursor(req.Cursor)
		if err != nil {
			return AppLogEntriesResponse{}, err
		}
		if cursor.Source != source {
			return AppLogEntriesResponse{}, ErrAppLogInvalidCursor
		}
		if info.Size() < cursor.Offset || info.ModTime().UnixNano() < cursor.Modified {
			rotated = true
			start = 0
		} else {
			start = cursor.Offset
		}
	}

	items, nextOffset, err := r.readForward(ctx, path, start, limit, req)
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	nextCursor := encodeAppLogCursor(appLogCursor{
		Source: source,
		Offset: nextOffset,
		Size: info.Size(),
		Modified: info.ModTime().UnixNano(),
	})
	return AppLogEntriesResponse{
		NodeID: req.NodeID,
		Source: source,
		Cursor: nextCursor,
		Rotated: rotated,
		Items: items,
	}, nil
}

func normalizeAppLogLimit(limit int) int {
	if limit <= 0 {
		return defaultAppLogPageLimit
	}
	if limit > maxAppLogPageLimit {
		return maxAppLogPageLimit
	}
	return limit
}

func (r *AppLogReader) tailStart(path string, size int64, limit int) (int64, error) {
	if size <= 0 {
		return 0, nil
	}
	scan := r.maxTailScanBytes
	if scan > size {
		scan = size
	}
	start := size - scan
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	data := make([]byte, scan)
	n, err := io.ReadFull(file, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, err
	}
	data = data[:n]
	newlines := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			newlines++
			if newlines > limit {
				return start + int64(i+1), nil
			}
		}
	}
	return start, nil
}

func (r *AppLogReader) readForward(ctx context.Context, path string, start int64, limit int, req AppLogEntriesRequest) ([]AppLogEntry, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, 0, err
	}
	reader := bufio.NewReader(file)
	offset := start
	items := make([]AppLogEntry, 0, limit)
	for len(items) < limit {
		if err := ctx.Err(); err != nil {
			return nil, offset, err
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, offset, readErr
		}
		if line == "" && errors.Is(readErr, io.EOF) {
			break
		}
		lineStart := offset
		offset += int64(len(line))
		raw := strings.TrimRight(line, "\r\n")
		entry := parseAppLogLine(raw)
		entry.Seq = uint64(lineStart)
		entry.Offset = uint64(lineStart)
		if len(entry.Raw) > r.maxLineBytes {
			entry.Raw = entry.Raw[:r.maxLineBytes]
			entry.Truncated = true
		}
		if appLogEntryMatches(entry, req) {
			items = append(items, entry)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return items, offset, nil
}

func appLogEntryMatches(entry AppLogEntry, req AppLogEntriesRequest) bool {
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" && !strings.Contains(entry.Raw, keyword) {
		return false
	}
	if len(req.Levels) == 0 {
		return true
	}
	want := map[string]struct{}{}
	for _, level := range req.Levels {
		level = strings.ToUpper(strings.TrimSpace(level))
		if level != "" {
			want[level] = struct{}{}
		}
	}
	if len(want) == 0 {
		return true
	}
	_, ok := want[strings.ToUpper(entry.Level)]
	return ok
}

func parseAppLogLine(raw string) AppLogEntry {
	entry := AppLogEntry{Raw: raw}
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) == nil {
		entry.Level = stringField(obj, "level")
		entry.Module = stringField(obj, "module")
		entry.Caller = stringField(obj, "caller")
		entry.Message = stringField(obj, "msg")
		if ts := stringField(obj, "time"); ts != "" {
			if parsed, err := time.ParseInLocation("2006-01-02 15:04:05.000", ts, time.Local); err == nil {
				entry.Time = parsed
			}
		}
		fields := make(map[string]any, len(obj))
		for key, value := range obj {
			if key == "time" || key == "level" || key == "module" || key == "caller" || key == "msg" {
				continue
			}
			fields[key] = value
		}
		if len(fields) > 0 {
			entry.Fields = fields
		}
		return entry
	}
	parts := strings.Split(raw, "\t")
	if len(parts) >= 4 {
		if parsed, err := time.ParseInLocation("2006-01-02 15:04:05.000", parts[0], time.Local); err == nil {
			entry.Time = parsed
		}
		entry.Level = strings.TrimSpace(parts[1])
		entry.Module = strings.Trim(strings.TrimSpace(parts[2]), "[]")
		entry.Caller = strings.TrimSpace(parts[3])
		if len(parts) >= 5 {
			entry.Message = strings.Join(parts[4:], "\t")
		}
	}
	return entry
}

func stringField(obj map[string]any, key string) string {
	if value, ok := obj[key].(string); ok {
		return value
	}
	return ""
}

func encodeAppLogCursor(cursor appLogCursor) string {
	body, _ := json.Marshal(cursor)
	return appLogCursorPrefix + base64.RawURLEncoding.EncodeToString(body)
}

func decodeAppLogCursor(raw string) (appLogCursor, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, appLogCursorPrefix) {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, appLogCursorPrefix))
	if err != nil {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	var cursor appLogCursor
	if err := json.Unmarshal(body, &cursor); err != nil {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	if _, err := normalizeAppLogSource(cursor.Source); err != nil || cursor.Offset < 0 {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	return cursor, nil
}

func sortedAppLogLevels(levels []string) []string {
	out := append([]string(nil), levels...)
	sort.Strings(out)
	return out
}
```

- [ ] **Step 8: Run all log reader tests**

Run:

```bash
go test ./internalv2/log -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

Run:

```bash
git add internalv2/log/app_reader.go internalv2/log/app_reader_test.go
git commit -m "feat: add wukongimv2 app log reader"
```

---

### Task 2: Management Usecase Port

**Files:**
- Create: `internalv2/usecase/management/app_logs.go`
- Create: `internalv2/usecase/management/app_logs_test.go`
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/usecase/management/FLOW.md`

- [ ] **Step 1: Write failing usecase validation tests**

Create `internalv2/usecase/management/app_logs_test.go` with:

```go
package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestApplicationLogEntriesValidatesNodeAndLimit(t *testing.T) {
	app := New(Options{ApplicationLogs: fakeApplicationLogs{}})
	_, err := app.ApplicationLogEntries(context.Background(), ApplicationLogEntriesRequest{NodeID: 0, Source: "app"})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("zero node error = %v, want invalid argument", err)
	}
	_, err = app.ApplicationLogEntries(context.Background(), ApplicationLogEntriesRequest{NodeID: 1, Source: "app", Limit: -1})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("negative limit error = %v, want invalid argument", err)
	}
}

func TestApplicationLogEntriesRequiresReader(t *testing.T) {
	app := New(Options{})
	_, err := app.ApplicationLogEntries(context.Background(), ApplicationLogEntriesRequest{NodeID: 1, Source: "app"})
	if !errors.Is(err, ErrApplicationLogReaderUnavailable) {
		t.Fatalf("error = %v, want ErrApplicationLogReaderUnavailable", err)
	}
}

func TestApplicationLogSourcesDelegates(t *testing.T) {
	reader := &recordingApplicationLogs{}
	app := New(Options{ApplicationLogs: reader})
	_, err := app.ApplicationLogSources(context.Background(), ApplicationLogSourcesRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("ApplicationLogSources() error = %v", err)
	}
	if reader.sourcesReq.NodeID != 2 {
		t.Fatalf("NodeID = %d, want 2", reader.sourcesReq.NodeID)
	}
}

type recordingApplicationLogs struct {
	sourcesReq ApplicationLogSourcesRequest
}

func (r *recordingApplicationLogs) ApplicationLogSources(_ context.Context, req ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error) {
	r.sourcesReq = req
	return ApplicationLogSourcesResponse{NodeID: req.NodeID}, nil
}

func (r *recordingApplicationLogs) ApplicationLogEntries(_ context.Context, req ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error) {
	return ApplicationLogEntriesResponse{NodeID: req.NodeID, Source: req.Source}, nil
}

type fakeApplicationLogs struct{}

func (fakeApplicationLogs) ApplicationLogSources(context.Context, ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error) {
	return ApplicationLogSourcesResponse{}, nil
}

func (fakeApplicationLogs) ApplicationLogEntries(context.Context, ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error) {
	return ApplicationLogEntriesResponse{}, nil
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestApplicationLog' -count=1
```

Expected: FAIL because the application log port and methods are undefined.

- [ ] **Step 3: Add management usecase types and methods**

Create `internalv2/usecase/management/app_logs.go` with:

```go
package management

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrApplicationLogReaderUnavailable reports that application log inspection is not wired.
var ErrApplicationLogReaderUnavailable = errors.New("internalv2/usecase/management: application log reader unavailable")

// ApplicationLogReader exposes selected-node ordinary application log pages.
type ApplicationLogReader interface {
	// ApplicationLogSources returns fixed application log source metadata for one node.
	ApplicationLogSources(context.Context, ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error)
	// ApplicationLogEntries returns one bounded page from one fixed application log source.
	ApplicationLogEntries(context.Context, ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error)
}

// ApplicationLogSourcesRequest selects source metadata for one node.
type ApplicationLogSourcesRequest struct {
	// NodeID is the node whose local application log sources should be described.
	NodeID uint64
}

// ApplicationLogSourcesResponse describes fixed application log sources for one node.
type ApplicationLogSourcesResponse struct {
	// NodeID is the selected node.
	NodeID uint64
	// Sources contains fixed application log source metadata.
	Sources []ApplicationLogSource
}

// ApplicationLogSource describes one fixed application log source.
type ApplicationLogSource struct {
	// Name is the fixed source identifier.
	Name string
	// File is the fixed filename under the node log directory.
	File string
	// Available reports whether the active file exists.
	Available bool
	// SizeBytes is the active file size.
	SizeBytes int64
	// ModifiedAt is the active file modification time.
	ModifiedAt time.Time
}

// ApplicationLogEntriesRequest selects one bounded application log page.
type ApplicationLogEntriesRequest struct {
	// NodeID is the node whose local application log should be read.
	NodeID uint64
	// Source is the fixed source identifier.
	Source string
	// Limit bounds returned entries.
	Limit int
	// Cursor is an opaque page cursor.
	Cursor string
	// Keyword filters raw lines inside the bounded page.
	Keyword string
	// Levels filters parsed log levels inside the bounded page.
	Levels []string
}

// ApplicationLogEntriesResponse is one bounded application log page.
type ApplicationLogEntriesResponse struct {
	// NodeID is the selected node.
	NodeID uint64
	// Source is the selected fixed source.
	Source string
	// Cursor is the cursor for the next read.
	Cursor string
	// Rotated reports whether the supplied cursor crossed a log rotation.
	Rotated bool
	// Items contains log entries in file order.
	Items []ApplicationLogEntry
}

// ApplicationLogEntry is one manager-facing ordinary application log line.
type ApplicationLogEntry struct {
	// Seq is a stable per-file sequence derived from the byte offset.
	Seq uint64
	// Offset is the byte offset where the line starts.
	Offset uint64
	// Time is parsed from the log line when available.
	Time time.Time
	// Level is the parsed log level.
	Level string
	// Module is the parsed logger name.
	Module string
	// Caller is the parsed caller.
	Caller string
	// Message is the parsed message.
	Message string
	// Fields contains parsed structured fields.
	Fields map[string]any
	// Raw is the original line without the trailing newline.
	Raw string
	// Truncated reports whether Raw was shortened.
	Truncated bool
}

// ApplicationLogSources returns fixed application log source metadata for one selected node.
func (a *App) ApplicationLogSources(ctx context.Context, req ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplicationLogSourcesResponse{}, err
	}
	if req.NodeID == 0 {
		return ApplicationLogSourcesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.applicationLogs == nil {
		return ApplicationLogSourcesResponse{}, ErrApplicationLogReaderUnavailable
	}
	return a.applicationLogs.ApplicationLogSources(ctx, req)
}

// ApplicationLogEntries returns one bounded application log page for one selected node.
func (a *App) ApplicationLogEntries(ctx context.Context, req ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplicationLogEntriesResponse{}, err
	}
	if req.NodeID == 0 || req.Limit < 0 {
		return ApplicationLogEntriesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.applicationLogs == nil {
		return ApplicationLogEntriesResponse{}, ErrApplicationLogReaderUnavailable
	}
	return a.applicationLogs.ApplicationLogEntries(ctx, req)
}
```

Modify `internalv2/usecase/management/nodes.go`:

Insert this field in `Options` immediately after `Logs LogReader`:

```go
// ApplicationLogs reads selected-node ordinary application log pages.
ApplicationLogs ApplicationLogReader
```

Insert this field in `App` immediately after `logs LogReader`:

```go
applicationLogs ApplicationLogReader
```

Add this assignment to the struct literal returned by `New`, immediately after `logs: opts.Logs`:

```go
applicationLogs: opts.ApplicationLogs,
```

- [ ] **Step 4: Run usecase tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestApplicationLog' -count=1
```

Expected: PASS.

- [ ] **Step 5: Update management flow documentation**

Modify `internalv2/usecase/management/FLOW.md` by adding `application log pages` to the responsibility list and this flow:

```markdown
## Application Log Flow

```text
manager HTTP handler
  -> management.App.ApplicationLogSources/ApplicationLogEntries
  -> ApplicationLogReader
  -> local node fixed-source app.log/warn.log/error.log/debug.log reader or node RPC routed peer read
```

Application logs are ordinary process logs from `WK_LOG_DIR`; they are not
Controller, Slot, Channel, or Raft logs. The usecase validates selected node,
source, cursor, and limit shape, while fixed filename resolution stays in the
node-local reader.
```

- [ ] **Step 6: Commit**

Run:

```bash
git add internalv2/usecase/management/app_logs.go internalv2/usecase/management/app_logs_test.go internalv2/usecase/management/nodes.go internalv2/usecase/management/FLOW.md
git commit -m "feat: add management app log usecase"
```

---

### Task 3: Manager App Log Node RPC

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`
- Create: `internalv2/access/node/manager_app_logs_codec.go`
- Create: `internalv2/access/node/manager_app_logs_rpc.go`
- Create: `internalv2/access/node/manager_app_logs_rpc_test.go`
- Modify: `internalv2/access/node/presence_rpc.go`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Write failing RPC service ID test update**

Modify `pkg/clusterv2/net/ids_test.go` map:

```go
"manager_app_logs":       RPCManagerAppLogs,
```

Run:

```bash
go test ./pkg/clusterv2/net -run TestRPCServiceIDsAreUniqueAndNonZero -count=1
```

Expected: FAIL because `RPCManagerAppLogs` is undefined.

- [ ] **Step 2: Add the service ID**

Modify `pkg/clusterv2/net/ids.go` after `RPCManagerDBInspect`:

```go
// RPCManagerAppLogs serves internalv2 node-local ordinary application log reads.
RPCManagerAppLogs
```

Run:

```bash
go test ./pkg/clusterv2/net -run TestRPCServiceIDsAreUniqueAndNonZero -count=1
```

Expected: PASS.

- [ ] **Step 3: Write failing RPC round-trip tests**

Create `internalv2/access/node/manager_app_logs_rpc_test.go` with:

```go
package node

import (
	"context"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerAppLogRPCRoundTripEntries(t *testing.T) {
	service := &fakeManagerAppLogService{
		entries: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "app",
			Cursor: "v1:cursor",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq: 7,
				Offset: 7,
				Time: time.Unix(1, 2).UTC(),
				Level: "INFO",
				Module: "access.gateway",
				Caller: "gateway/handler.go:42",
				Message: "started",
				Fields: map[string]any{"event": "x"},
				Raw: "raw",
			}},
		},
	}
	adapter := New(Options{ManagerAppLogs: service})
	reqBody, err := encodeManagerAppLogRequest(managerAppLogRPCRequest{
		Op: "entries",
		NodeID: 2,
		Source: "app",
		Limit: 5,
		Cursor: "v1:before",
		Keyword: "started",
		Levels: []string{"INFO"},
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerAppLogRPC(context.Background(), reqBody)
	if err != nil {
		t.Fatalf("HandleManagerAppLogRPC() error = %v", err)
	}
	resp, err := decodeManagerAppLogResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || len(resp.Entries.Items) != 1 || resp.Entries.Items[0].Raw != "raw" {
		t.Fatalf("response = %#v", resp)
	}
	if service.entriesReq.NodeID != 2 || service.entriesReq.Source != "app" || service.entriesReq.Keyword != "started" {
		t.Fatalf("entries request = %#v", service.entriesReq)
	}
}

func TestManagerAppLogRPCRoundTripSources(t *testing.T) {
	service := &fakeManagerAppLogService{
		sources: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 2,
			Sources: []managementusecase.ApplicationLogSource{{Name: "app", File: "app.log", Available: true}},
		},
	}
	adapter := New(Options{ManagerAppLogs: service})
	reqBody, err := encodeManagerAppLogRequest(managerAppLogRPCRequest{Op: "sources", NodeID: 2})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerAppLogRPC(context.Background(), reqBody)
	if err != nil {
		t.Fatalf("HandleManagerAppLogRPC() error = %v", err)
	}
	resp, err := decodeManagerAppLogResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || len(resp.Sources.Sources) != 1 || resp.Sources.Sources[0].Name != "app" {
		t.Fatalf("response = %#v", resp)
	}
}

type fakeManagerAppLogService struct {
	sourcesReq managementusecase.ApplicationLogSourcesRequest
	entriesReq managementusecase.ApplicationLogEntriesRequest
	sources managementusecase.ApplicationLogSourcesResponse
	entries managementusecase.ApplicationLogEntriesResponse
}

func (f *fakeManagerAppLogService) ApplicationLogSources(_ context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	f.sourcesReq = req
	return f.sources, nil
}

func (f *fakeManagerAppLogService) ApplicationLogEntries(_ context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	f.entriesReq = req
	return f.entries, nil
}
```

- [ ] **Step 4: Run RPC tests and confirm they fail**

Run:

```bash
go test ./internalv2/access/node -run 'TestManagerAppLogRPC' -count=1
```

Expected: FAIL because app-log RPC codec, service, and adapter options are undefined.

- [ ] **Step 5: Add adapter option and RPC codec**

Modify `internalv2/access/node/presence_rpc.go`:

```go
// ManagerAppLogs handles node-local manager ordinary application log page requests.
ManagerAppLogs ManagerAppLogReader
```

Add field to `Adapter`:

```go
managerAppLogs ManagerAppLogReader
```

Assign it in `New`:

```go
managerAppLogs: opts.ManagerAppLogs,
```

Create `internalv2/access/node/manager_app_logs_codec.go`:

```go
package node

import (
	"encoding/json"
	"fmt"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerAppLogRequestMagic  = [...]byte{'W', 'K', 'V', 'G', 1}
	managerAppLogResponseMagic = [...]byte{'W', 'K', 'V', 'g', 1}
)

type managerAppLogRPCRequest struct {
	Op string
	NodeID uint64
	Source string
	Limit int
	Cursor string
	Keyword string
	Levels []string
}

type managerAppLogRPCResponse struct {
	Status string
	Sources managementusecase.ApplicationLogSourcesResponse
	Entries managementusecase.ApplicationLogEntriesResponse
}

func encodeManagerAppLogRequest(req managerAppLogRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, managerAppLogRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendString(dst, req.Source)
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendString(dst, req.Cursor)
	dst = appendString(dst, req.Keyword)
	dst = appendStringSlice(dst, req.Levels)
	return dst, nil
}

func decodeManagerAppLogRequest(body []byte) (managerAppLogRPCRequest, error) {
	if !hasMagic(body, managerAppLogRequestMagic[:]) {
		return managerAppLogRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager app log request codec")
	}
	offset := len(managerAppLogRequestMagic)
	var req managerAppLogRPCRequest
	var err error
	if req.Op, offset, err = readString(body, offset); err != nil { return req, err }
	if req.NodeID, offset, err = readUvarint(body, offset); err != nil { return req, err }
	if req.Source, offset, err = readString(body, offset); err != nil { return req, err }
	limit, offset, err := readVarint(body, offset)
	if err != nil { return req, err }
	req.Limit = int(limit)
	if req.Cursor, offset, err = readString(body, offset); err != nil { return req, err }
	if req.Keyword, offset, err = readString(body, offset); err != nil { return req, err }
	if req.Levels, offset, err = readStringSlice(body, offset, "manager app log levels"); err != nil { return req, err }
	if offset != len(body) {
		return managerAppLogRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager app log request bytes")
	}
	return req, nil
}

func encodeManagerAppLogResponse(resp managerAppLogRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerAppLogResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendApplicationLogSources(dst, resp.Sources)
	dst = appendApplicationLogEntries(dst, resp.Entries)
	return dst, nil
}

func decodeManagerAppLogResponse(body []byte) (managerAppLogRPCResponse, error) {
	if !hasMagic(body, managerAppLogResponseMagic[:]) {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager app log response codec")
	}
	offset := len(managerAppLogResponseMagic)
	var resp managerAppLogRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil { return resp, err }
	if resp.Sources, offset, err = readApplicationLogSources(body, offset); err != nil { return resp, err }
	if resp.Entries, offset, err = readApplicationLogEntries(body, offset); err != nil { return resp, err }
	if offset != len(body) {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager app log response bytes")
	}
	return resp, nil
}

func appendApplicationLogSources(dst []byte, resp managementusecase.ApplicationLogSourcesResponse) []byte {
	dst = appendUvarint(dst, resp.NodeID)
	dst = appendUvarint(dst, uint64(len(resp.Sources)))
	for _, source := range resp.Sources {
		dst = appendString(dst, source.Name)
		dst = appendString(dst, source.File)
		dst = appendBoolByte(dst, source.Available)
		dst = appendVarint(dst, source.SizeBytes)
		dst = appendVarint(dst, source.ModifiedAt.UnixNano())
	}
	return dst
}

func readApplicationLogSources(body []byte, offset int) (managementusecase.ApplicationLogSourcesResponse, int, error) {
	var resp managementusecase.ApplicationLogSourcesResponse
	var err error
	if resp.NodeID, offset, err = readUvarint(body, offset); err != nil { return resp, offset, err }
	count, offset, err := readUvarint(body, offset)
	if err != nil { return resp, offset, err }
	resp.Sources = make([]managementusecase.ApplicationLogSource, 0, count)
	for i := uint64(0); i < count; i++ {
		var source managementusecase.ApplicationLogSource
		if source.Name, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if source.File, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if source.Available, offset, err = readBoolByte(body, offset, "manager app log source available"); err != nil { return resp, offset, err }
		if source.SizeBytes, offset, err = readVarint(body, offset); err != nil { return resp, offset, err }
		modified, next, err := readVarint(body, offset)
		if err != nil { return resp, offset, err }
		offset = next
		if modified != 0 {
			source.ModifiedAt = time.Unix(0, modified)
		}
		resp.Sources = append(resp.Sources, source)
	}
	return resp, offset, nil
}
```

Reuse the existing package-level `appendStringSlice` and `readStringSlice`
helpers from `delivery_codec.go`.

Add entry response helpers in `manager_app_logs_codec.go`:

```go
func appendApplicationLogEntries(dst []byte, resp managementusecase.ApplicationLogEntriesResponse) []byte {
	dst = appendUvarint(dst, resp.NodeID)
	dst = appendString(dst, resp.Source)
	dst = appendString(dst, resp.Cursor)
	dst = appendBoolByte(dst, resp.Rotated)
	dst = appendUvarint(dst, uint64(len(resp.Items)))
	for _, item := range resp.Items {
		dst = appendUvarint(dst, item.Seq)
		dst = appendUvarint(dst, item.Offset)
		dst = appendVarint(dst, item.Time.UnixNano())
		dst = appendString(dst, item.Level)
		dst = appendString(dst, item.Module)
		dst = appendString(dst, item.Caller)
		dst = appendString(dst, item.Message)
		dst = appendJSONMap(dst, item.Fields)
		dst = appendString(dst, item.Raw)
		dst = appendBoolByte(dst, item.Truncated)
	}
	return dst
}

func readApplicationLogEntries(body []byte, offset int) (managementusecase.ApplicationLogEntriesResponse, int, error) {
	var resp managementusecase.ApplicationLogEntriesResponse
	var err error
	if resp.NodeID, offset, err = readUvarint(body, offset); err != nil { return resp, offset, err }
	if resp.Source, offset, err = readString(body, offset); err != nil { return resp, offset, err }
	if resp.Cursor, offset, err = readString(body, offset); err != nil { return resp, offset, err }
	if resp.Rotated, offset, err = readBoolByte(body, offset, "manager app log rotated"); err != nil { return resp, offset, err }
	count, offset, err := readUvarint(body, offset)
	if err != nil { return resp, offset, err }
	if count > maxManagerLogRPCCollectionLen {
		return resp, offset, fmt.Errorf("internalv2/access/node: manager app log entries collection too large")
	}
	resp.Items = make([]managementusecase.ApplicationLogEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		var item managementusecase.ApplicationLogEntry
		if item.Seq, offset, err = readUvarint(body, offset); err != nil { return resp, offset, err }
		if item.Offset, offset, err = readUvarint(body, offset); err != nil { return resp, offset, err }
		ts, next, err := readVarint(body, offset)
		if err != nil { return resp, offset, err }
		offset = next
		if ts != 0 { item.Time = time.Unix(0, ts) }
		if item.Level, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if item.Module, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if item.Caller, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if item.Message, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if item.Fields, offset, err = readJSONMap(body, offset); err != nil { return resp, offset, err }
		if item.Raw, offset, err = readString(body, offset); err != nil { return resp, offset, err }
		if item.Truncated, offset, err = readBoolByte(body, offset, "manager app log truncated"); err != nil { return resp, offset, err }
		resp.Items = append(resp.Items, item)
	}
	return resp, offset, nil
}

func appendJSONMap(dst []byte, fields map[string]any) []byte {
	body, _ := json.Marshal(fields)
	return appendBytes(dst, body)
}

func readJSONMap(body []byte, offset int) (map[string]any, int, error) {
	raw, next, err := readBytes(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, next, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, offset, fmt.Errorf("internalv2/access/node: decode manager app log fields: %w", err)
	}
	return fields, next, nil
}
```

- [ ] **Step 6: Add RPC handler and client**

Create `internalv2/access/node/manager_app_logs_rpc.go`:

```go
package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerAppLogRPCServiceID is the clusterv2 RPC service for node-local ordinary application log reads.
const ManagerAppLogRPCServiceID uint8 = clusternet.RPCManagerAppLogs

// ManagerAppLogReader exposes node-local ordinary application log pages for RPC.
type ManagerAppLogReader interface {
	ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
	ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
}

// HandleManagerAppLogRPC handles one encoded manager application log RPC payload.
func (a *Adapter) HandleManagerAppLogRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerAppLogRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager app log rpc decode failed",
			wklog.Event("internalv2.access.node.manager_app_log_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerAppLogs == nil {
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case "sources":
		resp, err := a.managerAppLogs.ApplicationLogSources(ctx, managementusecase.ApplicationLogSourcesRequest{NodeID: req.NodeID})
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: managerAppLogStatusForError(err), Sources: resp})
	case "entries":
		resp, err := a.managerAppLogs.ApplicationLogEntries(ctx, managementusecase.ApplicationLogEntriesRequest{
			NodeID: req.NodeID,
			Source: req.Source,
			Limit: req.Limit,
			Cursor: req.Cursor,
			Keyword: req.Keyword,
			Levels: req.Levels,
		})
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: managerAppLogStatusForError(err), Entries: resp})
	default:
		return nil, fmt.Errorf("internalv2/access/node: unknown manager app log op %q", req.Op)
	}
}

// GetManagerApplicationLogSources reads fixed application log source metadata from one node.
func (c *Client) GetManagerApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	resp, err := c.callManagerAppLog(ctx, req.NodeID, managerAppLogRPCRequest{Op: "sources", NodeID: req.NodeID})
	if err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	if err := managerAppLogErrorForStatus(resp.Status); err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	return resp.Sources, nil
}

// GetManagerApplicationLogEntries reads one bounded application log page from one node.
func (c *Client) GetManagerApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	resp, err := c.callManagerAppLog(ctx, req.NodeID, managerAppLogRPCRequest{
		Op: "entries",
		NodeID: req.NodeID,
		Source: req.Source,
		Limit: req.Limit,
		Cursor: req.Cursor,
		Keyword: req.Keyword,
		Levels: req.Levels,
	})
	if err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	if err := managerAppLogErrorForStatus(resp.Status); err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	return resp.Entries, nil
}

func (c *Client) callManagerAppLog(ctx context.Context, nodeID uint64, req managerAppLogRPCRequest) (managerAppLogRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: manager app log rpc client not configured")
	}
	body, err := encodeManagerAppLogRequest(req)
	if err != nil {
		return managerAppLogRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerAppLogRPCServiceID, body)
	if err != nil {
		return managerAppLogRPCResponse{}, err
	}
	return decodeManagerAppLogResponse(respBody)
}

func managerAppLogStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrNotFound):
		return rpcStatusNotFound
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusRejected
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerAppLogErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotFound:
		return metadb.ErrNotFound
	case rpcStatusRejected:
		return managementusecase.ErrApplicationLogReaderUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager app log rpc status %q", status)
	}
}
```

- [ ] **Step 7: Run node RPC tests**

Run:

```bash
go test ./internalv2/access/node -run 'TestManagerAppLogRPC|TestRPCServiceIDsAreUniqueAndNonZero' -count=1
```

Expected: PASS for app-log RPC tests. The service ID test belongs to `pkg/clusterv2/net`; if this command does not run it, run the separate net package command from Step 2.

- [ ] **Step 8: Update access/node flow documentation**

Modify `internalv2/access/node/FLOW.md` with:

```markdown
## Manager Application Log RPC

```text
remote manager app-log reader
  -> encode W K V G 1 request
  -> clusterv2 RPCManagerAppLogs
  -> Adapter.HandleManagerAppLogRPC
  -> Management application log reader port
  -> encode W K V g 1 response
```

Manager Application Log RPC transports ordinary process log source and entry
page requests for one selected node. It does not read Controller, Slot, Channel,
or Raft logs, and it does not accept filesystem paths.
```

- [ ] **Step 9: Commit**

Run:

```bash
git add pkg/clusterv2/net/ids.go pkg/clusterv2/net/ids_test.go internalv2/access/node/manager_app_logs_codec.go internalv2/access/node/manager_app_logs_rpc.go internalv2/access/node/manager_app_logs_rpc_test.go internalv2/access/node/presence_rpc.go internalv2/access/node/FLOW.md
git commit -m "feat: add manager app log rpc"
```

---

### Task 4: Cluster Infra Reader and App Wiring

**Files:**
- Create: `internalv2/infra/cluster/management_app_logs.go`
- Create: `internalv2/infra/cluster/management_app_logs_test.go`
- Create: `internalv2/app/app_logs.go`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing local/remote routing tests**

Create `internalv2/infra/cluster/management_app_logs_test.go`:

```go
package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementAppLogReaderUsesLocalReader(t *testing.T) {
	local := &fakeLocalAppLogs{nodeID: 1, entries: managementusecase.ApplicationLogEntriesResponse{NodeID: 1, Source: "app"}}
	reader := NewManagementApplicationLogReader(local, local)
	resp, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{NodeID: 1, Source: "app"})
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}
	if resp.NodeID != 1 || !local.entriesCalled {
		t.Fatalf("response = %#v localCalled=%v", resp, local.entriesCalled)
	}
}

func TestManagementAppLogReaderRoutesRemoteReader(t *testing.T) {
	remoteService := &fakeLocalAppLogs{nodeID: 2, entries: managementusecase.ApplicationLogEntriesResponse{NodeID: 2, Source: "app"}}
	node := &fakeAppLogRPCNode{nodeID: 1, remote: remoteService}
	local := &fakeLocalAppLogs{nodeID: 1}
	reader := NewManagementApplicationLogReader(node, local)
	req := managementusecase.ApplicationLogEntriesRequest{NodeID: 2, Source: "app"}
	resp, err := reader.ApplicationLogEntries(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}
	if resp.NodeID != 2 || node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerAppLogRPCServiceID {
		t.Fatalf("resp=%#v calledNode=%d service=%d", resp, node.calledNodeID, node.calledServiceID)
	}
}
```

Add helper fakes in the same file:

```go
type fakeLocalAppLogs struct {
	nodeID uint64
	entries managementusecase.ApplicationLogEntriesResponse
	entriesCalled bool
}

func (f *fakeLocalAppLogs) NodeID() uint64 { return f.nodeID }

func (f *fakeLocalAppLogs) ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	return managementusecase.ApplicationLogSourcesResponse{NodeID: f.nodeID}, nil
}

func (f *fakeLocalAppLogs) ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	f.entriesCalled = true
	return f.entries, nil
}

type fakeAppLogRPCNode struct {
	nodeID uint64
	calledNodeID uint64
	calledServiceID uint8
	remote accessnode.ManagerAppLogReader
}

func (f *fakeAppLogRPCNode) NodeID() uint64 { return f.nodeID }
func (f *fakeAppLogRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	adapter := accessnode.New(accessnode.Options{ManagerAppLogs: f.remote})
	return adapter.HandleManagerAppLogRPC(context.Background(), payload)
}
```

- [ ] **Step 2: Run infra tests and confirm they fail**

Run:

```bash
go test ./internalv2/infra/cluster -run TestManagementAppLogReader -count=1
```

Expected: FAIL because `NewManagementApplicationLogReader` is undefined.

- [ ] **Step 3: Add cluster infra reader**

Create `internalv2/infra/cluster/management_app_logs.go`:

```go
package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementApplicationLogRPCNode exposes node RPC for remote application log reads.
type ManagementApplicationLogRPCNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// LocalApplicationLogReader exposes node-local ordinary application log pages.
type LocalApplicationLogReader interface {
	ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
	ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
}

// ManagementApplicationLogReader routes manager application log reads to selected nodes.
type ManagementApplicationLogReader struct {
	node ManagementApplicationLogRPCNode
	local LocalApplicationLogReader
	remote *accessnode.Client
}

// NewManagementApplicationLogReader creates a selected-node application log reader.
func NewManagementApplicationLogReader(node ManagementApplicationLogRPCNode, local LocalApplicationLogReader) *ManagementApplicationLogReader {
	return &ManagementApplicationLogReader{node: node, local: local, remote: accessnode.NewClient(node)}
}

// ApplicationLogSources returns fixed application log sources for one selected node.
func (r *ManagementApplicationLogReader) ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	if r == nil || r.local == nil {
		return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	if r.node == nil || req.NodeID == r.node.NodeID() {
		return r.local.ApplicationLogSources(ctx, req)
	}
	return r.remote.GetManagerApplicationLogSources(ctx, req)
}

// ApplicationLogEntries returns one bounded application log page for one selected node.
func (r *ManagementApplicationLogReader) ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	if r == nil || r.local == nil {
		return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	if r.node == nil || req.NodeID == r.node.NodeID() {
		return r.local.ApplicationLogEntries(ctx, req)
	}
	return r.remote.GetManagerApplicationLogEntries(ctx, req)
}
```

- [ ] **Step 4: Add the app-layer DTO adapter**

Create `internalv2/app/app_logs.go`:

```go
package app

import (
	"context"

	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

type applicationLogReader struct {
	reader *applog.AppLogReader
}

func newApplicationLogReader(dir string) *applicationLogReader {
	return &applicationLogReader{reader: applog.NewAppLogReader(applog.AppLogReaderOptions{Dir: dir})}
}

func (r *applicationLogReader) ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	if r == nil || r.reader == nil {
		return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	resp, err := r.reader.Sources(ctx, applog.AppLogSourcesRequest{NodeID: req.NodeID})
	if err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	out := managementusecase.ApplicationLogSourcesResponse{NodeID: resp.NodeID, Sources: make([]managementusecase.ApplicationLogSource, 0, len(resp.Sources))}
	for _, source := range resp.Sources {
		out.Sources = append(out.Sources, managementusecase.ApplicationLogSource{
			Name: source.Name,
			File: source.File,
			Available: source.Available,
			SizeBytes: source.SizeBytes,
			ModifiedAt: source.ModifiedAt,
		})
	}
	return out, nil
}

func (r *applicationLogReader) ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	if r == nil || r.reader == nil {
		return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	resp, err := r.reader.Entries(ctx, applog.AppLogEntriesRequest{
		NodeID: req.NodeID,
		Source: req.Source,
		Limit: req.Limit,
		Cursor: req.Cursor,
		Keyword: req.Keyword,
		Levels: req.Levels,
	})
	if err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	out := managementusecase.ApplicationLogEntriesResponse{
		NodeID: resp.NodeID,
		Source: resp.Source,
		Cursor: resp.Cursor,
		Rotated: resp.Rotated,
		Items: make([]managementusecase.ApplicationLogEntry, 0, len(resp.Items)),
	}
	for _, item := range resp.Items {
		out.Items = append(out.Items, managementusecase.ApplicationLogEntry{
			Seq: item.Seq,
			Offset: item.Offset,
			Time: item.Time,
			Level: item.Level,
			Module: item.Module,
			Caller: item.Caller,
			Message: item.Message,
			Fields: item.Fields,
			Raw: item.Raw,
			Truncated: item.Truncated,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Wire app-local reader and RPC**

Add helper in `internalv2/app/wiring.go`:

```go
func (a *App) newManagementApplicationLogReader() managementusecase.ApplicationLogReader {
	return newApplicationLogReader(a.cfg.Log.Dir)
}
```

Add `wireManagerAppLogRPC`:

```go
func (a *App) wireManagerAppLogRPC() {
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	node, hasNode := a.cluster.(clusterinfra.ManagementApplicationLogRPCNode)
	if !hasRegistrar || !hasNode {
		return
	}
	local := a.newManagementApplicationLogReader()
	reader := clusterinfra.NewManagementApplicationLogReader(node, local)
	adapter := accessnode.New(accessnode.Options{ManagerAppLogs: reader, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerAppLogRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerAppLogRPC))
}
```

Call it after `wireManagerLogRPC()` in `New`:

```go
app.wireManagerAppLogRPC()
```

In `wireManager()`, add:

```go
if appLogNode, ok := a.cluster.(clusterinfra.ManagementApplicationLogRPCNode); ok {
	opts.ApplicationLogs = clusterinfra.NewManagementApplicationLogReader(appLogNode, a.newManagementApplicationLogReader())
} else {
	opts.ApplicationLogs = a.newManagementApplicationLogReader()
}
```

- [ ] **Step 6: Run app and infra targeted tests**

Run:

```bash
go test ./internalv2/infra/cluster ./internalv2/app -run 'TestManagementAppLog|TestApp' -count=1
```

Expected: PASS. If existing app tests are broad and slow, run `go test ./internalv2/app -run TestNew -count=1` plus the new app wiring test added in this task.

- [ ] **Step 7: Update infra/app flow docs**

Add to `internalv2/infra/cluster/FLOW.md`:

```markdown
## Management Application Log Flow

`ManagementApplicationLogReader` routes ordinary process log reads for the
selected `node_id`. Local reads call the node-local fixed-source reader; remote
reads call access/node Manager Application Log RPC. It does not read Raft logs
or accept filesystem paths.
```

Add one line to `internalv2/app/FLOW.md` describing the manager app-log reader wiring if that file lists manager wiring responsibilities.

- [ ] **Step 8: Commit**

Run:

```bash
git add internalv2/infra/cluster/management_app_logs.go internalv2/infra/cluster/management_app_logs_test.go internalv2/infra/cluster/FLOW.md internalv2/app/app_logs.go internalv2/app/wiring.go internalv2/app/FLOW.md
git commit -m "feat: route manager app log reads"
```

---

### Task 5: Manager HTTP App Log Routes

**Files:**
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/app_logs.go`
- Create: `internalv2/access/manager/app_logs_test.go`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Write failing manager route tests**

Create `internalv2/access/manager/app_logs_test.go`:

```go
package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerApplicationLogEntriesRoute(t *testing.T) {
	service := &fakeManagerAppLogs{entries: managementusecase.ApplicationLogEntriesResponse{
		NodeID: 1,
		Source: "app",
		Cursor: "v1:cursor",
		Items: []managementusecase.ApplicationLogEntry{{Seq: 1, Raw: "hello", Level: "INFO"}},
	}}
	s := New(Options{Management: service})

	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs?node_id=1&source=app&limit=10&keyword=hello&levels=INFO", nil)
	w := httptest.NewRecorder()
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp applicationLogEntriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.NodeID != 1 || resp.Source != "app" || len(resp.Items) != 1 || resp.Items[0].Raw != "hello" {
		t.Fatalf("response = %#v", resp)
	}
	if service.entriesReq.Keyword != "hello" || len(service.entriesReq.Levels) != 1 || service.entriesReq.Levels[0] != "INFO" {
		t.Fatalf("entries request = %#v", service.entriesReq)
	}
}

func TestManagerApplicationLogStreamRouteWritesNDJSON(t *testing.T) {
	service := &fakeManagerAppLogs{entries: managementusecase.ApplicationLogEntriesResponse{
		NodeID: 1,
		Source: "app",
		Cursor: "v1:cursor",
		Items: []managementusecase.ApplicationLogEntry{{Seq: 1, Raw: "hello", Level: "INFO"}},
	}}
	s := New(Options{Management: service})

	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs/stream?node_id=1&source=app&limit=10", nil)
	w := httptest.NewRecorder()
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	if !scanner.Scan() {
		t.Fatal("missing stream event")
	}
	var event map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event["type"] != "line" {
		t.Fatalf("event = %#v, want line", event)
	}
}

type fakeManagerAppLogs struct {
	Management
	sourcesReq managementusecase.ApplicationLogSourcesRequest
	entriesReq managementusecase.ApplicationLogEntriesRequest
	sources managementusecase.ApplicationLogSourcesResponse
	entries managementusecase.ApplicationLogEntriesResponse
}

func (f *fakeManagerAppLogs) ApplicationLogSources(_ context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	f.sourcesReq = req
	return f.sources, nil
}

func (f *fakeManagerAppLogs) ApplicationLogEntries(_ context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	f.entriesReq = req
	if f.entries.Cursor == "" {
		f.entries.Cursor = "v1:cursor"
	}
	if len(f.entries.Items) == 0 {
		f.entries.Items = []managementusecase.ApplicationLogEntry{{Seq: uint64(time.Now().UnixNano()), Raw: "heartbeat source line"}}
	}
	return f.entries, nil
}
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerApplicationLog' -count=1
```

Expected: FAIL because routes and DTOs are undefined.

- [ ] **Step 3: Extend the manager Management interface and register routes**

Modify `internalv2/access/manager/server.go` `Management` interface:

```go
// ApplicationLogSources returns fixed application log source metadata for one node.
ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
// ApplicationLogEntries returns one bounded ordinary application log page.
ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
```

Add route group in `registerRoutes()`:

```go
appLogs := s.engine.Group("/manager")
if s.auth.enabled() {
	appLogs.Use(s.requirePermission("cluster.log", "r"))
}
appLogs.GET("/app-logs/sources", s.handleApplicationLogSources)
appLogs.GET("/app-logs", s.handleApplicationLogEntries)
appLogs.GET("/app-logs/stream", s.handleApplicationLogStream)
```

- [ ] **Step 4: Add manager HTTP DTOs and handlers**

Create `internalv2/access/manager/app_logs.go`:

```go
package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

type applicationLogSourcesResponse struct {
	NodeID uint64 `json:"node_id"`
	Sources []applicationLogSourceDTO `json:"sources"`
}

type applicationLogSourceDTO struct {
	Name string `json:"name"`
	File string `json:"file"`
	Available bool `json:"available"`
	SizeBytes int64 `json:"size_bytes"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

type applicationLogEntriesResponse struct {
	NodeID uint64 `json:"node_id"`
	Source string `json:"source"`
	Cursor string `json:"cursor"`
	Rotated bool `json:"rotated"`
	Items []applicationLogEntryDTO `json:"items"`
}

type applicationLogEntryDTO struct {
	Seq uint64 `json:"seq"`
	Offset uint64 `json:"offset"`
	Time string `json:"time,omitempty"`
	Level string `json:"level,omitempty"`
	Module string `json:"module,omitempty"`
	Caller string `json:"caller,omitempty"`
	Message string `json:"message,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	Raw string `json:"raw"`
	Truncated bool `json:"truncated,omitempty"`
}

type applicationLogStreamEvent struct {
	Type string `json:"type"`
	Cursor string `json:"cursor,omitempty"`
	Rotated bool `json:"rotated,omitempty"`
	Item *applicationLogEntryDTO `json:"item,omitempty"`
	Error string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

func (s *Server) handleApplicationLogSources(c *gin.Context) {
	req, err := parseApplicationLogSourcesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp, err := s.management.ApplicationLogSources(c.Request.Context(), req)
	if err != nil {
		writeApplicationLogError(c, err)
		return
	}
	c.JSON(http.StatusOK, applicationLogSourcesDTO(resp))
}

func (s *Server) handleApplicationLogEntries(c *gin.Context) {
	req, err := parseApplicationLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp, err := s.management.ApplicationLogEntries(c.Request.Context(), req)
	if err != nil {
		writeApplicationLogError(c, err)
		return
	}
	c.JSON(http.StatusOK, applicationLogEntriesDTO(resp))
}

func (s *Server) handleApplicationLogStream(c *gin.Context) {
	req, err := parseApplicationLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	flusher, _ := c.Writer.(http.Flusher)
	cursor := req.Cursor
	for {
		req.Cursor = cursor
		resp, err := s.management.ApplicationLogEntries(c.Request.Context(), req)
		if err != nil {
			writeNDJSON(c, applicationLogStreamEvent{Type: "error", Error: "read_failed", Message: err.Error()})
			if flusher != nil { flusher.Flush() }
			return
		}
		cursor = resp.Cursor
		if resp.Rotated {
			writeNDJSON(c, applicationLogStreamEvent{Type: "rotation", Cursor: cursor, Rotated: true})
		}
		for _, item := range applicationLogEntryDTOs(resp.Items) {
			line := item
			writeNDJSON(c, applicationLogStreamEvent{Type: "line", Cursor: cursor, Item: &line})
		}
		if flusher != nil { flusher.Flush() }
		if len(resp.Items) > 0 {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-time.After(time.Second):
			writeNDJSON(c, applicationLogStreamEvent{Type: "heartbeat", Cursor: cursor})
			if flusher != nil { flusher.Flush() }
			return
		}
	}
}
```

Add parse and mapping helpers in the same file:

```go
func parseApplicationLogSourcesRequest(c *gin.Context) (managementusecase.ApplicationLogSourcesRequest, error) {
	nodeID, err := parseRequiredLogNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ApplicationLogSourcesRequest{}, errors.New("invalid node_id")
	}
	return managementusecase.ApplicationLogSourcesRequest{NodeID: nodeID}, nil
}

func parseApplicationLogEntriesRequest(c *gin.Context) (managementusecase.ApplicationLogEntriesRequest, error) {
	nodeID, err := parseRequiredLogNodeID(c.Query("node_id"))
	if err != nil { return managementusecase.ApplicationLogEntriesRequest{}, errors.New("invalid node_id") }
	limit, err := parseLogLimit(c.Query("limit"))
	if err != nil { return managementusecase.ApplicationLogEntriesRequest{}, errors.New("invalid limit") }
	return managementusecase.ApplicationLogEntriesRequest{
		NodeID: nodeID,
		Source: c.DefaultQuery("source", applog.AppLogSourceApp),
		Limit: limit,
		Cursor: c.Query("cursor"),
		Keyword: c.Query("keyword"),
		Levels: parseCSV(c.Query("levels")),
	}, nil
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" { return nil }
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func applicationLogSourcesDTO(resp managementusecase.ApplicationLogSourcesResponse) applicationLogSourcesResponse {
	out := applicationLogSourcesResponse{NodeID: resp.NodeID, Sources: make([]applicationLogSourceDTO, 0, len(resp.Sources))}
	for _, source := range resp.Sources {
		dto := applicationLogSourceDTO{Name: source.Name, File: source.File, Available: source.Available, SizeBytes: source.SizeBytes}
		if !source.ModifiedAt.IsZero() { dto.ModifiedAt = source.ModifiedAt.Format(time.RFC3339Nano) }
		out.Sources = append(out.Sources, dto)
	}
	return out
}

func applicationLogEntriesDTO(resp managementusecase.ApplicationLogEntriesResponse) applicationLogEntriesResponse {
	return applicationLogEntriesResponse{
		NodeID: resp.NodeID,
		Source: resp.Source,
		Cursor: resp.Cursor,
		Rotated: resp.Rotated,
		Items: applicationLogEntryDTOs(resp.Items),
	}
}

func applicationLogEntryDTOs(items []managementusecase.ApplicationLogEntry) []applicationLogEntryDTO {
	out := make([]applicationLogEntryDTO, 0, len(items))
	for _, item := range items {
		dto := applicationLogEntryDTO{
			Seq: item.Seq,
			Offset: item.Offset,
			Level: item.Level,
			Module: item.Module,
			Caller: item.Caller,
			Message: item.Message,
			Fields: item.Fields,
			Raw: item.Raw,
			Truncated: item.Truncated,
		}
		if !item.Time.IsZero() { dto.Time = item.Time.Format(time.RFC3339Nano) }
		out = append(out, dto)
	}
	return out
}

func writeNDJSON(c *gin.Context, event applicationLogStreamEvent) {
	body, _ := json.Marshal(event)
	_, _ = c.Writer.Write(append(body, '\n'))
}

func writeApplicationLogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument), errors.Is(err, applog.ErrAppLogInvalidSource), errors.Is(err, applog.ErrAppLogInvalidCursor):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid application log request")
	case errors.Is(err, metadb.ErrNotFound), errors.Is(err, applog.ErrAppLogNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "application log not found")
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "application log reader unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
```

- [ ] **Step 5: Run manager route tests**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerApplicationLog' -count=1
```

Expected: PASS.

- [ ] **Step 6: Update manager flow documentation**

Modify `internalv2/access/manager/FLOW.md` route list with:

```text
GET  /manager/app-logs/sources (ordinary application log sources; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs         (ordinary application log page; requires cluster.log:r when Auth.On=true)
GET  /manager/app-logs/stream  (ordinary application log NDJSON stream; requires cluster.log:r when Auth.On=true)
```

Add a short paragraph:

```markdown
`/manager/app-logs*` exposes ordinary process logs written under `WK_LOG_DIR`.
It only accepts fixed sources (`app`, `warn`, `error`, `debug`) and does not
inspect Controller or Slot Raft logs.
```

- [ ] **Step 7: Commit**

Run:

```bash
git add internalv2/access/manager/server.go internalv2/access/manager/app_logs.go internalv2/access/manager/app_logs_test.go internalv2/access/manager/FLOW.md
git commit -m "feat: expose manager app log routes"
```

---

### Task 6: Web Manager API Helpers

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing web API tests**

Append to `web/src/lib/manager-api.test.ts`:

```ts
it("fetches application log sources and entries", async () => {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 2, sources: [{ name: "app", file: "app.log", available: true, size_bytes: 12 }] }), { status: 200 }))
  await expect(getApplicationLogSources({ nodeId: 2 })).resolves.toEqual({
    node_id: 2,
    sources: [{ name: "app", file: "app.log", available: true, size_bytes: 12 }],
  })
  expect(fetchMock).toHaveBeenLastCalledWith("/manager/app-logs/sources?node_id=2", expect.any(Object))

  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 2, source: "app", cursor: "v1:x", rotated: false, items: [] }), { status: 200 }))
  await expect(getApplicationLogs({ nodeId: 2, source: "app", limit: 50, keyword: "send", levels: ["INFO", "WARN"] })).resolves.toEqual({
    node_id: 2,
    source: "app",
    cursor: "v1:x",
    rotated: false,
    items: [],
  })
  expect(fetchMock).toHaveBeenLastCalledWith("/manager/app-logs?node_id=2&source=app&limit=50&keyword=send&levels=INFO%2CWARN", expect.any(Object))
})

it("opens application log stream with JSON accept header", async () => {
  fetchMock.mockResolvedValueOnce(new Response("", { status: 200 }))
  await openApplicationLogStream({ nodeId: 2, source: "error", cursor: "v1:x" })
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/manager/app-logs/stream?node_id=2&source=error&cursor=v1%3Ax",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
cd web
bun run test -- src/lib/manager-api.test.ts
```

Expected: FAIL because new helpers and types are undefined.

- [ ] **Step 3: Add TypeScript DTOs**

Modify `web/src/lib/manager-api.types.ts`:

```ts
export type ManagerApplicationLogSourceName = "app" | "warn" | "error" | "debug"

export type ManagerApplicationLogSource = {
  name: ManagerApplicationLogSourceName
  file: string
  available: boolean
  size_bytes: number
  modified_at?: string
}

export type ManagerApplicationLogSourcesResponse = {
  node_id: number
  sources: ManagerApplicationLogSource[]
}

export type ManagerApplicationLogEntry = {
  seq: number
  offset: number
  time?: string
  level?: string
  module?: string
  caller?: string
  message?: string
  fields?: Record<string, unknown>
  raw: string
  truncated?: boolean
}

export type ManagerApplicationLogsResponse = {
  node_id: number
  source: ManagerApplicationLogSourceName
  cursor: string
  rotated: boolean
  items: ManagerApplicationLogEntry[]
}

export type ApplicationLogSourcesParams = {
  nodeId: number
}

export type ApplicationLogListParams = {
  nodeId: number
  source?: ManagerApplicationLogSourceName
  limit?: number
  cursor?: string
  keyword?: string
  levels?: string[]
}

export type ManagerApplicationLogStreamEvent =
  | { type: "line"; cursor?: string; item: ManagerApplicationLogEntry }
  | { type: "heartbeat"; cursor?: string }
  | { type: "rotation"; cursor?: string; rotated: true }
  | { type: "error"; error: string; message: string }
```

- [ ] **Step 4: Add API helpers**

Modify imports in `web/src/lib/manager-api.ts` to include the new types.

Add helpers near other path builders:

```ts
function buildApplicationLogSourcesPath(params: ApplicationLogSourcesParams) {
  const search = new URLSearchParams()
  search.set("node_id", String(params.nodeId))
  return `/manager/app-logs/sources?${search.toString()}`
}

function buildApplicationLogsPath(params: ApplicationLogListParams, stream = false) {
  const search = new URLSearchParams()
  search.set("node_id", String(params.nodeId))
  if (params.source) search.set("source", params.source)
  if (typeof params.limit === "number") search.set("limit", String(params.limit))
  if (params.cursor) search.set("cursor", params.cursor)
  if (params.keyword) search.set("keyword", params.keyword)
  if (params.levels?.length) search.set("levels", params.levels.join(","))
  return `/manager/app-logs${stream ? "/stream" : ""}?${search.toString()}`
}
```

Add exported functions:

```ts
export function getApplicationLogSources(params: ApplicationLogSourcesParams) {
  return jsonManagerFetch<ManagerApplicationLogSourcesResponse>(buildApplicationLogSourcesPath(params))
}

export function getApplicationLogs(params: ApplicationLogListParams) {
  return jsonManagerFetch<ManagerApplicationLogsResponse>(buildApplicationLogsPath(params))
}

export function openApplicationLogStream(params: ApplicationLogListParams) {
  return managerFetch(buildApplicationLogsPath(params, true), {
    headers: { Accept: "application/x-ndjson" },
  })
}
```

- [ ] **Step 5: Run web API tests**

Run:

```bash
cd web
bun run test -- src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web app log api client"
```

---

### Task 7: Web App Logs Diagnostics Tab

**Files:**
- Create: `web/src/pages/app-logs/page.tsx`
- Create: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/pages/cluster/diagnostics/page.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/README.md`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/app-logs/page.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ClusterDiagnosticsPage } from "@/pages/cluster/diagnostics/page"

const getNodesMock = vi.fn()
const getSourcesMock = vi.fn()
const getLogsMock = vi.fn()
const openStreamMock = vi.fn()

vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getApplicationLogSources: (...args: unknown[]) => getSourcesMock(...args),
    getApplicationLogs: (...args: unknown[]) => getLogsMock(...args),
    openApplicationLogStream: (...args: unknown[]) => openStreamMock(...args),
  }
})

function renderPage(path = "/cluster/diagnostics?tab=app-logs") {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/cluster/diagnostics" element={<ClusterDiagnosticsPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getSourcesMock.mockReset()
  getLogsMock.mockReset()
  openStreamMock.mockReset()
  getNodesMock.mockResolvedValue({ generated_at: "now", controller_leader_id: 1, items: [{ node_id: 1, name: "node-1", status: "online", is_local: true }] })
  getSourcesMock.mockResolvedValue({ node_id: 1, sources: [{ name: "app", file: "app.log", available: true, size_bytes: 10 }] })
  getLogsMock.mockResolvedValue({ node_id: 1, source: "app", cursor: "v1:cursor", rotated: false, items: [{ seq: 1, offset: 1, level: "INFO", module: "access.gateway", raw: "hello", message: "hello" }] })
  openStreamMock.mockResolvedValue(new Response('{"type":"line","cursor":"v1:next","item":{"seq":2,"offset":2,"level":"WARN","raw":"streamed"}}\n', { status: 200 }))
})

test("renders application logs tab and initial rows", async () => {
  renderPage("/cluster/diagnostics?tab=app-logs")
  expect(await screen.findByText("Application Logs")).toBeInTheDocument()
  expect(await screen.findByText("hello")).toBeInTheDocument()
  await waitFor(() => expect(openStreamMock).toHaveBeenCalled())
})

test("pauses visible stream append", async () => {
  const user = userEvent.setup()
  renderPage("/cluster/diagnostics?tab=app-logs")
  await user.click(await screen.findByRole("button", { name: /pause/i }))
  expect(screen.getByRole("button", { name: /resume/i })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run page tests and confirm they fail**

Run:

```bash
cd web
bun run test -- src/pages/app-logs/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx
```

Expected: FAIL because the page and tab do not exist.

- [ ] **Step 3: Implement App Logs page**

Create `web/src/pages/app-logs/page.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"
import { Pause, Play, RotateCw, Trash2 } from "lucide-react"

import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { getApplicationLogSources, getApplicationLogs, getNodes, openApplicationLogStream } from "@/lib/manager-api"
import type { ManagerApplicationLogEntry, ManagerApplicationLogSourceName, ManagerNodesResponse } from "@/lib/manager-api.types"

const logLimit = 200
const maxVisibleRows = 2000
const sources: ManagerApplicationLogSourceName[] = ["app", "warn", "error", "debug"]

type LogRow =
  | { kind: "line"; item: ManagerApplicationLogEntry }
  | { kind: "rotation"; cursor?: string }

export function AppLogsPanel() {
  const intl = useIntl()
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [source, setSource] = useState<ManagerApplicationLogSourceName>("app")
  const [rows, setRows] = useState<LogRow[]>([])
  const [cursor, setCursor] = useState("")
  const [paused, setPaused] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    let cancelled = false
    getNodes().then((nextNodes) => {
      if (cancelled) return
      setNodes(nextNodes)
      setSelectedNodeId((current) => current && hasNode(nextNodes, current) ? current : defaultNodeId(nextNodes))
    }).catch((err) => {
      if (!cancelled) setError(err instanceof Error ? err : new Error("node list failed"))
    })
    return () => { cancelled = true }
  }, [])

  const appendRows = useCallback((next: LogRow[]) => {
    setRows((current) => [...current, ...next].slice(-maxVisibleRows))
  }, [])

  const loadInitial = useCallback(async (nodeId: number, nextSource: ManagerApplicationLogSourceName) => {
    setLoading(true)
    setError(null)
    abortRef.current?.abort()
    try {
      await getApplicationLogSources({ nodeId })
      const page = await getApplicationLogs({ nodeId, source: nextSource, limit: logLimit })
      setCursor(page.cursor)
      setRows(page.items.map((item) => ({ kind: "line", item })))
    } catch (err) {
      setError(err instanceof Error ? err : new Error("application log request failed"))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!selectedNodeId) return
    void loadInitial(selectedNodeId, source)
  }, [loadInitial, selectedNodeId, source])

  useEffect(() => {
    if (!selectedNodeId || paused || !cursor) return
    const controller = new AbortController()
    abortRef.current = controller
    async function stream() {
      try {
        const response = await openApplicationLogStream({ nodeId: selectedNodeId, source, cursor })
        const reader = response.body?.getReader()
        if (!reader) return
        const decoder = new TextDecoder()
        let buffer = ""
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split("\n")
          buffer = lines.pop() ?? ""
          for (const line of lines) {
            if (!line.trim()) continue
            const event = JSON.parse(line) as { type: string; cursor?: string; item?: ManagerApplicationLogEntry }
            if (event.cursor) setCursor(event.cursor)
            if (event.type === "line" && event.item) appendRows([{ kind: "line", item: event.item }])
            if (event.type === "rotation") appendRows([{ kind: "rotation", cursor: event.cursor }])
          }
        }
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err : new Error("application log stream failed"))
      }
    }
    void stream()
    return () => controller.abort()
  }, [appendRows, cursor, paused, selectedNodeId, source])

  const sourceButtons = useMemo(() => sources.map((name) => (
    <Button key={name} size="sm" variant={source === name ? "default" : "outline"} onClick={() => setSource(name)}>
      {name}
    </Button>
  )), [source])

  if (error) {
    return <ResourceState kind="error" title={intl.formatMessage({ id: "appLogs.title" })} description={error.message} />
  }

  return (
    <SectionCard
      description={intl.formatMessage({ id: "appLogs.description" })}
      title={intl.formatMessage({ id: "appLogs.title" })}
    >
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <NodeFilter nodes={nodes} selectedNodeId={selectedNodeId} onChange={setSelectedNodeId} />
        <div className="flex items-center gap-2">{sourceButtons}</div>
        <Button size="sm" variant="outline" onClick={() => setPaused((value) => !value)}>
          {paused ? <Play className="mr-2 size-4" /> : <Pause className="mr-2 size-4" />}
          {paused ? intl.formatMessage({ id: "appLogs.resume" }) : intl.formatMessage({ id: "appLogs.pause" })}
        </Button>
        <Button size="sm" variant="outline" onClick={() => selectedNodeId && loadInitial(selectedNodeId, source)}>
          <RotateCw className="mr-2 size-4" />
          {intl.formatMessage({ id: "common.refresh" })}
        </Button>
        <Button size="sm" variant="outline" onClick={() => setRows([])}>
          <Trash2 className="mr-2 size-4" />
          {intl.formatMessage({ id: "appLogs.clear" })}
        </Button>
      </div>

      {loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "appLogs.title" })} /> : null}
      {!loading ? (
        <div className="h-[560px] overflow-auto rounded-md border border-border bg-muted/20 font-mono text-xs">
          {rows.map((row, index) => row.kind === "rotation" ? (
            <div className="border-b border-border px-3 py-2 text-muted-foreground" key={`rotation-${index}`}>
              {intl.formatMessage({ id: "appLogs.rotation" })}
            </div>
          ) : (
            <div className="grid grid-cols-[150px_72px_180px_1fr] gap-3 border-b border-border/70 px-3 py-2" key={`${row.item.offset}-${index}`}>
              <span className="text-muted-foreground">{row.item.time ?? "-"}</span>
              <span className={levelClassName(row.item.level)}>{row.item.level ?? "-"}</span>
              <span className="truncate text-muted-foreground">{row.item.module ?? "-"}</span>
              <span className="whitespace-pre-wrap break-words text-foreground">{row.item.message || row.item.raw}</span>
            </div>
          ))}
        </div>
      ) : null}
    </SectionCard>
  )
}

function levelClassName(level?: string) {
  switch (level?.toUpperCase()) {
    case "ERROR":
      return "font-semibold text-destructive"
    case "WARN":
      return "font-semibold text-amber-600"
    case "INFO":
      return "font-semibold text-emerald-600"
    default:
      return "font-semibold text-muted-foreground"
  }
}
```

- [ ] **Step 4: Add diagnostics tab and route redirect**

Modify `web/src/pages/cluster/diagnostics/page.tsx`:

```tsx
import { AppLogsPanel } from "@/pages/app-logs/page"
```

Add tab:

```tsx
{ id: "app-logs", labelMessageId: "diagnostics.tabs.appLogs" },
```

Render:

```tsx
{activeTab === "app-logs" ? <AppLogsPanel /> : null}
```

Modify `web/src/app/router.tsx` legacy redirects:

```tsx
{ path: "app-logs", element: <Navigate replace to="/cluster/diagnostics?tab=app-logs" /> },
```

Modify `web/src/lib/navigation.ts` diagnostics aliases:

```ts
aliases: ["/diagnostics", "/network", "/controller", "/slot-logs", "/app-logs"],
```

Add legacy redirect:

```ts
"/app-logs": "/cluster/diagnostics?tab=app-logs",
```

- [ ] **Step 5: Add i18n keys**

Modify `web/src/i18n/messages/en.ts`:

```ts
"diagnostics.tabs.appLogs": "Application Logs",
"appLogs.title": "Application Logs",
"appLogs.description": "Tail ordinary wukongimv2 process logs from the selected node.",
"appLogs.pause": "Pause",
"appLogs.resume": "Resume",
"appLogs.clear": "Clear",
"appLogs.rotation": "Log rotated; continuing from the active file.",
```

Modify `web/src/i18n/messages/zh-CN.ts`:

```ts
"diagnostics.tabs.appLogs": "应用日志",
"appLogs.title": "应用日志",
"appLogs.description": "查看所选节点的 wukongimv2 普通进程日志。",
"appLogs.pause": "暂停",
"appLogs.resume": "继续",
"appLogs.clear": "清屏",
"appLogs.rotation": "日志已轮转，继续读取当前活跃文件。",
```

- [ ] **Step 6: Update README matrix**

Modify `web/README.md` Page And API Matrix row for `/cluster/diagnostics` to include:

```markdown
| `/cluster/diagnostics?tab=app-logs` | `GET /manager/app-logs/sources`, `GET /manager/app-logs`, `GET /manager/app-logs/stream` | Implemented |
```

- [ ] **Step 7: Run web tests**

Run:

```bash
cd web
bun run test -- src/pages/app-logs/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx src/app/router.test.tsx src/lib/navigation.test.ts
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
git add web/src/pages/app-logs/page.tsx web/src/pages/app-logs/page.test.tsx web/src/pages/cluster/diagnostics/page.tsx web/src/app/router.tsx web/src/lib/navigation.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/README.md
git commit -m "feat: add application logs diagnostics tab"
```

---

### Task 8: Final Verification

**Files:**
- Review: all files changed by Tasks 1-7

- [ ] **Step 1: Run targeted Go tests**

Run:

```bash
go test ./internalv2/log ./internalv2/usecase/management ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/access/manager ./internalv2/app ./pkg/clusterv2/net -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted web tests**

Run:

```bash
cd web
bun run test -- src/lib/manager-api.test.ts src/pages/app-logs/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx src/app/router.test.tsx src/lib/navigation.test.ts
```

Expected: PASS.

- [ ] **Step 3: Run broader package checks**

Run from repo root:

```bash
go test ./internalv2/... ./pkg/clusterv2/net
```

Expected: PASS.

- [ ] **Step 4: Run web build**

Run:

```bash
cd web
bun run build
```

Expected: PASS.

- [ ] **Step 5: Manual smoke test**

Run one node:

```bash
go run ./cmd/wukongimv2 -config ./cmd/wukongimv2/wukongimv2.conf.example
```

Run web dev server in another terminal:

```bash
cd web
bun run dev
```

Open `/cluster/diagnostics?tab=app-logs`, log in, select node 1, and verify:

- `app` source shows recent lines from the node's `app.log`.
- `warn` and `error` source buttons do not accept arbitrary paths.
- Pause stops appending visible rows.
- Resume continues from the latest cursor.
