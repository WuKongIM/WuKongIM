package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const maxInputBytes = 1 << 20

func main() {
	command := newRootCommand(os.Stdout)
	command.SetErr(os.Stderr)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(stdout io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "wkchatlifecycle", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(stdout)
	addMaterializeCommand(root)
	addSelectorCommand(root)
	addPlanSelectorCommand(root)
	addReportCommand(root)
	return root
}

type materializeOptions struct {
	templatePath, sourceSHA, operator, codexPubKey, requestID string
	repository, bundleDigest, deploymentPubKey, nowValue      string
	attempt, committedMicros                                  int64
	excludedZone, excludedComputeType                         string
}

func addMaterializeCommand(root *cobra.Command) {
	var options materializeOptions
	command := &cobra.Command{
		Use: "materialize", Short: "Bind the fixed operator surface to the reviewed Run Plan", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runMaterialize(command.OutOrStdout(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.templatePath, "template", "", "reviewed repository Run Plan template")
	flags.StringVar(&options.sourceSHA, "source-sha", "", "immutable source commit")
	flags.StringVar(&options.operator, "operator", "", "fixed operator identity")
	flags.StringVar(&options.codexPubKey, "codex-diagnostic-pubkey", "", "Codex diagnostic Ed25519 public key")
	flags.StringVar(&options.requestID, "request-id", "", "request correlation identity")
	flags.StringVar(&options.repository, "repository", "", "trusted workflow repository")
	flags.StringVar(&options.bundleDigest, "bundle-digest", "", "trusted offline bundle digest")
	flags.StringVar(&options.deploymentPubKey, "deployment-pubkey", "", "derived deployment Ed25519 public key")
	flags.StringVar(&options.nowValue, "now", "", "trusted RFC3339 UTC materialization time")
	flags.Int64Var(&options.attempt, "attempt", 0, "trusted Lease attempt number")
	flags.Int64Var(&options.committedMicros, "committed-micros", 0, "prior aggregate budget commitment")
	flags.StringVar(&options.excludedZone, "excluded-zone", "", "prior failed placement zone")
	flags.StringVar(&options.excludedComputeType, "excluded-compute-type", "", "prior failed provider compute type")
	for _, name := range []string{"template", "source-sha", "operator", "codex-diagnostic-pubkey", "request-id", "repository", "bundle-digest", "deployment-pubkey", "now", "attempt"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func runMaterialize(stdout io.Writer, options materializeOptions) error {
	file, err := os.Open(options.templatePath)
	if err != nil {
		return err
	}
	defer file.Close()
	template, err := chatlifecyclerun.DecodeTemplate(file)
	if err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339, options.nowValue)
	if err != nil || now.Location() != time.UTC {
		return chatlifecyclerun.ErrInvalidInput
	}
	trusted := chatlifecyclerun.TrustedContext{
		Repository: options.repository, BundleDigest: options.bundleDigest,
		DeploymentPubKey: options.deploymentPubKey, Now: now,
		Attempt: int(options.attempt), CommittedMicros: options.committedMicros,
	}
	if options.excludedZone != "" || options.excludedComputeType != "" {
		trusted.ExcludedPlacement = &cloudlease.PlacementExclusion{Zone: options.excludedZone, ComputeType: options.excludedComputeType}
	}
	plan, err := chatlifecyclerun.Materialize(template, chatlifecyclerun.OperatorInput{
		SourceSHA: options.sourceSHA, Operator: options.operator,
		CodexDiagnosticPubKey: options.codexPubKey, RequestID: options.requestID,
	}, trusted)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(plan)
}

type receiptDocument struct {
	Schema  string             `json:"schema"`
	Receipt cloudlease.Receipt `json:"receipt"`
}

type selectorDocument struct {
	Schema   string              `json:"schema"`
	Selector cloudlease.Selector `json:"selector"`
}

type quoteDocument struct {
	Schema string           `json:"schema"`
	Quote  cloudlease.Quote `json:"quote"`
}

func addSelectorCommand(root *cobra.Command) {
	var path string
	command := &cobra.Command{
		Use: "selector", Short: "Project one exact release selector from a typed Lease Receipt", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var document receiptDocument
			if err := readStrict(path, &document); err != nil || document.Schema != "wukongim.cloud_lease.receipt/v1" {
				return chatlifecyclerun.ErrInvalidInput
			}
			receipt := document.Receipt
			selector := cloudlease.Selector{
				LeaseID: receipt.LeaseID, RequestID: receipt.RequestID, Provider: receipt.Provider,
				Region: receipt.Region, Repository: receipt.Repository, PlanDigest: receipt.PlanDigest,
			}
			if err := cloudlease.ValidateReceipt(receipt); err != nil {
				return err
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(selectorDocument{Schema: "wukongim.cloud_lease.selector/v1", Selector: selector})
		},
	}
	command.Flags().StringVar(&path, "receipt", "", "typed Cloud Lease Receipt document")
	if err := command.MarkFlagRequired("receipt"); err != nil {
		panic(err)
	}
	root.AddCommand(command)
}

func addPlanSelectorCommand(root *cobra.Command) {
	var planPath, quotePath string
	command := &cobra.Command{
		Use: "selector-from-plan", Short: "Derive cleanup identity before a paid Lease dispatch", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var plan cloudlease.Plan
			if err := readStrict(planPath, &plan); err != nil {
				return err
			}
			var document quoteDocument
			if err := readStrict(quotePath, &document); err != nil || document.Schema != "wukongim.cloud_lease.quote/v1" {
				return chatlifecyclerun.ErrInvalidInput
			}
			selector, err := cloudlease.ReleaseSelectorFromPlanQuote(plan, document.Quote, time.Now().UTC())
			if err != nil {
				return err
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(selectorDocument{
				Schema: "wukongim.cloud_lease.selector/v1", Selector: selector,
			})
		},
	}
	command.Flags().StringVar(&planPath, "plan", "", "strict Cloud Lease Plan")
	command.Flags().StringVar(&quotePath, "quote", "", "typed read-only Cloud Lease Quote")
	for _, name := range []string{"plan", "quote"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

type reportResult struct {
	Schema  string                       `json:"schema"`
	Stage   chatlifecycle.Stage          `json:"stage"`
	Outcome chatlifecycle.VerdictOutcome `json:"outcome"`
	Cause   chatlifecycle.VerdictCause   `json:"cause"`
	End     time.Time                    `json:"end"`
}

func addReportCommand(root *cobra.Command) {
	var path string
	command := &cobra.Command{
		Use: "validate-rehearsal-report", Short: "Validate one terminal bounded rehearsal report", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report, err := chatlifecycle.ReadReport(path)
			if err != nil || report.Stage != chatlifecycle.StageRehearsal || report.Kind != chatlifecycle.CheckpointFinal ||
				!report.Final || report.Continue || !report.Verdict.Terminal {
				return chatlifecyclerun.ErrInvalidInput
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(reportResult{
				Schema: "wukongim.chat_lifecycle.rehearsal_result/v1", Stage: report.Stage,
				Outcome: report.Verdict.Outcome, Cause: report.Verdict.Cause, End: report.Window.End,
			})
		},
	}
	command.Flags().StringVar(&path, "report", "", "bounded chat-lifecycle report")
	if err := command.MarkFlagRequired("report"); err != nil {
		panic(err)
	}
	root.AddCommand(command)
}

func readStrict(path string, output any) error {
	if strings.TrimSpace(path) == "" {
		return chatlifecyclerun.ErrInvalidInput
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 || len(body) > maxInputBytes {
		return chatlifecyclerun.ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return chatlifecyclerun.ErrInvalidInput
	}
	return nil
}
