package cluster

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestChannelMigrationDiagnosticsReportsErrorsWithoutLogFlooding(t *testing.T) {
	log := &migrationDiagnosticLogger{}
	d := channelMigrationDiagnostics{logger: log}
	now := time.Unix(1, 0)
	d.report(now, channels.RepairScannerResult{PagesScanned: 4}, nil, nil)
	require.Empty(t, log.entries, "quiet scans must not consume the error-reporting interval")
	d.report(now, channels.RepairScannerResult{PagesScanned: 2}, errors.New("task commit timed out"), errors.New("slot unavailable"))
	require.Len(t, log.entries, 1)
	require.Equal(t, "warn", log.entries[0].level)
	require.Equal(t, "task commit timed out", log.entries[0].fields["executor_error"])
	require.Equal(t, "slot unavailable", log.entries[0].fields["scanner_error"])
	d.report(now.Add(time.Second), channels.RepairScannerResult{}, errors.New("different error"), nil)
	require.Len(t, log.entries, 1)
	d.report(now.Add(10*time.Second), channels.RepairScannerResult{}, errors.New("retry failed"), nil)
	require.Len(t, log.entries, 2)
}

func TestChannelMigrationDiagnosticsBoundsExamplesAndErrorText(t *testing.T) {
	log := &migrationDiagnosticLogger{}
	d := channelMigrationDiagnostics{logger: log}
	now := time.Unix(1, 0)
	d.report(now, channels.RepairScannerResult{Blocked: make([]channels.RepairScannerBlocked, 200)}, errors.New(strings.Repeat("x", 4096)), nil)
	require.Len(t, log.entries, 1)
	require.Equal(t, 200, log.entries[0].fields["blocked_observations"])
	require.Len(t, log.entries[0].fields["blocked_examples"], 4)
	require.Len(t, log.entries[0].fields["executor_error"], 2051)
	d.report(now.Add(10*time.Second), channels.RepairScannerResult{TasksCreated: 2}, nil, nil)
	require.Equal(t, "info", log.entries[1].level)
}

type migrationDiagnosticEntry struct {
	level  string
	fields map[string]any
}

type migrationDiagnosticLogger struct {
	wklog.Logger
	entries []migrationDiagnosticEntry
}

func (l *migrationDiagnosticLogger) record(level string, fields []wklog.Field) {
	entry := migrationDiagnosticEntry{level: level, fields: make(map[string]any)}
	for _, field := range fields {
		entry.fields[field.Key] = field.Value
	}
	l.entries = append(l.entries, entry)
}

func (l *migrationDiagnosticLogger) Warn(_ string, fields ...wklog.Field) { l.record("warn", fields) }
func (l *migrationDiagnosticLogger) Info(_ string, fields ...wklog.Field) { l.record("info", fields) }
