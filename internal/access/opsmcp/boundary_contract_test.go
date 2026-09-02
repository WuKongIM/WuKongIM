package opsmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	runtimeops "github.com/WuKongIM/WuKongIM/internal/runtime/opsmcp"
	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestStableToolErrorsExposeOnlyClosedPublicClassifications(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		code       string
		retryable  bool
		retryAfter int
		invalid    bool
	}{
		{name: "rate", err: runtimeops.ErrRateLimited, code: "mcp_rate_limited", retryable: true, retryAfter: 60},
		{name: "concurrency", err: runtimeops.ErrConcurrencyLimited, code: "mcp_concurrency_limited", retryable: true, retryAfter: 1},
		{name: "invalid input", err: observe.ErrInvalidToolInput, code: "invalid_tool_input", invalid: true},
		{name: "oversize response", err: observe.ErrResponseTooLarge, code: "mcp_response_too_large"},
		{name: "unknown", err: errors.New("provider secret: do not expose"), code: "tool_unavailable", retryable: true, retryAfter: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := stableToolError(tt.err)
			var payload []byte
			if tt.invalid {
				var rpcErr *jsonrpc.Error
				if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
					t.Fatalf("invalid input error = %#v", err)
				}
				payload = rpcErr.Data
			} else {
				payload = []byte(err.Error())
			}
			var envelope toolErrorEnvelope
			if decodeErr := json.Unmarshal(payload, &envelope); decodeErr != nil {
				t.Fatalf("decode public tool error %q: %v", payload, decodeErr)
			}
			if envelope.Code != tt.code || envelope.Retryable != tt.retryable || envelope.RetryAfterSeconds != tt.retryAfter {
				t.Fatalf("public tool error = %+v", envelope)
			}
			if strings.Contains(string(payload), "provider secret") {
				t.Fatalf("private error leaked: %s", payload)
			}
		})
	}
}

func TestVerifierErrorsPreserveStableHTTPStatusAndRetryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		retryable  bool
		retryAfter string
	}{
		{name: "disabled", err: ErrDisabled, status: http.StatusServiceUnavailable, code: "mcp_disabled"},
		{name: "owner", err: ErrOwnerUnavailable, status: http.StatusServiceUnavailable, code: "mcp_owner_unavailable", retryable: true, retryAfter: "1"},
		{name: "state changed", err: runtimeops.ErrStateChanged, status: http.StatusServiceUnavailable, code: "mcp_state_changed", retryable: true, retryAfter: "1"},
		{name: "rate", err: runtimeops.ErrRateLimited, status: http.StatusTooManyRequests, code: "mcp_rate_limited", retryable: true, retryAfter: "60"},
		{name: "concurrency", err: runtimeops.ErrConcurrencyLimited, status: http.StatusTooManyRequests, code: "mcp_concurrency_limited", retryable: true, retryAfter: "1"},
		{name: "unknown", err: errors.New("secret verifier detail"), status: http.StatusUnauthorized, code: "mcp_unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeVerifierError(recorder, tt.err)
			var body httpError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode HTTP error: %v", err)
			}
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable || recorder.Header().Get("Retry-After") != tt.retryAfter {
				t.Fatalf("status/header/body = %d/%q/%+v", recorder.Code, recorder.Header().Get("Retry-After"), body)
			}
			if strings.Contains(recorder.Body.String(), "secret verifier") {
				t.Fatalf("verifier detail leaked: %s", recorder.Body.String())
			}
		})
	}
}

func TestBoundedRequestAndForwardHeadersRejectOversizeAndControls(t *testing.T) {
	if payload, err := readBoundedRequest(nil); err != nil || len(payload) != 0 {
		t.Fatalf("nil body = %d bytes, %v", len(payload), err)
	}
	exact := bytes.Repeat([]byte{'x'}, MaxRequestBytes)
	if payload, err := readBoundedRequest(io.NopCloser(bytes.NewReader(exact))); err != nil || !bytes.Equal(payload, exact) {
		t.Fatalf("exact body = %d bytes, %v", len(payload), err)
	}
	if _, err := readBoundedRequest(io.NopCloser(bytes.NewReader(append(exact, 'x')))); err == nil {
		t.Fatal("oversize request body unexpectedly accepted")
	}
	if _, err := readBoundedRequest(errorReadCloser{}); err == nil {
		t.Fatal("request read error unexpectedly discarded")
	}

	if !validForwardHeaders(nil) || !validForwardHeaderValues("application/json", "application/json", "2025-06-18") {
		t.Fatal("canonical forward headers rejected")
	}
	for name, valid := range map[string]bool{
		"oversize content type": validForwardHeaderValue(strings.Repeat("a", maxForwardContentTypeBytes+1), maxForwardContentTypeBytes),
		"newline":               validForwardHeaderValue("application/json\nsecret", maxForwardContentTypeBytes),
		"delete":                validForwardHeaderValue("value\x7f", maxForwardContentTypeBytes),
	} {
		if valid {
			t.Fatalf("%s forward header unexpectedly accepted", name)
		}
	}
}

func TestExecuteForwardRejectsInvalidEnvelopeAndMissingOwnerVerifier(t *testing.T) {
	valid := opscontract.ForwardRequest{
		Version: opscontract.RPCVersion, RequestID: "request-1", IngressNodeID: 1,
		CredentialID: "test", DigestSHA256: "digest", ExpectedRevision: 8,
		ContentType: "application/json", Accept: "application/json", Payload: []byte(`{}`),
	}
	if _, err := (*Endpoint)(nil).ExecuteForward(context.Background(), valid); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("nil endpoint error = %v", err)
	}
	endpoint, err := NewEndpoint(Config{Verifier: verifierStub{token: "token"}, Service: mustObservationService(t), LocalNodeID: 2})
	if err != nil {
		t.Fatalf("new endpoint: %v", err)
	}
	if _, err := endpoint.ExecuteForward(context.Background(), valid); !errors.Is(err, ErrOwnerUnavailable) {
		t.Fatalf("missing forward verifier error = %v", err)
	}
	invalid := valid
	invalid.RequestID = strings.Repeat("r", 65)
	if _, err := endpoint.ExecuteForward(context.Background(), invalid); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("oversize request ID error = %v", err)
	}
	invalid = valid
	invalid.ProtocolVersion = "version\nsecret"
	if _, err := endpoint.ExecuteForward(context.Background(), invalid); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("control header error = %v", err)
	}
}

func TestForwardResponseAndBufferedFlushPreserveBoundedOwnerOutput(t *testing.T) {
	for name, response := range map[string]opscontract.ForwardResponse{
		"version": {Version: 0, StatusCode: http.StatusOK},
		"status":  {Version: opscontract.RPCVersion, StatusCode: 99},
		"payload": {Version: opscontract.RPCVersion, StatusCode: http.StatusOK, Payload: bytes.Repeat([]byte{'x'}, observe.MaxResponseBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeForwardResponse(recorder, response)
			if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "mcp_owner_unavailable") {
				t.Fatalf("invalid owner response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}

	recorder := httptest.NewRecorder()
	writeForwardResponse(recorder, opscontract.ForwardResponse{
		Version: opscontract.RPCVersion, StatusCode: http.StatusAccepted, Payload: []byte("accepted"),
	})
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Content-Type") != "application/json" || recorder.Body.String() != "accepted" {
		t.Fatalf("valid owner response = %d %#v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	buffered := newBufferedResponse()
	buffered.Header().Add("X-Test", "one")
	buffered.Header().Add("X-Test", "two")
	buffered.WriteHeader(http.StatusCreated)
	buffered.WriteHeader(http.StatusInternalServerError)
	if _, err := buffered.Write([]byte("body")); err != nil {
		t.Fatalf("buffer response: %v", err)
	}
	flushed := httptest.NewRecorder()
	buffered.flushTo(flushed)
	if flushed.Code != http.StatusCreated || strings.Join(flushed.Header().Values("X-Test"), ",") != "one,two" || flushed.Body.String() != "body" {
		t.Fatalf("flushed response = %d %#v %q", flushed.Code, flushed.Header(), flushed.Body.String())
	}
}

type errorReadCloser struct{}

func (errorReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func (errorReadCloser) Close() error { return nil }
