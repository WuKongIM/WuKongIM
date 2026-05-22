package channelplane

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEffectExecutorRunsSubmittedTask(t *testing.T) {
	exec := newEffectExecutor(effectExecutorOptions{Workers: 1, QueueSize: 1})
	exec.start()
	defer stopEffectExecutor(t, exec)

	run := make(chan struct{}, 1)
	require.NoError(t, exec.submit(context.Background(), func() { run <- struct{}{} }))

	select {
	case <-run:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for effect task")
	}
}

func TestEffectExecutorReturnsBackpressureWhenQueueFull(t *testing.T) {
	exec := newEffectExecutor(effectExecutorOptions{Workers: 1, QueueSize: 1})
	exec.start()
	defer stopEffectExecutor(t, exec)

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	require.NoError(t, exec.submit(context.Background(), func() {
		started <- struct{}{}
		<-block
	}))
	<-started
	require.NoError(t, exec.submit(context.Background(), func() {}))
	require.ErrorIs(t, exec.submit(context.Background(), func() {}), ErrOverloaded)
	close(block)
}

func TestEffectExecutorStopRejectsSubmit(t *testing.T) {
	exec := newEffectExecutor(effectExecutorOptions{Workers: 1, QueueSize: 1})
	exec.start()
	stopEffectExecutor(t, exec)

	require.ErrorIs(t, exec.submit(context.Background(), func() {}), ErrClosed)
}

func stopEffectExecutor(t *testing.T, exec *effectExecutor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, exec.stop(ctx))
}
