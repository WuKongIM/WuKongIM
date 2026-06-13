package command

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const (
	ExitOK = iota
	ExitConfig
	ExitUnavailable
	ExitInternal
)

// Deps carries process dependencies that every subcommand can reuse.
type Deps struct {
	// Stdout receives normal command output and help text.
	Stdout io.Writer
	// Stderr receives diagnostics and command errors.
	Stderr io.Writer
	// ContextDir points to the directory used for persistent wkcli contexts.
	ContextDir *string
}

// Factory creates a Cobra subcommand using shared root dependencies.
type Factory func(Deps) *cobra.Command

// Exit carries an intentional process exit code and optional diagnostic.
type Exit struct {
	// Code is the process exit status returned to main.
	Code int
	// Message is an optional diagnostic printed by the root executor.
	Message string
}

func (e Exit) Error() string {
	return e.Message
}

// NewPlaceholder returns a visible command reserved for future implementation.
func NewPlaceholder(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Exit{
				Code:    ExitUnavailable,
				Message: fmt.Sprintf("wkcli %s is not implemented yet", use),
			}
		},
	}
}
