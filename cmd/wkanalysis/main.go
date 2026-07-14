package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudanalysismcp"
	"github.com/WuKongIM/WuKongIM/internal/app"
)

func main() {
	os.Exit(run(os.Getenv, os.Stderr))
}

func run(getenv func(string) string, stderr *os.File) int {
	cfg, err := loadServeConfig(getenv, time.Now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var oidcVerifier *githubOIDCVerifier
	var sessions *analysisSessionStore
	if cfg.oidc != nil {
		oidcVerifier = newGitHubOIDCVerifier(*cfg.oidc, nil, time.Now)
		sessions = newAnalysisSessionStore(cfg.gateway.RunExpiresAt, time.Now)
		cfg.gateway.MCPTokenVerifier = sessions.Verify
	}
	gateway, err := app.NewCloudAnalysisGatewayHandler(cfg.gateway)
	if err != nil {
		fmt.Fprintf(stderr, "build analysis gateway: %v\n", err)
		return 1
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", gateway)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "run_id": cfg.gateway.RunID, "run_state": cfg.gateway.RunState,
			"run_expires_at": cfg.gateway.RunExpiresAt,
		})
	})
	mux.HandleFunc("GET /self-check", func(w http.ResponseWriter, request *http.Request) {
		failures := selfCheck(request.Context(), cfg)
		w.Header().Set("Content-Type", "application/json")
		if len(failures) != 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": len(failures) == 0, "failures": failures})
	})
	if oidcVerifier != nil {
		tokenHandler, tokenErr := cloudanalysismcp.NewTokenExchangeHandler(cloudanalysismcp.TokenExchangeConfig{
			Verify: oidcVerifier.Verify,
			Issue:  sessions.Issue,
		})
		if tokenErr != nil {
			fmt.Fprintf(stderr, "build analysis token endpoint: %v\n", tokenErr)
			return 1
		}
		mux.Handle("POST /analysis/token", tokenHandler)
	}
	server := &http.Server{
		Addr: cfg.listenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 65 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger := log.New(stderr, "wkanalysis: ", log.LstdFlags|log.LUTC)
	logger.Printf("serving run %s on %s", cfg.gateway.RunID, cfg.listenAddr)
	serve := server.ListenAndServe
	if cfg.tlsCert != "" {
		serve = func() error { return server.ListenAndServeTLS(cfg.tlsCert, cfg.tlsKey) }
	}
	if err := serve(); err != nil && err != http.ErrServerClosed {
		logger.Printf("server failed: %v", err)
		return 1
	}
	return 0
}

func selfCheck(ctx context.Context, cfg serveConfig) []string {
	client := &http.Client{Timeout: 5 * time.Second}
	checks := []struct {
		name   string
		method string
		url    string
		body   []byte
	}{
		{name: "prometheus", method: http.MethodGet, url: cfg.gateway.PrometheusBaseURL + "/-/ready"},
	}
	for nodeID, baseURL := range cfg.gateway.NodeAPIBaseURLs {
		checks = append(checks, struct {
			name   string
			method string
			url    string
			body   []byte
		}{name: fmt.Sprintf("node-%d", nodeID), method: http.MethodGet, url: baseURL + "/readyz"})
	}
	if cfg.gateway.ManagerAuth.Username != "" {
		payload, _ := json.Marshal(map[string]string{"username": cfg.gateway.ManagerAuth.Username, "password": cfg.gateway.ManagerAuth.Password})
		checks = append(checks, struct {
			name   string
			method string
			url    string
			body   []byte
		}{name: "manager", method: http.MethodPost, url: cfg.gateway.ManagerBaseURL + "/manager/login", body: payload})
	}
	failures := make([]string, 0)
	for _, check := range checks {
		request, err := http.NewRequestWithContext(ctx, check.method, check.url, bytes.NewReader(check.body))
		if err != nil {
			failures = append(failures, check.name)
			continue
		}
		if len(check.body) != 0 {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			failures = append(failures, check.name)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failures = append(failures, check.name)
		}
	}
	return failures
}
