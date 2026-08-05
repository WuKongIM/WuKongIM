package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	workerModeDefault       = "default"
	workerModeChatLifecycle = "chat-lifecycle"
)

func newWorkerCommand(stderr io.Writer) *cobra.Command {
	cfg := defaultWorkerCLIConfig()
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start a wkbench worker control process",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeWorkerConfig(&cfg); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runWorkerConfig(cfg, stderr))
		},
	}
	bindWorkerFlags(cmd.Flags(), &cfg)
	return cmd
}

func defaultWorkerCLIConfig() workerCLIConfig {
	return workerCLIConfig{listen: "127.0.0.1:19090", mode: workerModeDefault}
}

func bindWorkerFlags(flags *pflag.FlagSet, cfg *workerCLIConfig) {
	flags.StringVar(&cfg.mode, "mode", cfg.mode, fmt.Sprintf("worker runtime mode (%s or %s)", workerModeDefault, workerModeChatLifecycle))
	flags.StringVar(&cfg.listen, "listen", cfg.listen, "worker control listen address")
	flags.StringVar(&cfg.server.WorkDir, "work-dir", "", "directory for worker control state")
	flags.StringVar(&cfg.server.ControlToken, "control-token", os.Getenv("WK_BENCH_WORKER_TOKEN"), "bearer token for worker control API")
	flags.BoolVar(&cfg.server.InsecureControl, "insecure-control", false, "allow unauthenticated worker control API")
}
