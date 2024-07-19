package cmd

import (
	"github.com/spf13/cobra"
)

type WuKongIMContext struct {
}

type CMD interface {
	CMD() *cobra.Command
}
