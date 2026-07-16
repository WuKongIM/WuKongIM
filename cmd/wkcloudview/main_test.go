package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestDoctorProvesCompletePublicObservationSurface(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(cloudview.GateProbeHeader) != "gate-secret" {
			http.Error(w, "missing gate token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/", "/demo/":
			w.WriteHeader(http.StatusOK)
		case "/manager/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "token", "permissions": []map[string]any{{"resource": "*", "actions": []string{"*"}}},
			})
		case "/manager/nodes":
			if r.Header.Get("Authorization") != "Bearer token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1}, {"id": 2}, {"id": 3}})
		case "/route":
			_ = json.NewEncoder(w).Encode(map[string]string{"ws_addr": "ws" + strings.TrimPrefix(serverURL(r), "http")})
		case "/prometheus/api/v1/targets":
			targets := make([]map[string]string, 7)
			for i := range targets {
				targets[i] = map[string]string{"health": "up"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"activeTargets": targets}})
		case "/ws":
			connection, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				_ = connection.Close()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := runDoctor(t.Context(), doctorOptions{
		BaseURL: server.URL, Username: "admin", Password: "a1234567", ExpectedTargets: 7,
		WebSocketPath: "/ws", GateToken: "gate-secret",
	})
	if err != nil {
		t.Fatalf("runDoctor() error = %v", err)
	}
	if !result.Manager || !result.Demo || !result.RouteRewrite || !result.WebSocket || result.PrometheusTargetsUp != 7 {
		t.Fatalf("doctor result = %#v", result)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}

func TestAnnotateReportPersistsBenchmarkPurity(t *testing.T) {
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "report.json")
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cloud-view/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(cloudViewStatus{
			State:              cloudviewstate.State{RunID: "run-1", OperatorModified: true, UpdatedAt: time.Now().UTC()},
			PersistenceHealthy: true,
		})
	}))
	t.Cleanup(statusServer.Close)
	if err := os.WriteFile(reportPath, []byte(`{"run_id":"run-1","status":"pass","counter":18446744073709551615}`), 0o640); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"annotate-report", "--status-url", statusServer.URL + "/cloud-view/status", "--report", reportPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("annotate-report code=%d stderr=%q", code, stderr.String())
	}
	var report struct {
		BenchmarkPurity benchmarkPurity `json:"benchmark_purity"`
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
	code := execute([]string{"annotate-report", "--status-url", "http://127.0.0.1:1/cloud-view/status", "--report", reportPath}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("annotate-report succeeded without live Cloud View status")
	}
	var report struct {
		BenchmarkPurity benchmarkPurity `json:"benchmark_purity"`
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
}
