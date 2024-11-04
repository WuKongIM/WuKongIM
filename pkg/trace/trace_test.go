package trace_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/stretchr/testify/require"
)

func TestTraceStartAndStop(t *testing.T) {
	trace := trace.New(context.Background(), trace.NewOptions())
	err := trace.Start()
	require.NoError(t, err)
	trace.Stop()
}
