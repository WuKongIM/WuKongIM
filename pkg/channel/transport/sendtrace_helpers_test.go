package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

func TestSendTraceResultFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want sendtrace.Result
	}{
		{name: "nil", err: nil, want: sendtrace.ResultOK},
		{name: "canceled", err: context.Canceled, want: sendtrace.ResultCanceled},
		{name: "timeout", err: context.DeadlineExceeded, want: sendtrace.ResultTimeout},
		{name: "error", err: errors.New("boom"), want: sendtrace.ResultError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sendTraceResultFromError(tt.err))
		})
	}
}

func TestSendTraceErrorCode(t *testing.T) {
	require.Empty(t, sendTraceErrorCode(nil))
	require.Equal(t, "context_canceled", sendTraceErrorCode(context.Canceled))
	require.Equal(t, "deadline_exceeded", sendTraceErrorCode(context.DeadlineExceeded))
	require.Equal(t, "unknown_error", sendTraceErrorCode(errors.New("boom")))
}

func TestShortTraceErrorBoundsText(t *testing.T) {
	require.Empty(t, shortTraceError(nil))
	require.Equal(t, "boom", shortTraceError(errors.New("boom")))
	require.Len(t, shortTraceError(errors.New(string(make([]byte, 300)))), 256)
}
