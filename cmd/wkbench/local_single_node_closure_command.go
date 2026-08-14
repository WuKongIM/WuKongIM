package main

import (
	"github.com/spf13/cobra"
)

func newLocalSingleNodeStepClosureCommand() *cobra.Command {
	var rootPath, closurePath, outputPath string
	cmd := &cobra.Command{
		Use:   "local-single-node-step-closure",
		Short: "Verify and consume one sealed single-node cluster step closure",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := openLocalSingleNodeArtifactRoot(rootPath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node closure root failed"}
			}
			closureRelative, err := root.relative(closurePath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node closure path failed"}
			}
			outputRelative, err := root.relative(outputPath)
			if err != nil || outputRelative == closureRelative {
				return commandExit{code: exitConfig, message: "local single-node closure decision output failed"}
			}
			closure, err := verifyLocalSingleNodeStepClosure(root, closureRelative)
			if err != nil {
				return commandExit{code: exitInternal, message: "local single-node closure verification failed: " + err.Error()}
			}
			if err := root.writeJSONExclusive(outputRelative, closure.Result); err != nil {
				return commandExit{code: exitInternal, message: "local single-node closure decision write failed: " + err.Error()}
			}
			return exitCodeError(localSingleNodeStepExitCode(closure.Result))
		},
	}
	cmd.Flags().StringVar(&rootPath, "root", "", "single-node cluster artifact root")
	cmd.Flags().StringVar(&closurePath, "closure", "", "step closure manifest")
	cmd.Flags().StringVar(&outputPath, "output", "", "verified closure decision JSON")
	for _, name := range []string{"root", "closure", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}
