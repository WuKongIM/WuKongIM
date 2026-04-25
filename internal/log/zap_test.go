package log

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)

	child := logger.Named("cluster").With(
		wklog.String("peerID", "node-2"),
		wklog.Int("attempt", 3),
	)
	child.Info("connecting")
	require.NoError(t, logger.Sync())

	entry := readSingleJSONLogEntry(t, filepath.Join(dir, "app.log"))
	require.Equal(t, "INFO", entry["level"])
	require.Equal(t, "cluster", entry["module"])
	require.Equal(t, "connecting", entry["msg"])
	require.Equal(t, "node-2", entry["peerID"])
	require.EqualValues(t, 3, entry["attempt"])
	require.Contains(t, entry["caller"], "zap_test.go")
}

func TestNewLoggerWritesNestedModuleAndSemanticFields(t *testing.T) {
	dir := t.TempDir()

	logger, err := NewLogger(Config{
		Dir:     dir,
		Level:   "debug",
		Console: false,
		Format:  "json",
	})
	require.NoError(t, err)

	logger.Named("message").Named("send").Error(
		"persist committed message failed",
		wklog.Event("message.send.persist.failed"),
		wklog.ChannelID("u1@u2"),
		wklog.MessageID(88),
		wklog.Error(errors.New("boom")),
	)
	require.NoError(t, logger.Sync())

	entry := readSingleJSONLogEntry(t, filepath.Join(dir, "app.log"))
	require.Equal(t, "ERROR", entry["level"])
	require.Equal(t, "message.send", entry["module"])
	require.Equal(t, "persist committed message failed", entry["msg"])
	require.Equal(t, "message.send.persist.failed", entry["event"])
	require.Equal(t, "u1@u2", entry["channelID"])
	require.EqualValues(t, 88, entry["messageID"])
	require.Equal(t, "boom", entry["error"])
}

func TestNewLoggerRoutesLevelsToSeparateFiles(t *testing.T) {
	dir := t.TempDir()

	logger, err := NewLogger(Config{
		Dir:     dir,
		Level:   "debug",
		Console: false,
		Format:  "json",
	})
	require.NoError(t, err)

	errBoom := errors.New("boom")
	logger.Debug("debugging", wklog.Bool("ready", true))
	logger.Info("starting", wklog.Uint64("nodeID", 1))
	logger.Error("failed", wklog.Error(errBoom))
	require.NoError(t, logger.Sync())

	debugContents := readLogFile(t, filepath.Join(dir, "debug.log"))
	require.Contains(t, debugContents, `"msg":"debugging"`)
	require.Contains(t, debugContents, `"msg":"starting"`)
	require.Contains(t, debugContents, `"msg":"failed"`)

	appContents := readLogFile(t, filepath.Join(dir, "app.log"))
	require.NotContains(t, appContents, `"msg":"debugging"`)
	require.Contains(t, appContents, `"msg":"starting"`)
	require.Contains(t, appContents, `"msg":"failed"`)

	errorContents := readLogFile(t, filepath.Join(dir, "error.log"))
	require.NotContains(t, errorContents, `"msg":"debugging"`)
	require.NotContains(t, errorContents, `"msg":"starting"`)
	require.Contains(t, errorContents, `"msg":"failed"`)
	require.Contains(t, errorContents, `"error":"boom"`)
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

	require.Len(t, fields, 9)
	enc := zap.NewExample().With(fields...).Core()
	require.NotNil(t, enc)
}

func readSingleJSONLogEntry(t *testing.T, path string) map[string]any {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()

	scanner := bufio.NewScanner(file)
	require.True(t, scanner.Scan(), "expected at least one log line")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry))
	require.NoError(t, scanner.Err())
	return entry
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
