package cloudanalysismcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errInvalidTokenExchange = errors.New("internal/access/cloudanalysismcp: invalid token exchange")

// TokenExchangeConfig adapts GitHub OIDC verification and run-scoped session
// issuance without moving either security implementation into the HTTP layer.
type TokenExchangeConfig struct {
	// Verify validates one raw GitHub OIDC bearer token and its exact claims.
	Verify func(context.Context, string) error
	// Issue creates one non-renewable, run-lease-bounded Analysis Token.
	Issue func() (string, time.Time, error)
}

// NewTokenExchangeHandler creates the Analysis Token HTTP protocol adapter.
func NewTokenExchangeHandler(config TokenExchangeConfig) (http.Handler, error) {
	if config.Verify == nil || config.Issue == nil {
		return nil, errInvalidTokenExchange
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		raw := authorizationBearer(request.Header.Get("Authorization"))
		if raw == "" || config.Verify(request.Context(), raw) != nil {
			http.Error(w, "invalid GitHub OIDC identity", http.StatusUnauthorized)
			return
		}
		token, expiresAt, err := config.Issue()
		if err != nil {
			http.Error(w, "analysis session unavailable", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			AccessToken string    `json:"access_token"`
			TokenType   string    `json:"token_type"`
			ExpiresAt   time.Time `json:"expires_at"`
		}{AccessToken: token, TokenType: "Bearer", ExpiresAt: expiresAt})
	}), nil
}

func authorizationBearer(header string) string {
	scheme, raw, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(raw)
}
