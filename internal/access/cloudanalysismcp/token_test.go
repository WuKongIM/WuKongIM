package cloudanalysismcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenExchangeHandlerVerifiesIdentityAndReturnsBoundedSession(t *testing.T) {
	expiresAt := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	handler, err := NewTokenExchangeHandler(TokenExchangeConfig{
		Verify: func(_ context.Context, raw string) error {
			if raw != "oidc-token" {
				return errors.New("unexpected identity")
			}
			return nil
		},
		Issue: func() (string, time.Time, error) { return "analysis-token", expiresAt, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/analysis/token", nil)
	request.Header.Set("Authorization", "Bearer oidc-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"access_token":"analysis-token"`) || !strings.Contains(response.Body.String(), expiresAt.Format(time.RFC3339)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestTokenExchangeHandlerFailsClosed(t *testing.T) {
	handler, err := NewTokenExchangeHandler(TokenExchangeConfig{
		Verify: func(context.Context, string) error { return errors.New("invalid") },
		Issue:  func() (string, time.Time, error) { return "", time.Time{}, errors.New("unavailable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		method string
		auth   string
		status int
	}{
		{name: "method", method: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "missing identity", method: http.MethodPost, status: http.StatusUnauthorized},
		{name: "invalid identity", method: http.MethodPost, auth: "Bearer bad", status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/analysis/token", nil)
			request.Header.Set("Authorization", test.auth)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}
