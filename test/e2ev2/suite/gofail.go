//go:build e2e

package suite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// GofailEndpoint controls one node-local gofail HTTP server.
type GofailEndpoint struct {
	// Addr is the loopback host:port assigned to GOFAIL_HTTP.
	Addr string
}

// ReserveGofailEndpoint reserves and releases one loopback address for a gofail HTTP server.
func ReserveGofailEndpoint(t testing.TB) GofailEndpoint {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve gofail endpoint: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved gofail listener: %v", err)
	}
	return GofailEndpoint{Addr: addr}
}

func (e GofailEndpoint) Env() string { return "GOFAIL_HTTP=" + e.Addr }

func (e GofailEndpoint) BaseURL() string { return "http://" + e.Addr }

func (e GofailEndpoint) Enable(ctx context.Context, name string, expression string) error {
	name, err := cleanFailpointName(name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, e.BaseURL()+"/"+name, bytes.NewBufferString(expression))
	if err != nil {
		return err
	}
	return doGofailRequest(req)
}

func (e GofailEndpoint) Disable(ctx context.Context, name string) error {
	name, err := cleanFailpointName(name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.BaseURL()+"/"+name, nil)
	if err != nil {
		return err
	}
	return doGofailRequest(req)
}

func (e GofailEndpoint) List(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL()+"/", nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gofail list: status=%s body=%q", res.Status, string(body))
	}
	return string(body), nil
}

func (e GofailEndpoint) WaitListed(ctx context.Context, names ...string) (string, error) {
	want := make([]string, 0, len(names))
	for _, name := range names {
		cleaned, err := cleanFailpointName(name)
		if err != nil {
			return "", err
		}
		want = append(want, cleaned+"=")
	}

	var lastBody string
	var lastErr error
	for {
		body, err := e.List(ctx)
		if err == nil {
			lastBody = body
			allListed := true
			for _, name := range want {
				if !strings.Contains(body, name) {
					allListed = false
					break
				}
			}
			if allListed {
				return body, nil
			}
		} else {
			lastErr = err
		}

		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return "", fmt.Errorf("wait gofail list %v: %w; last_body=%q last_error=%v", names, ctx.Err(), lastBody, lastErr)
		case <-timer.C:
		}
	}
}

func cleanFailpointName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("gofail failpoint name is empty")
	}
	return name, nil
}

func doGofailRequest(req *http.Request) error {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("gofail %s %s: status=%s body=%q", req.Method, req.URL.String(), res.Status, string(body))
	}
	return nil
}
