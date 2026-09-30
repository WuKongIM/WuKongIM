//go:build integration

package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Queued envelopes retain their undecoded wire bytes, so waiting is bounded
// by bytes as well as count. Failure cases written before implementation:
//   - an envelope that would exceed the queued-byte budget waits instead of
//     failing fast, so a burst of large envelopes pins memory;
//   - a rejected envelope leaks queued bytes and later small ones are refused;
//   - a granted, canceled or timed-out waiter does not return its bytes.
func TestSendPermissionWaitingBytesAreBounded(t *testing.T) {
	s := &Store{}
	var releases []func()
	t.Cleanup(func() {
		for _, release := range releases {
			release()
		}
	})
	for range sendPermissionMaxExecuting {
		release, err := s.acquireSendPermissionEnvelope(context.Background(), 0)
		require.NoError(t, err)
		releases = append(releases, release)
	}

	// Fill the byte budget with one waiter, then an oversized arrival must be
	// rejected immediately rather than after the wait deadline.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held := make(chan error, 1)
	go func() {
		release, err := s.acquireSendPermissionEnvelope(ctx, sendPermissionMaxWaitingBytes)
		if release != nil {
			release()
		}
		held <- err
	}()
	require.Eventually(t, func() bool { return s.permissionWaiting.Load() == 1 }, time.Second, time.Millisecond)
	started := time.Now()
	release, err := s.acquireSendPermissionEnvelope(context.Background(), 1)
	require.Nil(t, release)
	require.ErrorIs(t, err, ErrPermissionBusy)
	require.Less(t, time.Since(started), sendPermissionMaxWait/2, "byte-budget rejection must not wait")

	// Canceling the queued waiter must return its bytes.
	cancel()
	require.ErrorIs(t, <-held, context.Canceled)
	require.Zero(t, s.permissionWaiting.Load())
	require.Zero(t, s.permissionWaitingBytes)

	// A granted waiter also returns its bytes once it owns a permit.
	granted := make(chan error, 1)
	go func() {
		release, err := s.acquireSendPermissionEnvelope(context.Background(), sendPermissionMaxWaitingBytes)
		if release != nil {
			release()
		}
		granted <- err
	}()
	require.Eventually(t, func() bool { return s.permissionWaiting.Load() == 1 }, time.Second, time.Millisecond)
	releases[0]()
	releases = releases[1:]
	require.NoError(t, <-granted)
	require.Zero(t, s.permissionWaitingBytes)
}
