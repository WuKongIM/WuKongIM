//go:build e2e

package suite

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const readyPollInterval = 100 * time.Millisecond

// HTTPObservation captures the last observed HTTP status/body pair from a probe.
type HTTPObservation struct {
	StatusCode int
	Body       string
}

// WaitWKProtoReady waits until a real WKProto handshake succeeds on the address.
func WaitWKProtoReady(ctx context.Context, addr string) error {
	return waitWKProtoReady(ctx, addr, nil)
}

func waitWKProtoReady(ctx context.Context, addr string, process *NodeProcess) error {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	processDone := process.Done()

	var lastErr error
	for {
		if err := processReadinessExitError(process, "WKProto readiness"); err != nil {
			return err
		}
		client, err := NewWKProtoClient()
		if err != nil {
			return err
		}

		_, err = client.ConnectContext(ctx, addr, "e2e-ready", "e2e-ready-device")
		_ = client.Close()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-processDone:
			return processReadinessExitErrorAfterDone(process, "WKProto readiness")
		case <-ticker.C:
		}
	}
}

// WaitHTTPReady waits until the HTTP endpoint starts returning status 200.
func WaitHTTPReady(ctx context.Context, addr, path string) error {
	_, err := waitHTTPReadyDetailed(ctx, addr, path)
	return err
}

// WaitHTTPReady waits for HTTP readiness while also failing when this child exits.
func (p *NodeProcess) WaitHTTPReady(ctx context.Context, addr, path string) (HTTPObservation, error) {
	return waitHTTPReadyDetailedForProcess(ctx, addr, path, p)
}

// WaitWKProtoReady waits for WKProto readiness while also failing when this child exits.
func (p *NodeProcess) WaitWKProtoReady(ctx context.Context, addr string) error {
	return waitWKProtoReady(ctx, addr, p)
}

// WaitTCPReady waits until a TCP listener accepts a connection on addr.
func WaitTCPReady(ctx context.Context, addr string) error {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitHTTPReadyDetailed(ctx context.Context, addr, path string) (HTTPObservation, error) {
	return waitHTTPReadyDetailedForProcess(ctx, addr, path, nil)
}

func waitHTTPReadyDetailedForProcess(ctx context.Context, addr, path string, process *NodeProcess) (HTTPObservation, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	processDone := process.Done()

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	var (
		lastErr error
		lastObs HTTPObservation
	)
	for {
		if err := processReadinessExitError(process, "HTTP readiness"); err != nil {
			return lastObs, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		if err != nil {
			return lastObs, err
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else {
				lastObs = HTTPObservation{
					StatusCode: resp.StatusCode,
					Body:       strings.TrimSpace(string(body)),
				}
				if resp.StatusCode == http.StatusOK {
					return lastObs, nil
				}
				lastErr = fmt.Errorf("http readiness %s returned %d: %s", path, resp.StatusCode, lastObs.Body)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastObs, lastErr
			}
			return lastObs, ctx.Err()
		case <-processDone:
			return lastObs, processReadinessExitErrorAfterDone(process, "HTTP readiness")
		case <-ticker.C:
		}
	}
}

func processReadinessExitError(process *NodeProcess, readiness string) error {
	if process == nil {
		return nil
	}
	err, exited := process.ExitResult()
	if !exited {
		return nil
	}
	if err != nil {
		return fmt.Errorf("process exited before %s: %w", readiness, err)
	}
	return fmt.Errorf("process exited before %s", readiness)
}

func processReadinessExitErrorAfterDone(process *NodeProcess, readiness string) error {
	if err := processReadinessExitError(process, readiness); err != nil {
		return err
	}
	return fmt.Errorf("process exited before %s", readiness)
}
