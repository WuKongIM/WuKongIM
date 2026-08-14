package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

type fakeTerminalFenceBenchController struct {
	request benchterminal.PrepareRequest
	grant   benchterminal.Grant
	err     error
	calls   int
}

func (f *fakeTerminalFenceBenchController) Prepare(_ context.Context, request benchterminal.PrepareRequest) (benchterminal.Grant, error) {
	f.calls++
	f.request = request
	if f.err != nil {
		return benchterminal.Grant{}, f.err
	}
	return f.grant, nil
}

func TestBenchTerminalFencePrepareRequiresEnabledAuthenticatedBenchAPI(t *testing.T) {
	controller := &fakeTerminalFenceBenchController{}
	disabled := httptest.NewServer(New(Options{BenchTerminalFence: controller}).Handler())
	t.Cleanup(disabled.Close)
	postTerminalFence(t, disabled.URL, "", validTerminalFencePrepareJSON(), http.StatusNotFound)
	unauthenticatedConfig := httptest.NewServer(New(Options{BenchEnabled: true, BenchTerminalFence: controller}).Handler())
	t.Cleanup(unauthenticatedConfig.Close)
	postTerminalFence(t, unauthenticatedConfig.URL, "", validTerminalFencePrepareJSON(), http.StatusNotFound)

	enabled := httptest.NewServer(New(Options{
		BenchEnabled:       true,
		BenchToken:         "terminal-api-token",
		BenchTerminalFence: controller,
	}).Handler())
	t.Cleanup(enabled.Close)

	postTerminalFence(t, enabled.URL, "", validTerminalFencePrepareJSON(), http.StatusUnauthorized)
	postTerminalFence(t, enabled.URL, "wrong-token", validTerminalFencePrepareJSON(), http.StatusUnauthorized)
	if controller.calls != 0 {
		t.Fatalf("Prepare() calls before authorization = %d, want 0", controller.calls)
	}

	controller.grant = benchterminal.Grant{Epoch: 7, Capability: "bounded-terminal-capability"}
	resp := postTerminalFence(t, enabled.URL, "terminal-api-token", validTerminalFencePrepareJSON(), http.StatusOK)
	defer resp.Body.Close()
	var grant model.TerminalFenceGrant
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if got, want := grant.Version, model.TerminalFenceVersion; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	if grant.RunID != "run-a" || grant.AssignmentID != "generation-a" || grant.ExpectedSessions != 2500 || grant.Epoch != 7 || grant.Capability != "bounded-terminal-capability" {
		t.Fatalf("grant = %#v, want exact request identity and controller grant", grant)
	}
	if controller.request != (benchterminal.PrepareRequest{RunID: "run-a", AssignmentID: "generation-a", ExpectedSessions: 2500}) {
		t.Fatalf("Prepare() request = %#v, want exact decoded identity", controller.request)
	}
}

func TestBenchTerminalFencePrepareUsesStrictBoundedJSON(t *testing.T) {
	controller := &fakeTerminalFenceBenchController{grant: benchterminal.Grant{Epoch: 7, Capability: "bounded-terminal-capability"}}
	server := httptest.NewServer(New(Options{BenchEnabled: true, BenchToken: "terminal-api-token", BenchTerminalFence: controller}).Handler())
	t.Cleanup(server.Close)

	for _, body := range []string{
		`{"run_id":"run-a","assignment_id":"generation-a","expected_sessions":2500,"unknown":true}`,
		validTerminalFencePrepareJSON() + ` {}`,
		`{"run_id":"run-a","assignment_id":"generation-a","expected_sessions":0}`,
		`{"run_id":"","assignment_id":"generation-a","expected_sessions":2500}`,
	} {
		postTerminalFence(t, server.URL, "terminal-api-token", body, http.StatusBadRequest)
	}
	large := `{"run_id":"` + strings.Repeat("x", int(terminalFenceMaxRequestBytes)) + `","assignment_id":"generation-a","expected_sessions":2500}`
	postTerminalFence(t, server.URL, "terminal-api-token", large, http.StatusRequestEntityTooLarge)
	if controller.calls != 0 {
		t.Fatalf("Prepare() calls for invalid requests = %d, want 0", controller.calls)
	}
}

func TestBenchTerminalFencePrepareFailureIsLowCardinalityAndRedacted(t *testing.T) {
	const secret = "capability-must-never-appear"
	logger := newRecordingAPILogger("internal.access.api")
	controller := &fakeTerminalFenceBenchController{err: fmt.Errorf("adapter exploded with %s", secret)}
	server := httptest.NewServer(New(Options{
		BenchEnabled:       true,
		BenchToken:         "terminal-api-token",
		BenchTerminalFence: controller,
		Logger:             logger,
	}).Handler())
	t.Cleanup(server.Close)

	resp := postTerminalFence(t, server.URL, "terminal-api-token", validTerminalFencePrepareJSON(), http.StatusInternalServerError)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) || !strings.Contains(string(body), "terminal fence unavailable") {
		t.Fatalf("response = %s, want redacted stable error", body)
	}
	entry := requireAPILogEntry(t, logger, "ERROR", "internal.access.api.http", "internal.access.api.bench_terminal_fence_failed")
	for _, field := range entry.fields {
		if strings.Contains(fmt.Sprint(field.Value), secret) || field.Key == "error" {
			t.Fatalf("log field %#v exposed raw terminal error", field)
		}
	}
}

func TestBenchCapabilitiesAdvertiseTerminalFenceOnlyWithController(t *testing.T) {
	for _, test := range []struct {
		name       string
		controller TerminalFenceBenchController
		token      string
		want       bool
	}{
		{name: "absent", want: false},
		{name: "controller without token", controller: &fakeTerminalFenceBenchController{}, want: false},
		{name: "authenticated controller", controller: &fakeTerminalFenceBenchController{}, token: "terminal-api-token", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(New(Options{BenchEnabled: true, BenchToken: test.token, BenchTerminalFence: test.controller}).Handler())
			defer srv.Close()
			var caps capabilitiesResponse
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/bench/v1/capabilities", nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			resp, err := http.DefaultClient.Do(req)
			decodeJSON(t, resp, err, &caps)
			if caps.Supports.TerminalFencePrepare != test.want {
				t.Fatalf("terminal_fence_prepare = %v, want %v", caps.Supports.TerminalFencePrepare, test.want)
			}
		})
	}
}

func TestBenchTerminalFencePrepareMapsClosedControllerErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{name: "invalid", err: benchterminal.ErrInvalidPrepareRequest, status: http.StatusBadRequest, msg: "invalid terminal fence request"},
		{name: "conflict", err: benchterminal.ErrPreparationConflict, status: http.StatusConflict, msg: "terminal fence identity conflict"},
		{name: "failed", err: benchterminal.ErrPreparationFailed, status: http.StatusServiceUnavailable, msg: "terminal fence preparation failed"},
		{name: "deadline", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, msg: "terminal fence preparation timed out"},
		{name: "canceled", err: context.Canceled, status: http.StatusRequestTimeout, msg: "terminal fence request canceled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &fakeTerminalFenceBenchController{err: test.err}
			server := httptest.NewServer(New(Options{BenchEnabled: true, BenchToken: "terminal-api-token", BenchTerminalFence: controller}).Handler())
			defer server.Close()
			resp := postTerminalFence(t, server.URL, "terminal-api-token", validTerminalFencePrepareJSON(), test.status)
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), test.msg) {
				t.Fatalf("body = %s, want %q", body, test.msg)
			}
		})
	}
}

func validTerminalFencePrepareJSON() string {
	return `{"run_id":"run-a","assignment_id":"generation-a","expected_sessions":2500}`
}

func postTerminalFence(t *testing.T, baseURL, token, body string, wantStatus int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/bench/v1/terminal-fence/prepare", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, wantStatus, data)
	}
	return resp
}
