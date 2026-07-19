package workload

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldContinueTrafficOperationError(t *testing.T) {
	sessionErr := sessionOperationError("u-1", "group sendack", errors.New("sendack timeout"))

	require.True(t, shouldContinueTrafficOperationError(context.Background(), "warmup", sessionErr))
	require.False(t, shouldContinueTrafficOperationError(context.Background(), "warmup", errors.New("invalid channel plan")))
	require.True(t, shouldContinueTrafficOperationError(context.Background(), "run", errors.New("message failed")))
	require.True(t, shouldContinueTrafficOperationError(context.Background(), "run-window-2", errors.New("message failed")))
	require.False(t, shouldContinueTrafficOperationError(context.Background(), "cooldown", sessionErr))

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, shouldContinueTrafficOperationError(canceled, "warmup", context.Canceled))
}
