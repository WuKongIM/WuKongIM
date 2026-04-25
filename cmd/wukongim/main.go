package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	configPath := registerConfigFlag(flag.CommandLine)
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	application, err := app.New(cfg)
	if err != nil {
		return err
	}

	if err := application.Start(); err != nil {
		return err
	}
	defer func() {
		if err := application.Stop(); err != nil {
			log.Printf("stop app: %v", err)
		}
	}()

	waitForSignal()
	return nil
}

func registerConfigFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "path to wukongim.conf file")
}

func waitForSignal() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
