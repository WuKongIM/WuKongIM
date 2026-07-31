package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewCheckMCPRejectsArguments(t *testing.T) {
	t.Parallel()

	require.EqualError(
		t,
		run(context.Background(), []string{"shell"}),
		"Review Check MCP arguments are invalid",
	)
}
