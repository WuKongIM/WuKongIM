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
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		client, err := NewWKProtoClient()
		if err != nil {
			return err
		}

		_, err = client.ConnectContext(ctx, addr, "e2ev2-ready", "e2ev2-ready-device")
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
		case <-ticker.C:
		}
	}
}

// WaitHTTPReady waits until the HTTP endpoint starts returning status 200.
func WaitHTTPReady(ctx context.Context, addr, path string) error {
	_, err := waitHTTPReadyDetailed(ctx, addr, path)
	return err
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
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	var (
		lastErr error
		lastObs HTTPObservation
	)
	for {
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
		case <-ticker.C:
		}
	}
}
