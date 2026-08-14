package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	"github.com/spf13/cobra"
)

const localTerminalQueuePendingExit = 3

func newLocalTerminalQueueConvergenceCommand() *cobra.Command {
	var postWarmupPath, candidatePath, runID, assignmentID, outputPath string
	cmd := &cobra.Command{
		Use:   "local-single-node-queue-convergence",
		Short: "Validate one exact pre-close product queue candidate against its post-warmup floor",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(runID) == "" || strings.TrimSpace(assignmentID) == "" {
				return commandExit{code: exitConfig, message: "--run-id and --assignment-id are required"}
			}
			baseline, err := readLocalSingleNodeBoundedFile(postWarmupPath, localbaseline.MaximumProductQueueCutBytes)
			if err != nil {
				return commandExit{code: exitInternal, message: fmt.Sprintf("post-warmup product queues: %v", err)}
			}
			candidate, err := readLocalSingleNodeBoundedFile(candidatePath, localbaseline.MaximumProductQueueCutBytes)
			if err != nil {
				return commandExit{code: exitInternal, message: fmt.Sprintf("candidate product queues: %v", err)}
			}
			result, err := localbaseline.QueryTerminalProductQueueConvergence(
				bytes.NewReader(baseline), bytes.NewReader(candidate), runID, assignmentID,
			)
			if err != nil {
				return commandExit{code: exitInternal, message: fmt.Sprintf("terminal product queue query: %v", err)}
			}
			if err := writeLocalSingleNodeJSON(outputPath, result); err != nil {
				return commandExit{code: exitInternal, message: "terminal product queue result write failed"}
			}
			if !result.EvidenceComplete {
				return commandExit{code: exitInternal, message: "terminal product queue evidence is incomplete"}
			}
			if !result.Converged {
				return commandExit{code: localTerminalQueuePendingExit}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&postWarmupPath, "post-warmup", "", "raw post-warmup Prometheus queue cut")
	cmd.Flags().StringVar(&candidatePath, "candidate", "", "raw pre-close Prometheus queue candidate")
	cmd.Flags().StringVar(&runID, "run-id", "", "exact worker run ID")
	cmd.Flags().StringVar(&assignmentID, "assignment-id", "", "exact worker assignment generation")
	cmd.Flags().StringVar(&outputPath, "output", "", "typed queue convergence result JSON")
	for _, name := range []string{"post-warmup", "candidate", "run-id", "assignment-id", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}
