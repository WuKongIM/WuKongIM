package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRejectsArbitraryReviewCheckSelector(t *testing.T) {
	t.Parallel()

	require.EqualError(t, run([]string{"bash", "-c", "true"}), "review check selector is required")
	require.EqualError(t, run([]string{"merge"}), "unknown Review check selector")
}
