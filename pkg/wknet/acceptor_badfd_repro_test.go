//go:build linux

package wknet

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// TestEngineE2EAvoidsAcceptorBadFDUnderIdleChurn verifies the server does not
// emit the acceptor bad-fd warning even under aggressive idle-close churn.
func TestEngineE2EAvoidsAcceptorBadFDUnderIdleChurn(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)

	const trials = 8
	for trial := 1; trial <= trials; trial++ {
		observed, excerpt, err := runBadFDReproTrial(t, trial)
		if err != nil {
			t.Fatalf("trial %d failed: %v", trial, err)
		}
		if excerpt != "" {
			t.Logf("log excerpt from trial %d:\n%s", trial, excerpt)
		}
		if observed {
			t.Fatalf("observed %q on trial %d", "subReactor.AddConn() failed", trial)
		}
	}
}

func runBadFDReproTrial(t *testing.T, trial int) (bool, string, error) {
	t.Helper()

	logDir := t.TempDir()
	logOpts := wklog.NewOptions()
	logOpts.LogDir = logDir
	logOpts.Level = zap.DebugLevel
	logOpts.NoStdout = true
	wklog.Configure(logOpts)

	const idleTimeout = 30 * time.Millisecond
	engine := NewEngine(
		WithAddr("tcp://127.0.0.1:0"),
		WithSubReactorNum(1),
	)
	engine.OnNewConn(func(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
		return &badFDReproConn{
			DefaultConn:      GetDefaultConn(id, connFd, localAddr, remoteAddr, eg, reactorSub),
			holdAfterRelease: 30 * time.Millisecond,
		}, nil
	})
	engine.OnConnect(func(conn Conn) error {
		conn.SetMaxIdle(idleTimeout)
		return nil
	})

	if err := engine.Start(); err != nil {
		return false, "", fmt.Errorf("start engine: %w", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = engine.Stop()
		}
		_ = wklog.Sync()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()

	addr := engine.TCPRealListenAddr().String()
	var wg sync.WaitGroup
	for worker := 0; worker < 96; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			seed := int64(trial*1000 + worker + 1)
			rnd := rand.New(rand.NewSource(seed))
			dialer := &net.Dialer{Timeout: 100 * time.Millisecond}

			for {
				if ctx.Err() != nil {
					return
				}

				conn, err := dialer.DialContext(ctx, "tcp", addr)
				if err != nil {
					if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
						return
					}
					continue
				}

				if tcpConn, ok := conn.(*net.TCPConn); ok {
					_ = tcpConn.SetLinger(0)
					_ = tcpConn.SetNoDelay(true)
				}

				hold := idleTimeout - time.Millisecond + time.Duration(rnd.Intn(4))*time.Millisecond
				timer := time.NewTimer(hold)
				select {
				case <-ctx.Done():
					timer.Stop()
					_ = conn.Close()
					return
				case <-timer.C:
					_ = conn.Close()
				}
			}
		}(worker)
	}

	<-ctx.Done()
	wg.Wait()

	if err := engine.Stop(); err != nil {
		return false, "", fmt.Errorf("stop engine: %w", err)
	}
	stopped = true
	if err := wklog.Sync(); err != nil {
		return false, "", fmt.Errorf("sync logs: %w", err)
	}

	return badFDObserved(logDir)
}

type badFDReproConn struct {
	*DefaultConn
	holdAfterRelease time.Duration
}

func (c *badFDReproConn) Close() error {
	return c.closeWithPause(nil)
}

func (c *badFDReproConn) CloseWithErr(err error) error {
	return c.closeWithPause(err)
}

func (c *badFDReproConn) closeWithPause(closeErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed.Load() {
		return nil
	}
	c.closed.Store(true)

	if closeErr != nil && !errors.Is(closeErr, syscall.ECONNRESET) {
		if err := c.reactorSub.DeleteFd(c); err != nil {
			c.Debug("delete fd from poller error", zap.Error(err), zap.Int("fd", c.Fd().fd), zap.String("uid", c.uid.Load()))
		}
	}

	_ = c.fd.Close()
	c.eg.RemoveConn(c)
	c.reactorSub.ConnDec()
	c.eg.eventHandler.OnClose(c)
	c.release()

	if c.holdAfterRelease > 0 {
		time.Sleep(c.holdAfterRelease)
	}
	return nil
}

func badFDObserved(logDir string) (bool, string, error) {
	warnLog := filepath.Join(logDir, "warn.log")
	data, err := os.ReadFile(warnLog)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}

	content := string(data)
	if !strings.Contains(content, "【Acceptor】subReactor.AddConn() failed") {
		return false, "", nil
	}
	if !strings.Contains(content, "bad file descriptor") {
		return false, "", nil
	}

	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return true, strings.Join(lines, "\n"), nil
}
