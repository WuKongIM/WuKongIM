package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRejectsMissingCommandWithoutStartingWuKongIM(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run(nil, bytes.NewReader(nil), &stdout, &stderr)
	require.Equal(t, 2, exitCode)
	require.Empty(t, stdout.String())
}
