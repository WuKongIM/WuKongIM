package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
)

func TestLoadConfigAppliesEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudview.json")
	body := `{
  "listen_addr":"127.0.0.1:19443",
  "run_id":"run-1",
  "public_base_url":"http://198.51.100.10:19443",
  "gate_probe_token":"file-token"
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"WK_CLOUD_VIEW_PUBLIC_BASE_URL":  "  http://203.0.113.20:19443  ",
		"WK_CLOUD_VIEW_GATE_PROBE_TOKEN": "  environment-token  ",
	}
	configured, err := loadConfig(path, func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	if configured.PublicBaseURL != "http://203.0.113.20:19443" || configured.GateProbeToken != "environment-token" {
		t.Fatalf("environment overrides = base %q token %q", configured.PublicBaseURL, configured.GateProbeToken)
	}
}

func TestLoadConfigRejectsNonStrictInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{"},
		{name: "unknown field", body: `{"listen_addr":"127.0.0.1:19443","run_id":"run-1","unknown":true}`},
		{name: "trailing document", body: `{"listen_addr":"127.0.0.1:19443","run_id":"run-1"} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cloudview.json")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path, func(string) string { return "" }); err == nil {
				t.Fatal("loadConfig() accepted non-strict input")
			}
		})
	}
	if _, err := loadConfig(filepath.Join(t.TempDir(), "missing.json"), func(string) string { return "" }); err == nil {
		t.Fatal("loadConfig(missing) succeeded")
	}
}

func TestValidateConfigRejectsInvalidInputWithoutStateWrites(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	metricsPath := filepath.Join(t.TempDir(), "metrics.prom")
	valid := fileConfig{
		ListenAddr: "127.0.0.1:19443",
		Options: cloudview.Options{
			RunID:         "run-1",
			PublicBaseURL: "http://198.51.100.10:19443",
			PrometheusURL: "http://127.0.0.1:9090",
			Nodes: []cloudview.NodeUpstream{{
				ID: 1, APIBaseURL: "http://10.42.0.11:5001", ManagerBaseURL: "http://10.42.0.11:5301", WebSocketBaseURL: "http://10.42.0.11:5200",
			}},
			StatePath: statePath, MetricsPath: metricsPath,
		},
	}
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{statePath, metricsPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("validation created %s: %v", path, err)
		}
	}

	for name, configured := range map[string]fileConfig{
		"missing listen address":  {Options: valid.Options},
		"invalid listen address":  {ListenAddr: "127.0.0.1", Options: valid.Options},
		"invalid gateway options": {ListenAddr: valid.ListenAddr, Options: cloudview.Options{RunID: "run-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateConfig(configured); err == nil {
				t.Fatal("validateConfig() accepted invalid configuration")
			}
		})
	}
}

func TestExecuteCommandErrorsDoNotStartServing(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"--help"}, &stdout, &stderr); code != 0 {
			t.Fatalf("execute(--help) = %d, stderr = %q", code, stderr.String())
		}
		for _, command := range []string{"validate", "serve", "doctor", "annotate-report"} {
			if !strings.Contains(stdout.String(), command) {
				t.Fatalf("help omits %q:\n%s", command, stdout.String())
			}
		}
	})

	t.Run("required config", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"validate"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "required flag") {
			t.Fatalf("validate code = %d, stderr = %q", code, stderr.String())
		}
	})

	t.Run("validate forwards strict parsing errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cloudview.json")
		if err := os.WriteFile(path, []byte(`{"listen_addr":"127.0.0.1:19443","unknown":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"validate", "--config", path}, &stdout, &stderr); code != 1 || stderr.Len() == 0 || stdout.Len() != 0 {
			t.Fatalf("validate code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("serve validates before listening", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cloudview.json")
		if err := os.WriteFile(path, []byte(`{"listen_addr":"127.0.0.1:19443"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"serve", "--config", path}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "run_id") {
			t.Fatalf("serve code = %d, stderr = %q", code, stderr.String())
		}
	})

	t.Run("doctor rejects non-http origin before requests", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := execute([]string{"doctor", "--base-url", "https://example.test", "--gate-token", "token"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "HTTP origin") {
			t.Fatalf("doctor code = %d, stderr = %q", code, stderr.String())
		}
	})
}

func TestRunDoctorRejectsInvalidOptionsBeforeRequests(t *testing.T) {
	for _, baseURL := range []string{"", "https://example.test", "http://example.test/path"} {
		_, err := runDoctor(t.Context(), doctorOptions{
			BaseURL: baseURL, Username: "admin", Password: "password", GateToken: "token", ExpectedTargets: 1,
		})
		if err == nil || !strings.Contains(err.Error(), "HTTP origin") {
			t.Fatalf("runDoctor(%q) error = %v", baseURL, err)
		}
	}
	_, err := runDoctor(t.Context(), doctorOptions{BaseURL: "http://example.test", Username: "", Password: "password", GateToken: "token", ExpectedTargets: 1})
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("runDoctor(missing username) error = %v", err)
	}
}

func TestHasWildcardPermissionRequiresResourceAndAction(t *testing.T) {
	permissions := []struct {
		Resource string   `json:"resource"`
		Actions  []string `json:"actions"`
	}{
		{Resource: "nodes", Actions: []string{"*"}},
		{Resource: "*", Actions: []string{"read"}},
	}
	if hasWildcardPermission(permissions) {
		t.Fatal("hasWildcardPermission() accepted partial wildcards")
	}
	permissions = append(permissions, struct {
		Resource string   `json:"resource"`
		Actions  []string `json:"actions"`
	}{Resource: "*", Actions: []string{"read", "*"}})
	if !hasWildcardPermission(permissions) {
		t.Fatal("hasWildcardPermission() rejected wildcard resource and action")
	}
}

func TestDoctorTransportAddsGateToken(t *testing.T) {
	var observed string
	transport := doctorTransport{
		gateToken: "gate-token",
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = request.Header.Get(cloudview.GateProbeHeader)
			return response(http.StatusNoContent, ""), nil
		}),
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Body.Close()
	if observed != "gate-token" {
		t.Fatalf("gate token = %q", observed)
	}
}

func TestDoctorGETContracts(t *testing.T) {
	t.Run("success forwards authorization", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("Authorization"); got != "Bearer token" {
				t.Fatalf("Authorization = %q", got)
			}
			return response(http.StatusOK, "ok"), nil
		})}
		if err := doctorGET(t.Context(), client, "http://example.test/manager/nodes", "Bearer token"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("request construction", func(t *testing.T) {
		if err := doctorGET(t.Context(), http.DefaultClient, "://bad-url", ""); err == nil {
			t.Fatal("doctorGET() accepted an invalid endpoint")
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		transportErr := errors.New("transport failed")
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}
		if err := doctorGET(t.Context(), client, "http://example.test/", ""); !errors.Is(err, transportErr) {
			t.Fatalf("doctorGET() error = %v", err)
		}
	})

	t.Run("body failure", func(t *testing.T) {
		bodyErr := errors.New("body failed")
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingReadCloser{err: bodyErr}}, nil
		})}
		if err := doctorGET(t.Context(), client, "http://example.test/", ""); !errors.Is(err, bodyErr) {
			t.Fatalf("doctorGET() error = %v", err)
		}
	})

	t.Run("non-success status", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusBadGateway, "upstream unavailable"), nil
		})}
		if err := doctorGET(t.Context(), client, "http://example.test/", ""); err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("doctorGET() error = %v", err)
		}
	})
}

func TestDoctorJSONContracts(t *testing.T) {
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/value", nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"value":"ok"}`), nil
		})}
		var destination struct {
			Value string `json:"value"`
		}
		if err := doctorJSON(client, request.Clone(t.Context()), &destination); err != nil || destination.Value != "ok" {
			t.Fatalf("doctorJSON() = %#v, %v", destination, err)
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		transportErr := errors.New("transport failed")
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}
		if err := doctorJSON(client, request.Clone(t.Context()), &struct{}{}); !errors.Is(err, transportErr) {
			t.Fatalf("doctorJSON() error = %v", err)
		}
	})

	t.Run("non-success status", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, "denied"), nil
		})}
		if err := doctorJSON(client, request.Clone(t.Context()), &struct{}{}); err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("doctorJSON() error = %v", err)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "{"), nil
		})}
		if err := doctorJSON(client, request.Clone(t.Context()), &struct{}{}); err == nil {
			t.Fatal("doctorJSON() accepted malformed JSON")
		}
	})
}

func TestAnnotateReportRejectsMalformedInputBeforeStatusRead(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{"},
		{name: "trailing document", body: `{"run_id":"run-1"} {}`},
		{name: "missing run identity", body: `{"status":"pass"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			if err := os.WriteFile(path, []byte(test.body), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := annotateReport(t.Context(), "https://127.0.0.1/cloud-view/status", path); err == nil {
				t.Fatal("annotateReport() accepted malformed report")
			}
		})
	}
	if err := annotateReport(t.Context(), "https://127.0.0.1/cloud-view/status", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("annotateReport(missing) succeeded")
	}
}

func TestReadCloudViewStatusRejectsNonLoopbackEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"%",
		"https://127.0.0.1/cloud-view/status",
		"http://198.51.100.10/cloud-view/status",
		"http://127.0.0.1/wrong",
		"http://127.0.0.1/cloud-view/status?query=1",
	} {
		if _, err := readCloudViewStatus(t.Context(), endpoint); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("readCloudViewStatus(%q) error = %v", endpoint, err)
		}
	}
}

func TestReplaceFileRejectsMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "report.json")
	if err := replaceFile(path, []byte("{}\n"), 0o640); err == nil {
		t.Fatal("replaceFile() succeeded with a missing parent")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type failingReadCloser struct{ err error }

func (reader failingReadCloser) Read([]byte) (int, error) { return 0, reader.err }
func (failingReadCloser) Close() error                    { return nil }
