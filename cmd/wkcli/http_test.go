package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckResponse_Success(t *testing.T) {
	err := checkResponse([]byte(`{"status":"ok"}`), 200)
	assert.NoError(t, err)
}

func TestCheckResponse_Success204(t *testing.T) {
	err := checkResponse([]byte(""), 204)
	assert.NoError(t, err)
}

func TestCheckResponse_ErrorWithMessage(t *testing.T) {
	body := `{"code":1001,"msg":"channel not found"}`
	err := checkResponse([]byte(body), 400)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "channel not found")
	assert.Contains(t, err.Error(), "1001")
}

func TestCheckResponse_ErrorWithoutMessage(t *testing.T) {
	body := `some raw error`
	err := checkResponse([]byte(body), 500)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "some raw error")
}

func TestCheckResponse_ErrorInvalidJSON(t *testing.T) {
	body := `{invalid json`
	err := checkResponse([]byte(body), 400)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestDoRequest_NoServerConfigured(t *testing.T) {
	oldServer := flagServer
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		cfg = oldCfg
	}()

	flagServer = ""
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	_, _, _, err := doRequest("GET", "/health", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server not configured")
}

func TestDoGet_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	body, statusCode, elapsed, err := doGet("/health")
	assert.NoError(t, err)
	assert.Equal(t, 200, statusCode)
	assert.Greater(t, elapsed.Nanoseconds(), int64(0))
	assert.Contains(t, string(body), "ok")
}

func TestDoPost_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/user/token", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "test1", req["uid"])

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	reqBody := map[string]interface{}{"uid": "test1", "token": "abc"}
	body, statusCode, _, err := doPost("/user/token", reqBody)
	assert.NoError(t, err)
	assert.Equal(t, 200, statusCode)
	assert.Contains(t, string(body), "ok")
}

func TestDoRequest_TokenHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "mytoken123", r.Header.Get("token"))
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	oldToken := flagToken
	defer func() {
		flagServer = oldServer
		flagToken = oldToken
	}()
	flagServer = server.URL
	flagToken = "mytoken123"

	_, statusCode, _, err := doGet("/health")
	assert.NoError(t, err)
	assert.Equal(t, 200, statusCode)
}

func TestDoRequest_NoTokenHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "", r.Header.Get("token"))
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	oldToken := flagToken
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		flagToken = oldToken
		cfg = oldCfg
	}()
	flagServer = server.URL
	flagToken = ""
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	_, _, _, err := doGet("/health")
	assert.NoError(t, err)
}

func TestDoPost_NilBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		// With nil body, Content-Type should not be set.
		assert.Equal(t, "", r.Header.Get("Content-Type"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	_, statusCode, _, err := doPost("/test", nil)
	assert.NoError(t, err)
	assert.Equal(t, 200, statusCode)
}

func TestDoRequest_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"code":500,"msg":"internal error"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	body, statusCode, _, err := doGet("/health")
	assert.NoError(t, err)
	assert.Equal(t, 500, statusCode)

	err = checkResponse(body, statusCode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

func TestDoRequest_URLConstruction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/test", r.URL.Path)
		assert.Equal(t, "key=value", r.URL.RawQuery)
		w.WriteHeader(200)
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	// Trailing slash should be trimmed.
	flagServer = server.URL + "/"

	_, _, _, err := doGet("/api/test?key=value")
	assert.NoError(t, err)
}
