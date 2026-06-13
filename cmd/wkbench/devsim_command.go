package main

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newDevSimCommand(stderr io.Writer) *cobra.Command {
	var cfg devSimCLIConfig
	cmd := &cobra.Command{
		Use:   "dev-sim",
		Short: "Run the long-lived development simulator",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeDevSimConfig(cfg); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runDevSimConfig(cfg, stderr))
		},
	}
	bindDevSimFlags(cmd.Flags(), &cfg)
	return cmd
}

func bindDevSimFlags(flags *pflag.FlagSet, cfg *devSimCLIConfig) {
	flags.StringVar(&cfg.configPath, "config", "", "dev-sim YAML file")
	flags.StringVar(&cfg.statusListen, "status-listen", "", "override status.listen")
}
