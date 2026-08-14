package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	"github.com/spf13/cobra"
)

func newLocalSingleNodeProfileThresholdCommand() *cobra.Command {
	var lifecyclePath, runID, outputPath string
	var offeredQPS, minimumThroughputPercent int
	cmd := &cobra.Command{
		Use:   "local-single-node-profile-threshold",
		Short: "Reduce typed single-node cluster lifecycle samples to the first measured profile threshold",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(runID) == "" || offeredQPS <= 0 || minimumThroughputPercent < 1 || minimumThroughputPercent > 100 {
				return commandExit{code: exitConfig, message: "--run-id, positive --offered-qps, and --minimum-throughput-percent in [1,100] are required"}
			}
			file, err := os.Open(filepath.Clean(lifecyclePath))
			if err != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("lifecycle evidence: %v", err)}
			}
			captures, partialLine, parseErr := localbaseline.ParseProfileLifecycleSnapshot(file)
			closeErr := file.Close()
			if parseErr != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("lifecycle evidence: %v", parseErr)}
			}
			if closeErr != nil {
				return commandExit{code: exitInternal, message: "lifecycle evidence close failed"}
			}
			query := localbaseline.QueryFirstMeasuredProfileThreshold(
				captures, runID, offeredQPS, minimumThroughputPercent,
			)
			query.PartialLine = partialLine
			if err := writeLocalSingleNodeJSON(outputPath, query); err != nil {
				return commandExit{code: exitInternal, message: "local single-node profile threshold write failed"}
			}
			if !query.EvidenceComplete {
				return commandExit{code: exitConfig, message: "local single-node profile threshold evidence is incomplete"}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&lifecyclePath, "lifecycle", "", "versioned periodic worker/process JSONL")
	cmd.Flags().StringVar(&runID, "run-id", "", "exact worker run ID")
	cmd.Flags().IntVar(&offeredQPS, "offered-qps", 0, "offered measured SEND/s")
	cmd.Flags().IntVar(&minimumThroughputPercent, "minimum-throughput-percent", 90, "minimum interval actual/offered percentage")
	cmd.Flags().StringVar(&outputPath, "output", "", "versioned profile threshold query JSON")
	for _, name := range []string{"lifecycle", "run-id", "offered-qps", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}
