package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Printf("server failed: %v", err)
		return 1
	}
	return 0
}
