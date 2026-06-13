package main

import (
	"io"

	"github.com/spf13/cobra"
)

type metricsClassifyConfig struct {
	beforePath string
	afterPath  string
}

func newMetricsCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Analyze benchmark metrics snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return commandExit{code: exitInternal, message: err.Error()}
			}
			return commandExit{code: exitConfig}
		},
	}
	cmd.AddCommand(newMetricsClassifyCommand(stderr))
	return cmd
}

func newMetricsClassifyCommand(stderr io.Writer) *cobra.Command {
	var cfg metricsClassifyConfig
	cmd := &cobra.Command{
		Use:   "classify",
		Short: "Classify before/after Prometheus snapshot deltas",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMetricsClassifyConfig(cfg); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runMetricsClassifyConfig(cfg, stderr))
		},
	}
	bindMetricsClassifyFlags(cmd, &cfg)
	return cmd
}

func bindMetricsClassifyFlags(cmd *cobra.Command, cfg *metricsClassifyConfig) {
	flags := cmd.Flags()
	flags.StringVar(&cfg.beforePath, "before", "", "Prometheus text snapshot before the measured window")
	flags.StringVar(&cfg.afterPath, "after", "", "Prometheus text snapshot after the measured window")
}
