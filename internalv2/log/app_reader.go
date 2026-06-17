package log

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// AppLogSourceApp identifies the ordinary application log.
	AppLogSourceApp = "app"
	// AppLogSourceWarn identifies the warning-only application log.
	AppLogSourceWarn = "warn"
	// AppLogSourceError identifies the error application log.
	AppLogSourceError = "error"
	// AppLogSourceDebug identifies the debug application log.
	AppLogSourceDebug = "debug"
)

const (
	defaultAppLogMaxTailScanBytes int64 = 8 * 1024 * 1024
	defaultAppLogMaxLineBytes     int64 = 64 * 1024
	defaultAppLogLimit                  = 200
	maxAppLogLimit                      = 1000
	appLogCursorDigestBytes       int64 = 4096
)

var (
	// ErrAppLogInvalidSource reports a source outside the fixed application log source list.
	ErrAppLogInvalidSource = errors.New("invalid app log source")
	// ErrAppLogNotFound reports that the selected fixed log file does not exist.
	ErrAppLogNotFound = errors.New("app log not found")
	// ErrAppLogInvalidCursor reports a malformed or source-mismatched cursor.
	ErrAppLogInvalidCursor = errors.New("invalid app log cursor")
)

var appLogSources = []appLogSource{
	{Source: AppLogSourceApp, Filename: "app.log"},
	{Source: AppLogSourceWarn, Filename: "warn.log"},
	{Source: AppLogSourceError, Filename: "error.log"},
	{Source: AppLogSourceDebug, Filename: "debug.log"},
}

type appLogSource struct {
	Source   string
	Filename string
}

// AppLogReaderOptions controls bounded node-local application log reads.
type AppLogReaderOptions struct {
	// Dir is the application log directory; empty uses the logger default directory.
	Dir string
	// MaxTailScanBytes bounds bytes scanned when serving an initial tail request.
	MaxTailScanBytes int64
	// MaxLineBytes bounds the raw bytes returned for each log entry line.
	MaxLineBytes int64
}

// AppLogReader reads fixed node-local WuKongIM application log files.
type AppLogReader struct {
	// dir is the local application log directory.
	dir string
	// maxTailScanBytes bounds initial tail scans.
	maxTailScanBytes int64
	// maxLineBytes bounds returned raw line bytes.
	maxLineBytes int64
}

// AppLogSourcesRequest requests metadata for the fixed application log sources.
type AppLogSourcesRequest struct {
	// NodeID identifies the node whose local sources are being described.
	NodeID uint64
}

// AppLogSource describes one fixed application log source.
type AppLogSource struct {
	// Name is the stable source identifier used in read requests.
	Name string
	// File is the fixed application log filename for this source.
	File string
	// Available reports whether the source file currently exists.
	Available bool
	// SizeBytes is the current source file size in bytes when available.
	SizeBytes int64
	// ModifiedAt is the source file modification time when available.
	ModifiedAt time.Time
	// Path is intentionally empty so absolute local paths are not exposed.
	Path string
}

// AppLogSourcesResponse contains fixed application log source metadata.
type AppLogSourcesResponse struct {
	// NodeID identifies the node whose local sources were described.
	NodeID uint64
	// Sources are returned in display order: app, warn, error, debug.
	Sources []AppLogSource
}

// AppLogEntriesRequest requests entries from one fixed application log source.
type AppLogEntriesRequest struct {
	// NodeID identifies the node whose local log is being read.
	NodeID uint64
	// Source selects a fixed source; empty defaults to app.
	Source string
	// Limit bounds returned entries; empty defaults to 200 and values above 1000 are capped.
	Limit int
	// Cursor is an opaque cursor returned by a previous Entries call.
	Cursor string
	// Keyword filters returned entries by raw line substring after reading.
	Keyword string
	// Levels filters returned entries by parsed level after reading.
	Levels []string
}

// AppLogEntry is one parsed application log line.
type AppLogEntry struct {
	// Seq is the byte offset where this line starts in the active file.
	Seq uint64
	// Offset is the byte offset where this line begins in the active file.
	Offset uint64
	// Time is the parsed time field when available.
	Time time.Time
	// Level is the parsed level field when available.
	Level string
	// Module is the parsed module/logger field when available.
	Module string
	// Caller is the parsed caller field when available.
	Caller string
	// Message is the parsed message field when available.
	Message string
	// Fields preserves parsed structured fields not promoted to common fields.
	Fields map[string]any
	// Raw is the raw log line without the trailing newline.
	Raw string
	// Truncated reports whether Raw was shortened to the configured line byte limit.
	Truncated bool
}

// AppLogEntriesResponse contains entries and a cursor for subsequent forward reads.
type AppLogEntriesResponse struct {
	// NodeID identifies the node whose local log was read.
	NodeID uint64
	// Source is the fixed source identifier that was read.
	Source string
	// Cursor is an opaque cursor for forward reads from the active file.
	Cursor string
	// Rotated reports that the supplied cursor did not match the active file.
	Rotated bool
	// Items are returned in file order after post-read filters.
	Items []AppLogEntry
}

type appLogCursor struct {
	Source      string `json:"source"`
	Offset      int64  `json:"offset"`
	Identity    string `json:"identity"`
	DigestStart int64  `json:"digest_start"`
	Digest      string `json:"digest"`
}

// NewAppLogReader builds a reader for fixed node-local application log files.
func NewAppLogReader(opts AppLogReaderOptions) *AppLogReader {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir = defaultDir
	}
	maxTailScanBytes := opts.MaxTailScanBytes
	if maxTailScanBytes <= 0 {
		maxTailScanBytes = defaultAppLogMaxTailScanBytes
	}
	maxLineBytes := opts.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultAppLogMaxLineBytes
	}
	return &AppLogReader{
		dir:              dir,
		maxTailScanBytes: maxTailScanBytes,
		maxLineBytes:     maxLineBytes,
	}
}

// Sources returns metadata for all fixed node-local application log sources.
func (r *AppLogReader) Sources(ctx context.Context, req AppLogSourcesRequest) (AppLogSourcesResponse, error) {
	if err := ctx.Err(); err != nil {
		return AppLogSourcesResponse{}, err
	}
	resp := AppLogSourcesResponse{
		NodeID:  req.NodeID,
		Sources: make([]AppLogSource, 0, len(appLogSources)),
	}
	for _, source := range appLogSources {
		record := AppLogSource{
			Name: source.Source,
			File: source.Filename,
		}
		info, err := os.Stat(r.sourcePath(source))
		if err == nil {
			record.Available = true
			record.SizeBytes = info.Size()
			record.ModifiedAt = info.ModTime()
		} else if !errors.Is(err, os.ErrNotExist) {
			return AppLogSourcesResponse{}, err
		}
		resp.Sources = append(resp.Sources, record)
	}
	return resp, nil
}

// Entries returns a bounded page of parsed entries from one fixed application log source.
func (r *AppLogReader) Entries(ctx context.Context, req AppLogEntriesRequest) (AppLogEntriesResponse, error) {
	source, err := normalizeAppLogSource(req.Source)
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	sourceDef, _ := lookupAppLogSource(source)
	path := r.sourcePath(sourceDef)

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return AppLogEntriesResponse{}, ErrAppLogNotFound
	}
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	identity := fileIdentity(info)
	limit := normalizeAppLogLimit(req.Limit)
	startOffset := int64(0)
	rotated := false
	if strings.TrimSpace(req.Cursor) == "" {
		startOffset, err = tailStartOffset(file, info.Size(), r.maxTailScanBytes, limit)
		if err != nil {
			return AppLogEntriesResponse{}, err
		}
	} else {
		cursor, err := decodeAppLogCursor(req.Cursor)
		if err != nil {
			return AppLogEntriesResponse{}, err
		}
		if cursor.Source != source {
			return AppLogEntriesResponse{}, ErrAppLogInvalidCursor
		}
		matches, err := r.cursorMatches(file, info, identity, cursor)
		if err != nil {
			return AppLogEntriesResponse{}, err
		}
		if matches {
			startOffset = cursor.Offset
		} else {
			rotated = true
		}
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return AppLogEntriesResponse{}, err
	}

	entries, endOffset, _, err := r.readEntries(ctx, file, startOffset, limit, req)
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	cursor, err := r.makeCursor(file, source, identity, endOffset)
	if err != nil {
		return AppLogEntriesResponse{}, err
	}
	return AppLogEntriesResponse{
		NodeID:  req.NodeID,
		Source:  source,
		Items:   entries,
		Cursor:  cursor,
		Rotated: rotated,
	}, nil
}

func (r *AppLogReader) readEntries(ctx context.Context, file *os.File, startOffset int64, limit int, req AppLogEntriesRequest) ([]AppLogEntry, int64, bool, error) {
	levels := make(map[string]struct{}, len(req.Levels))
	for _, level := range req.Levels {
		levels[strings.ToUpper(strings.TrimSpace(level))] = struct{}{}
	}
	var entries []AppLogEntry
	var pending []byte
	offset := startOffset
	pendingStart := startOffset
	truncatedAny := false
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, offset, false, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			offset += int64(n)
			pending = append(pending, chunk...)
			for {
				idx := bytes.IndexByte(pending, '\n')
				if idx < 0 {
					break
				}
				lineStart := pendingStart
				line := pending[:idx]
				pending = pending[idx+1:]
				pendingStart = lineStart + int64(idx) + 1
				entry, truncated := r.parseEntry(lineStart, line)
				if appLogEntryMatches(entry, req.Keyword, levels) {
					truncatedAny = truncatedAny || truncated
					entries = append(entries, entry)
					if len(entries) >= limit {
						return entries, pendingStart, truncatedAny, nil
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, offset, false, readErr
		}
	}
	if len(pending) > 0 {
		entry, truncated := r.parseEntry(pendingStart, pending)
		if appLogEntryMatches(entry, req.Keyword, levels) {
			truncatedAny = truncatedAny || truncated
			entries = append(entries, entry)
		}
	}
	return entries, offset, truncatedAny, nil
}

func (r *AppLogReader) parseEntry(offset int64, line []byte) (AppLogEntry, bool) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	rawLine := line
	raw := string(rawLine)
	truncated := int64(len(rawLine)) > r.maxLineBytes
	if truncated {
		raw = string(rawLine[:r.maxLineBytes])
	}
	entry := AppLogEntry{
		Seq:       uint64(offset),
		Offset:    uint64(offset),
		Raw:       raw,
		Fields:    map[string]any{},
		Truncated: truncated,
	}
	if parseJSONAppLogEntry(&entry, rawLine) {
		return entry, truncated
	}
	parseConsoleAppLogEntry(&entry, raw)
	return entry, truncated
}

func parseJSONAppLogEntry(entry *AppLogEntry, line []byte) bool {
	var fields map[string]any
	if err := json.Unmarshal(line, &fields); err != nil {
		return false
	}
	entry.Time = parseAppLogTime(appLogStringField(fields, "time"))
	entry.Level = appLogStringField(fields, "level")
	entry.Module = appLogStringField(fields, "module")
	entry.Caller = appLogStringField(fields, "caller")
	entry.Message = appLogStringField(fields, "msg")
	for _, key := range []string{"time", "level", "module", "caller", "msg"} {
		delete(fields, key)
	}
	entry.Fields = fields
	return true
}

func parseConsoleAppLogEntry(entry *AppLogEntry, raw string) {
	parts := strings.Split(raw, "\t")
	if len(parts) == 0 {
		return
	}
	if len(parts) > 0 {
		entry.Time = parseAppLogTime(strings.TrimSpace(parts[0]))
	}
	if len(parts) > 1 {
		entry.Level = strings.TrimSpace(parts[1])
	}
	if len(parts) == 4 && isConsoleCallerField(parts[2]) {
		entry.Caller = strings.TrimSpace(parts[2])
		entry.Message = strings.TrimSpace(parts[3])
		return
	}
	if len(parts) > 2 {
		entry.Module = strings.Trim(strings.TrimSpace(parts[2]), "[]")
	}
	if len(parts) > 3 {
		entry.Caller = strings.TrimSpace(parts[3])
	}
	if len(parts) > 4 {
		entry.Message = strings.TrimSpace(parts[4])
	}
	if len(parts) > 5 {
		entry.Fields["extra"] = strings.Join(parts[5:], "\t")
	}
}

func isConsoleCallerField(value string) bool {
	value = strings.TrimSpace(value)
	colon := strings.LastIndex(value, ":")
	if colon <= 0 || colon == len(value)-1 {
		return false
	}
	for _, ch := range value[colon+1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func appLogStringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func parseAppLogTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func appLogEntryMatches(entry AppLogEntry, keyword string, levels map[string]struct{}) bool {
	if keyword != "" && !strings.Contains(entry.Raw, keyword) {
		return false
	}
	if len(levels) > 0 {
		if _, ok := levels[strings.ToUpper(strings.TrimSpace(entry.Level))]; !ok {
			return false
		}
	}
	return true
}

func (r *AppLogReader) makeCursor(file *os.File, source, identity string, offset int64) (string, error) {
	digestStart, digest, err := digestAppLogWindow(file, offset)
	if err != nil {
		return "", err
	}
	return encodeAppLogCursor(appLogCursor{
		Source:      source,
		Offset:      offset,
		Identity:    identity,
		DigestStart: digestStart,
		Digest:      digest,
	})
}

func (r *AppLogReader) cursorMatches(file *os.File, info os.FileInfo, identity string, cursor appLogCursor) (bool, error) {
	if cursor.Offset < 0 || cursor.Offset > info.Size() {
		return false, nil
	}
	if cursor.Identity != "" && identity != "" && cursor.Identity != identity {
		return false, nil
	}
	digestStart, digest, err := digestAppLogWindow(file, cursor.Offset)
	if err != nil {
		return false, err
	}
	if cursor.DigestStart != digestStart || cursor.Digest != digest {
		return false, nil
	}
	return true, nil
}

func digestAppLogWindow(file *os.File, offset int64) (int64, string, error) {
	if offset < 0 {
		return 0, "", ErrAppLogInvalidCursor
	}
	start := offset - appLogCursorDigestBytes
	if start < 0 {
		start = 0
	}
	length := offset - start
	if length == 0 {
		return start, "", nil
	}
	buf := make([]byte, length)
	if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0, "", err
	}
	sum := sha256.Sum256(buf)
	return start, hex.EncodeToString(sum[:]), nil
}

func encodeAppLogCursor(cursor appLogCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeAppLogCursor(encoded string) (appLogCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	var cursor appLogCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	if cursor.Source == "" || cursor.Offset < 0 {
		return appLogCursor{}, ErrAppLogInvalidCursor
	}
	return cursor, nil
}

func normalizeAppLogLimit(limit int) int {
	if limit <= 0 {
		return defaultAppLogLimit
	}
	if limit > maxAppLogLimit {
		return maxAppLogLimit
	}
	return limit
}

func normalizeAppLogSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = AppLogSourceApp
	}
	if _, ok := lookupAppLogSource(source); !ok {
		return "", ErrAppLogInvalidSource
	}
	return source, nil
}

func lookupAppLogSource(source string) (appLogSource, bool) {
	for _, candidate := range appLogSources {
		if candidate.Source == source {
			return candidate, true
		}
	}
	return appLogSource{}, false
}

func (r *AppLogReader) sourcePath(source appLogSource) string {
	return filepath.Join(r.dir, source.Filename)
}

func tailStartOffset(file *os.File, size, maxScanBytes int64, limit int) (int64, error) {
	start := size - maxScanBytes
	if start <= 0 {
		start = 0
	}
	if size == start {
		return start, nil
	}
	data := make([]byte, size-start)
	n, err := file.ReadAt(data, start)
	if err != nil && err != io.EOF {
		return 0, err
	}
	data = data[:n]
	if start > 0 {
		previous := make([]byte, 1)
		if _, err := file.ReadAt(previous, start-1); err != nil && err != io.EOF {
			return 0, err
		}
		if previous[0] != '\n' {
			idx := bytes.IndexByte(data, '\n')
			if idx < 0 {
				return size, nil
			}
			start += int64(idx) + 1
			data = data[idx+1:]
		}
	}
	lineStarts := []int64{start}
	for idx, b := range data {
		if b == '\n' && idx+1 < len(data) {
			lineStarts = append(lineStarts, start+int64(idx)+1)
		}
	}
	if len(lineStarts) > limit {
		return lineStarts[len(lineStarts)-limit], nil
	}
	return start, nil
}

func fileIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return runtime.GOOS + ":size=" + strconv.FormatInt(info.Size(), 10)
	}
	return runtime.GOOS + ":dev=" + strconv.FormatUint(uint64(stat.Dev), 10) + ":ino=" + strconv.FormatUint(uint64(stat.Ino), 10)
}
