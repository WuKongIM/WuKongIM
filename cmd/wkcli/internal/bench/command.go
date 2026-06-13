package bench

import (
	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

// NewCommand builds the bench subcommand.
func NewCommand(deps command.Deps) *cobra.Command {
	return command.NewPlaceholder("bench", "Run WuKongIM benchmark helpers")
}
