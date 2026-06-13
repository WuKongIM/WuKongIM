package top

import (
	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

// NewCommand builds the top subcommand.
func NewCommand(deps command.Deps) *cobra.Command {
	return command.NewPlaceholder("top", "Inspect live WuKongIM runtime pressure")
}
