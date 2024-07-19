package trace_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/stretchr/testify/require"
)

func TestTraceStartAndStop(t *testing.T) {
	trace := trace.New(context.Background(), trace.NewOptions())
	err := trace.Start()
	require.NoError(t, err)
	trace.Stop()
}

func TestTraceStartSpan(t *testing.T) {
	trace := trace.New(context.Background(), trace.NewOptions())
	err := trace.Start()
	defer trace.Stop()

	require.NoError(t, err)
	_, span := trace.StartSpan(context.Background(), "test")
	span.SetInt("key", 2)
	span.End()

	time.Sleep(time.Second * 10)

}
