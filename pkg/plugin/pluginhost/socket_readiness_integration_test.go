//go:build integration

package pluginhost

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
)

// CPU throttling may delay every connack beyond a short connection probe while
// the host is still responsive within the overall startup readiness budget.
func TestWaitUnixSocketReadyAllowsDelayedHandshakeWithinBudget(t *testing.T) {
	path := filepath.Join(shortSocketTempDir(t), "delayed.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	body, err := (&wkrpcproto.Connack{Id: 1, Status: wkrpcproto.StatusOK}).Marshal()
	require.NoError(t, err)
	packet, err := wkrpcproto.New().Encode(body, wkrpcproto.MsgTypeConnack)
	require.NoError(t, err)
	stop := make(chan struct{})
	done := make(chan error, 1)
	var workers sync.WaitGroup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					err = nil
				}
				done <- err
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				timer := time.NewTimer(200 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					_, _ = conn.Write(packet)
				case <-stop:
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); close(stop); require.NoError(t, <-done); workers.Wait() })
	require.NoError(t, waitUnixSocketReady(path, 800*time.Millisecond))
}

func TestWaitUnixSocketReadyRejectsSilentListenerWithinBudget(t *testing.T) {
	path := filepath.Join(shortSocketTempDir(t), "silent.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer listener.Close()
	started := time.Now()
	err = waitUnixSocketReady(path, 150*time.Millisecond)
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
}
