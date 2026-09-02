package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
	"github.com/WuKongIM/WuKongIM/internal/runtime/cloudviewstate"
)

func TestValidateAcceptsStrictCloudViewConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudview.json")
	body := `{
  "listen_addr": "0.0.0.0:19443",
  "run_id": "gh-123-1",
  "public_base_url": "http://198.51.100.20:19443",
  "prometheus_url": "http://127.0.0.1:9090",
  "state_path": "/var/lib/wukongim-cloud/cloud-view-state.json",
  "metrics_path": "/var/lib/wukongim/textfile/cloud-view.prom",
  "nodes": [
    {"id":1,"api_base_url":"http://10.42.0.11:5001","manager_base_url":"http://10.42.0.11:5301","websocket_base_url":"http://10.42.0.11:5200"}
  ],
  "limits": {
    "http_requests_per_second_per_ip":30,
    "http_burst_per_ip":60,
    "http_requests_per_second_global":200,
    "http_burst_global":400,
    "websocket_connections_per_ip":20,
    "websocket_connections_global":64
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := execute([]string{"validate", "--config", path}, &stdout, &stderr)

	if code != 0 || strings.TrimSpace(stdout.String()) != "valid" || stderr.Len() != 0 {
		t.Fatalf("validate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLoadConfigRejectsOversizedWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudview.json")
	body := append([]byte(`{"listen_addr":"127.0.0.1:19443","run_id":"run-1"}`), bytes.Repeat([]byte{' '}, maxConfigBytes)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfig(path, func(string) string { return "" }); err == nil {
		t.Fatal("loadConfig() accepted a config larger than maxConfigBytes")
	}
}

func TestDoctorProvesCompletePublicObservationSurface(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get(cloudview.GateProbeHeader) != "gate-secret" {
			t.Fatalf("gate token = %q", r.Header.Get(cloudview.GateProbeHeader))
		}
		switch r.URL.Path {
		case "/", "/demo/":
			return response(http.StatusOK, ""), nil
		case "/manager/login":
			return response(http.StatusOK, `{"access_token":"token","permissions":[{"resource":"*","actions":["*"]}]}`), nil
		case "/manager/nodes":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			return response(http.StatusOK, `[{"id":1},{"id":2},{"id":3}]`), nil
		case "/route":
			return response(http.StatusOK, `{"ws_addr":"ws://cloudview.test"}`), nil
		case "/prometheus/api/v1/targets":
			return response(http.StatusOK, `{"status":"success","data":{"activeTargets":[{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"}]}}`), nil
		default:
			t.Fatalf("unexpected doctor request %s %s", r.Method, r.URL.String())
			return response(http.StatusNotFound, ""), nil
		}
	})
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	originalNetDial := websocket.DefaultDialer.NetDialContext
	originalProxy := websocket.DefaultDialer.Proxy
	handshakeDone := make(chan error, 1)
	websocket.DefaultDialer.Proxy = nil
	websocket.DefaultDialer.NetDialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
		clientConnection, serverConnection := net.Pipe()
		if err := serverConnection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			_ = clientConnection.Close()
			_ = serverConnection.Close()
			return nil, err
		}
		go func() { handshakeDone <- serveDoctorWebSocketHandshake(serverConnection) }()
		return clientConnection, nil
	}
	t.Cleanup(func() {
		websocket.DefaultDialer.NetDialContext = originalNetDial
		websocket.DefaultDialer.Proxy = originalProxy
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result, err := runDoctor(ctx, doctorOptions{
		BaseURL: "http://cloudview.test", Username: "admin", Password: "a1234567", ExpectedTargets: 7,
		WebSocketPath: "/ws", GateToken: "gate-secret",
	})
	if err != nil {
		t.Fatalf("runDoctor() error = %v", err)
	}
	if !result.Manager || !result.Demo || !result.RouteRewrite || !result.WebSocket || result.PrometheusTargetsUp != 7 {
		t.Fatalf("doctor result = %#v", result)
	}
	select {
	case err := <-handshakeDone:
		if err != nil {
			t.Fatalf("in-memory WebSocket handshake: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("in-memory WebSocket handshake did not finish: %v", ctx.Err())
	}
}

func serveDoctorWebSocketHandshake(connection net.Conn) error {
	defer connection.Close()
	request, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		return err
	}
	if request.URL.Path != "/ws" || request.Header.Get(cloudview.GateProbeHeader) != "gate-secret" {
		return fmt.Errorf("unexpected WebSocket request path=%q headers=%v", request.URL.Path, request.Header)
	}
	digest := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if _, err := fmt.Fprintf(connection, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:])); err != nil {
		return err
	}
	buffer := make([]byte, 1)
	_, err = connection.Read(buffer)
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func TestAnnotateReportPersistsBenchmarkPurity(t *testing.T) {
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "report.json")
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cloud-view/status" {
			return response(http.StatusNotFound, ""), nil
		}
		body, err := json.Marshal(cloudViewStatus{
			State:              cloudviewstate.State{RunID: "run-1", OperatorModified: true, UpdatedAt: time.Now().UTC()},
			PersistenceHealthy: true,
		})
		if err != nil {
			return nil, err
		}
		return response(http.StatusOK, string(body)), nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	if err := os.WriteFile(reportPath, []byte(`{"run_id":"run-1","status":"pass","counter":18446744073709551615}`), 0o640); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"annotate-report", "--status-url", "http://127.0.0.1/cloud-view/status", "--report", reportPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("annotate-report code=%d stderr=%q", code, stderr.String())
	}
	var report struct {
		BenchmarkPurity  benchmarkPurity `json:"benchmark_purity"`
		StabilityVerdict string          `json:"stability_verdict"`
	}
	body, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.BenchmarkPurity.Pure || !report.BenchmarkPurity.StateKnown || !report.BenchmarkPurity.PersistenceHealthy ||
		!report.BenchmarkPurity.OperatorModified || report.BenchmarkPurity.Interactive {
		t.Fatalf("benchmark purity = %#v", report.BenchmarkPurity)
	}
	if report.StabilityVerdict != "operator_modified" {
		t.Fatalf("stability_verdict = %q, want operator_modified", report.StabilityVerdict)
	}
	if !bytes.Contains(body, []byte("18446744073709551615")) {
		t.Fatalf("large report counter changed during annotation: %s", body)
	}
}

func TestAnnotateReportWritesFailClosedPurityWhenStatusUnavailable(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"run_id":"run-1","status":"pass"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute([]string{"annotate-report", "--status-url", "https://127.0.0.1/cloud-view/status", "--report", reportPath}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("annotate-report succeeded without live Cloud View status")
	}
	var report struct {
		BenchmarkPurity  benchmarkPurity `json:"benchmark_purity"`
		StabilityVerdict string          `json:"stability_verdict"`
	}
	body, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.BenchmarkPurity.Pure || report.BenchmarkPurity.StateKnown || report.BenchmarkPurity.PersistenceHealthy {
		t.Fatalf("unavailable status purity = %#v, want explicit fail-closed state", report.BenchmarkPurity)
	}
	if report.StabilityVerdict != "insufficient_evidence" {
		t.Fatalf("stability_verdict = %q, want insufficient_evidence", report.StabilityVerdict)
	}
}
