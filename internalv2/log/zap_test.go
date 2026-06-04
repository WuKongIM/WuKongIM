package log

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

func TestNewLoggerWritesNamedAndContextualFields(t *testing.T) {
	dir := t.TempDir()

	logger, err := NewLogger(Config{
		Dir:     dir,
		Level:   "debug",
		Console: false,
		Format:  "json",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	child := logger.Named("cluster").With(
		wklog.String("peerID", "node-2"),
		wklog.Int("attempt", 3),
	)
	child.Info("connecting")
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	entry := readSingleJSONLogEntry(t, filepath.Join(dir, "app.log"))
	if entry["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", entry["level"])
	}
	if entry["module"] != "cluster" {
		t.Fatalf("module = %v, want cluster", entry["module"])
	}
	if entry["msg"] != "connecting" {
		t.Fatalf("msg = %v, want connecting", entry["msg"])
	}
	if entry["peerID"] != "node-2" {
		t.Fatalf("peerID = %v, want node-2", entry["peerID"])
	}
	if entry["attempt"] != float64(3) {
		t.Fatalf("attempt = %v, want 3", entry["attempt"])
	}
}

func TestNewLoggerRoutesLevelsToSeparateFiles(t *testing.T) {
	dir := t.TempDir()

	logger, err := NewLogger(Config{
		Dir:     dir,
		Level:   "debug",
		Console: false,
		Format:  "json",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	errBoom := errors.New("boom")
	logger.Debug("debugging", wklog.Bool("ready", true))
	logger.Info("starting", wklog.Uint64("nodeID", 1))
	logger.Warn("slow", wklog.String("component", "delivery"))
	logger.Error("failed", wklog.Error(errBoom))
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	debugContents := readLogFile(t, filepath.Join(dir, "debug.log"))
	assertContains(t, debugContents, `"msg":"debugging"`)
	assertContains(t, debugContents, `"msg":"starting"`)
	assertContains(t, debugContents, `"msg":"slow"`)
	assertContains(t, debugContents, `"msg":"failed"`)

	appContents := readLogFile(t, filepath.Join(dir, "app.log"))
	assertNotContains(t, appContents, `"msg":"debugging"`)
	assertContains(t, appContents, `"msg":"starting"`)
	assertContains(t, appContents, `"msg":"slow"`)
	assertContains(t, appContents, `"msg":"failed"`)

	warnContents := readLogFile(t, filepath.Join(dir, "warn.log"))
	assertNotContains(t, warnContents, `"msg":"debugging"`)
	assertNotContains(t, warnContents, `"msg":"starting"`)
	assertContains(t, warnContents, `"msg":"slow"`)
	assertNotContains(t, warnContents, `"msg":"failed"`)
	assertContains(t, warnContents, `"component":"delivery"`)

	errorContents := readLogFile(t, filepath.Join(dir, "error.log"))
	assertNotContains(t, errorContents, `"msg":"debugging"`)
	assertNotContains(t, errorContents, `"msg":"starting"`)
	assertNotContains(t, errorContents, `"msg":"slow"`)
	assertContains(t, errorContents, `"msg":"failed"`)
	assertContains(t, errorContents, `"error":"boom"`)
}

func TestDisabledDebugSkipsFieldConversion(t *testing.T) {
	logger, err := NewLogger(Config{
		Dir:     t.TempDir(),
		Level:   "info",
		Console: false,
		Format:  "json",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	if wklog.DebugEnabled(logger) {
		t.Fatal("DebugEnabled() = true, want false")
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Debug() panicked for disabled debug field conversion: %v", recovered)
		}
	}()
	logger.Debug("skipped", wklog.Field{Key: "bad", Type: wklog.IntType, Value: "not-int"})
}

func TestToZapFieldsConvertsAllSupportedTypes(t *testing.T) {
	errBoom := errors.New("boom")
	duration := 250 * time.Millisecond

	fields := toZapFields([]wklog.Field{
		wklog.String("s", "v"),
		wklog.Int("i", 1),
		wklog.Int64("i64", 2),
		wklog.Uint64("u64", 3),
		wklog.Float64("f64", 1.5),
		wklog.Bool("b", true),
		wklog.Error(errBoom),
		wklog.Duration("d", duration),
		wklog.Any("any", map[string]int{"n": 1}),
	})

	if len(fields) != 9 {
		t.Fatalf("converted field count = %d, want 9", len(fields))
	}
	if enc := zap.NewExample().With(fields...).Core(); enc == nil {
		t.Fatal("zap core is nil after field conversion")
	}
}

func readSingleJSONLogEntry(t *testing.T, path string) map[string]any {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("expected at least one log line in %s", path)
	}

	var entry map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error = %v", err)
	}
	return entry
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected log to contain %q:\n%s", needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected log not to contain %q:\n%s", needle, haystack)
	}
}
