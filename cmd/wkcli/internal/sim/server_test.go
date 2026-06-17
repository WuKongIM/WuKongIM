package sim

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestStatusServerHealthStatusAndShutdown(t *testing.T) {
	status := newStatus("run-1")
	server := newStatusServer("127.0.0.1:0", status)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.start(ctx)
	}()

	baseURL := "http://" + waitStatusServerAddr(t, server)
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %s", resp.Status)
	}
	resp.Body.Close()

	resp, err = http.Get(baseURL + "/status")
	if err != nil {
		t.Fatalf("GET /status error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status status = %s", resp.Status)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var snapshot Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode /status error = %v", err)
	}
	if snapshot.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", snapshot.RunID)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/status", nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /status error = %v", err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /status status = %s", resp.Status)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server.start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not stop after context cancellation")
	}
}

func waitStatusServerAddr(t *testing.T, server *statusServer) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := server.addr(); addr != "" {
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not publish listen address")
	return ""
}
