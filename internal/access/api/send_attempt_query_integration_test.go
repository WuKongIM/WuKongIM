//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

// attemptSelectorBaselineReader deliberately supplies only the existing generic
// diagnostics capability. It must not allocate an attempt summary ring.
type attemptSelectorBaselineReader struct{}

func (attemptSelectorBaselineReader) QueryDiagnostics(context.Context, diagnostics.Query) diagnostics.QueryResult {
	return diagnostics.QueryResult{Status: diagnostics.StatusNotFound}
}

// This compiled baseline documents a collection gap, not a product-latency
// root cause: a caller cannot select an anonymous original attempt through an
// absent route. An enabled debug API must report unsupported explicitly when
// this optional capability is not composed; the general diagnostics opt-in
// alone must not allocate or activate attempt-link state.
func TestDiagnosticsSendAttemptSelectorReportsUnsupportedWithoutCapability(t *testing.T) {
	const token = "attempt-selector-test-token"
	server := httptest.NewServer(New(Options{
		DebugAPIEnabled: true,
		BenchToken:      token,
		Diagnostics:     attemptSelectorBaselineReader{},
	}).Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	request, err := http.NewRequest(http.MethodPost, server.URL+"/debug/diagnostics/send-attempts/query", strings.NewReader(`{"version":1,"keys":[{"message_fingerprint":"00112233445566778899aabbccddeeff","client_seq":1,"connect_packet_fingerprint":"ffeeddccbbaa99887766554433221100"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("anonymous original-attempt selector is unavailable: status=%d body=%q; want explicit versioned unsupported JSON; this is a collection gap, not proof of the R26 product root cause", response.StatusCode, body)
	}
	if len(body) > 64<<10 {
		t.Fatalf("attempt selector response exceeds the 64KiB diagnostic cap: %d", len(body))
	}
	var got struct {
		Version uint64            `json:"version"`
		Status  string            `json:"status"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("selector must return bounded JSON: %v", err)
	}
	if got.Version != 1 || got.Status != "unsupported" || len(got.Results) != 0 {
		t.Fatalf("uncomposed selector = version:%d status:%q results:%d; want 1/unsupported/0", got.Version, got.Status, len(got.Results))
	}
}
