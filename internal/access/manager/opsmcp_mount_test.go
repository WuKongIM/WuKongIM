package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerMountsOpsMCPWithoutOpenCORS(t *testing.T) {
	called := false
	server := New(Options{OpsMCPHandler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})})
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Origin", "https://example.test")
	response := httptest.NewRecorder()
	server.Engine().ServeHTTP(response, request)

	if !called || response.Code != http.StatusAccepted {
		t.Fatalf("called=%v status=%d", called, response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("/mcp inherited manager CORS headers: %#v", response.Header())
	}
}
