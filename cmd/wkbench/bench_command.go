package main

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newRunCommand(stderr io.Writer) *cobra.Command {
	var cfg runBenchConfig
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the full benchmark coordinator flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRunBenchConfig(cfg); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runBenchConfigCommand(cfg, stderr))
		},
	}
	bindBenchConfigPathFlags(cmd.Flags(), &cfg.paths)
	cmd.Flags().DurationVar(&cfg.phasePollTimeout, "phase-poll-timeout", 0, "base worker phase poll timeout; connect/warmup/run/cooldown add their configured schedule duration; 0 uses the wkbench default")
	return cmd
}

func newValidateCommand(stderr io.Writer) *cobra.Command {
	var paths benchConfigPaths
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate target, worker, and scenario YAML without network checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBenchConfigPaths(paths, true); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runValidateConfig(paths, stderr))
		},
	}
	bindBenchConfigPathFlags(cmd.Flags(), &paths)
	return cmd
}

func newDoctorCommand(stderr io.Writer) *cobra.Command {
	var paths benchConfigPaths
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run config and network preflight checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBenchConfigPaths(paths, false); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runDoctorConfig(paths, stderr))
		},
	}
	bindBenchConfigPathFlags(cmd.Flags(), &paths)
	return cmd
}

func bindBenchConfigPathFlags(flags *pflag.FlagSet, paths *benchConfigPaths) {
	flags.StringVar(&paths.target, "target", "", "target YAML file")
	flags.StringVar(&paths.scenario, "scenario", "", "scenario YAML file")
	flags.StringVar(&paths.workers, "workers", "", "workers YAML file")
}
