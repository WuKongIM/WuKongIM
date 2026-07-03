package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/app"
)

const defaultStopTimeout = 5 * time.Second

// runtimeApp is the lifecycle surface required by the command entrypoint.
type runtimeApp interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// appFactory creates the runtime app from a loaded config.
type appFactory func(app.Config) (runtimeApp, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], newInternalV2App); err != nil {
		log.Fatal(err)
	}
}

// run loads config, starts the app, waits for cancellation, and stops it.
func run(ctx context.Context, args []string, newApp appFactory) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	application, err := newApp(cfg)
	if err != nil {
		return err
	}
	if err := application.Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout(cfg))
	defer cancel()
	return application.Stop(stopCtx)
}

// newInternalV2App builds the internalv2 composition root.
func newInternalV2App(cfg app.Config) (runtimeApp, error) {
	return app.New(cfg)
}

// stopTimeout returns the configured stop budget or the entrypoint default.
func stopTimeout(cfg app.Config) time.Duration {
	if cfg.Cluster.Timeouts.Stop > 0 {
		return cfg.Cluster.Timeouts.Stop
	}
	return defaultStopTimeout
}
