package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
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
	if code := runMain(); code != 0 {
		os.Exit(code)
	}
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := execute(ctx, os.Args[1:], commandIO{
		stdin:          os.Stdin,
		stdout:         os.Stdout,
		stderr:         os.Stderr,
		stdoutTerminal: isTerminal(os.Stdout),
	}, newInternalApp)
	if err == nil {
		return 0
	}
	fmt.Fprintf(os.Stderr, "wukongim: %v\n", err)
	return commandExitCode(err)
}

// run loads config, starts the app, waits for cancellation, and stops it.
func run(ctx context.Context, args []string, newApp appFactory) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return newCommandError(exitConfig, err)
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

// newInternalApp builds the internal composition root.
func newInternalApp(cfg app.Config) (runtimeApp, error) {
	return app.New(cfg)
}

// stopTimeout returns the configured stop budget or the entrypoint default.
func stopTimeout(cfg app.Config) time.Duration {
	if cfg.Cluster.Timeouts.Stop > 0 {
		return cfg.Cluster.Timeouts.Stop
	}
	return defaultStopTimeout
}
