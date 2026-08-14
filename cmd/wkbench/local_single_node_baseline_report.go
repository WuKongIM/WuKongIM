package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	"github.com/spf13/cobra"
)

func newLocalSingleNodeBaselineReportCommand() *cobra.Command {
	var rootPath, evidencePath, sealedEvidencePath, outputPath string
	var closurePaths []string
	cmd := &cobra.Command{
		Use:   "local-single-node-baseline",
		Short: "Authorize the next diagnostic from closed single-node cluster evidence",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := openLocalSingleNodeArtifactRoot(rootPath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node evidence root failed"}
			}
			evidenceRelative, err := root.relative(evidencePath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node evidence path failed"}
			}
			evidenceData, err := root.read(evidenceRelative, localbaseline.MaximumEvidenceBytes)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node evidence open failed"}
			}
			evidence, parseErr := localbaseline.ParseBaselineEvidence(bytes.NewReader(evidenceData))
			if parseErr != nil {
				return commandExit{code: exitConfig, message: "local single-node evidence parse failed: " + parseErr.Error()}
			}
			evidence.StepClosures = make([]localbaseline.StepClosure, 0, len(closurePaths))
			for _, closurePath := range closurePaths {
				relative, relativeErr := root.relative(closurePath)
				if relativeErr != nil {
					return commandExit{code: exitConfig, message: "local single-node closure path failed"}
				}
				closure, closureErr := verifyLocalSingleNodeStepClosure(root, relative)
				if closureErr != nil {
					return commandExit{code: exitInternal, message: "local single-node closure verification failed: " + closureErr.Error()}
				}
				evidence.StepClosures = append(evidence.StepClosures, closure)
			}
			evidence.Seal.PayloadComplete = evidence.Seal.PayloadComplete &&
				len(evidence.StepClosures) == len(localbaseline.ReviewedOfferedSendQPS)
			localbaseline.SealBaselineEvidence(&evidence)
			result := localbaseline.AuthorizeThreeNodeDiagnostic(evidence)
			sealedRelative, err := root.relative(sealedEvidencePath)
			if err != nil || sealedRelative == evidenceRelative {
				return commandExit{code: exitConfig, message: "local single-node sealed evidence output failed"}
			}
			outputRelative, err := root.relative(outputPath)
			if err != nil {
				return commandExit{code: exitConfig, message: "local single-node authorization output failed"}
			}
			if err := root.writeJSONExclusive(sealedRelative, evidence); err != nil {
				return commandExit{code: exitInternal, message: "local single-node sealed evidence write failed"}
			}
			if err := root.writeJSONExclusive(outputRelative, result); err != nil {
				return commandExit{code: exitInternal, message: "local single-node authorization write failed"}
			}
			return exitCodeError(localSingleNodeAuthorizationExitCode(result))
		},
	}
	cmd.Flags().StringVar(&rootPath, "root", "", "single-node cluster artifact root")
	cmd.Flags().StringVar(&evidencePath, "evidence", "", "closed typed single-node cluster evidence JSON")
	cmd.Flags().StringVar(&sealedEvidencePath, "sealed-evidence-output", "", "verified baseline evidence JSON")
	cmd.Flags().StringSliceVar(&closurePaths, "step-closure", nil, "verified step closure manifest (repeat for each staircase step)")
	cmd.Flags().StringVar(&outputPath, "output", "", "derived typed authorization JSON")
	for _, name := range []string{"root", "evidence", "sealed-evidence-output", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func localSingleNodeAuthorizationExitCode(result localbaseline.AuthorizationResult) int {
	if result.ExitCode == 0 && result.Authorizes && result.ReviewedContractSatisfied && result.Outcome == localbaseline.OutcomeClean {
		return 0
	}
	if result.ExitCode == exitHardLimit && !result.Authorizes &&
		(result.Outcome == localbaseline.OutcomeRateFailed || result.Outcome == localbaseline.OutcomeProductFailure) {
		return exitHardLimit
	}
	if result.ExitCode == exitPreflight && !result.Authorizes &&
		(result.Outcome == localbaseline.OutcomeHostConfounded || result.Outcome == localbaseline.OutcomeStorageConfounded) {
		return exitPreflight
	}
	return exitInternal
}

func writeLocalSingleNodeAuthorization(path string, result localbaseline.AuthorizationResult) error {
	path = strings.TrimSpace(path)
	if path == "" || filepath.Base(path) == "." {
		return fmt.Errorf("local single-node authorization output path is invalid")
	}
	return writeLocalSingleNodeJSON(path, result)
}
