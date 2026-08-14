package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newLocalSingleNodePublishCommand validates a complete marker draft against
// sealed artifacts before atomically creating the one public marker. The same
// path is used for terminal preflight denials and measured decisions.
func newLocalSingleNodePublishCommand() *cobra.Command {
	var rootPath, draftPath, outputPath string
	cmd := &cobra.Command{
		Use:   "local-single-node-publish",
		Short: "Atomically publish one verified single-node cluster decision marker",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := openLocalSingleNodeArtifactRoot(rootPath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node publication root failed"}
			}
			draftRelative, err := root.relative(draftPath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node marker draft path failed"}
			}
			outputRelative, err := root.relative(outputPath)
			if err != nil || outputRelative != "local-baseline.json" || draftRelative == outputRelative {
				return commandExit{code: exitConfig, message: "local single-node marker output path failed"}
			}
			data, err := root.read(draftRelative, 1<<20)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node marker draft read failed"}
			}
			result, err := verifyLocalSingleNodeCompletionData(root, data)
			if err != nil {
				return commandExit{code: exitInternal, message: "local single-node marker draft verification failed: " + err.Error()}
			}
			if err := root.writeExclusive(outputRelative, data); err != nil {
				return commandExit{code: exitInternal, message: "local single-node marker publication failed: " + err.Error()}
			}
			published, err := root.read(outputRelative, 1<<20)
			if err != nil || string(published) != string(data) {
				return commandExit{code: exitInternal, message: "local single-node published marker readback failed"}
			}
			if _, err := verifyLocalSingleNodeCompletionData(root, published); err != nil {
				return commandExit{code: exitInternal, message: fmt.Sprintf("local single-node published marker verification failed: %v", err)}
			}
			return exitCodeError(localSingleNodeAuthorizationExitCode(result))
		},
	}
	cmd.Flags().StringVar(&rootPath, "root", "", "sealed single-node cluster evidence root")
	cmd.Flags().StringVar(&draftPath, "draft", "", "unpublished marker draft")
	cmd.Flags().StringVar(&outputPath, "output", "", "root/local-baseline.json marker")
	for _, name := range []string{"root", "draft", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}
