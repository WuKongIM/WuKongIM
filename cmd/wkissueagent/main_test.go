package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssueAgentMainRejectsLegacyWorkerCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	exit := run(
		[]string{"run-worker"},
		bytes.NewBufferString(`{}`),
		&stdout,
		&stderr,
	)
	require.Equal(t, 2, exit)
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "unknown command")
}

func TestParsePositiveInt64(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(42), parsePositiveInt64("42"))
	require.Zero(t, parsePositiveInt64("0"))
	require.Zero(t, parsePositiveInt64("not-a-number"))
}
