package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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
	addPrepareCapacityConfigCommand(root)
	addFormalChainReportCommand(root)
	addSealAccessCommand(root)
	addOpenAccessCommand(root)
	addDeploymentIdentityCommands(root)
	addRepairCommands(root)
	return root
}

type formalChainResult struct {
	Schema          string                       `json:"schema"`
	Outcome         chatlifecycle.VerdictOutcome `json:"outcome"`
	Cause           chatlifecycle.VerdictCause   `json:"cause"`
	FormalOutcome   chatlifecycle.VerdictOutcome `json:"formal_outcome"`
	CapacityOutcome chatlifecycle.VerdictOutcome `json:"capacity_outcome,omitempty"`
	LowerBound      bool                         `json:"lower_bound"`
	End             time.Time                    `json:"end"`
}

func addFormalChainReportCommand(root *cobra.Command) {
	var formalPath, capacityPath string
	command := &cobra.Command{
		Use: "validate-formal-chain", Short: "Validate terminal formal and same-Lease capacity reports", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			formal, err := chatlifecycle.ReadReport(formalPath)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			var capacity *chatlifecycle.Report
			if strings.TrimSpace(capacityPath) != "" {
				read, readErr := chatlifecycle.ReadReport(capacityPath)
				if readErr != nil {
					return chatlifecyclerun.ErrInvalidInput
				}
				capacity = &read
			}
			result, resultErr := formalChainResultFromReports(formal, capacity)
			if resultErr != nil {
				return resultErr
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(result)
		},
	}
	command.Flags().StringVar(&formalPath, "formal-report", "", "terminal formal 72-hour or early-failure report")
	command.Flags().StringVar(&capacityPath, "capacity-report", "", "terminal aged-data capacity and recovery report")
	if err := command.MarkFlagRequired("formal-report"); err != nil {
		panic(err)
	}
	root.AddCommand(command)
}

func formalChainResultFromReports(formal chatlifecycle.Report, capacity *chatlifecycle.Report) (formalChainResult, error) {
	if formal.Profile != chatlifecycle.ProfileFormal || formal.Mode != chatlifecycle.ModeSoak ||
		formal.Stage != chatlifecycle.StageFormal || formal.Kind != chatlifecycle.CheckpointFinal ||
		!formal.Final || formal.Continue || !formal.Continuous || !formal.Verdict.Terminal {
		return formalChainResult{}, chatlifecyclerun.ErrInvalidInput
	}
	result := formalChainResult{
		Schema: "wukongim.chat_lifecycle.formal_chain_result/v1", Outcome: formal.Verdict.Outcome,
		Cause: formal.Verdict.Cause, FormalOutcome: formal.Verdict.Outcome, End: formal.Window.End,
	}
	if formal.Verdict.Outcome != chatlifecycle.VerdictPass {
		if capacity != nil {
			return formalChainResult{}, chatlifecyclerun.ErrInvalidInput
		}
		return result, nil
	}
	if capacity == nil || capacity.Profile != chatlifecycle.ProfileFormal || capacity.Mode != chatlifecycle.ModeCapacity ||
		capacity.Stage != chatlifecycle.StageFormal || capacity.Kind != chatlifecycle.CheckpointFinal ||
		!capacity.Final || capacity.Continue || !capacity.Continuous || !capacity.Verdict.Terminal || !capacity.Capacity.Attempted {
		return formalChainResult{}, chatlifecyclerun.ErrInvalidInput
	}
	// A terminal shape alone is not continuity evidence. Bind both reports to
	// the exact live dataset, worker fence, threshold contract, and a strictly
	// ordered time window so separately valid process lifetimes cannot be
	// spliced into one formal verdict.
	if capacity.DatasetDigest != formal.DatasetDigest || capacity.Fence != formal.Fence ||
		capacity.Thresholds != formal.Thresholds || !capacity.Window.Start.After(formal.Window.End) {
		return formalChainResult{}, chatlifecyclerun.ErrInvalidInput
	}
	result.Outcome, result.Cause, result.CapacityOutcome = capacity.Verdict.Outcome, capacity.Verdict.Cause, capacity.Verdict.Outcome
	result.LowerBound, result.End = capacity.Capacity.LowerBound, capacity.Window.End
	return result, nil
}

func addPrepareCapacityConfigCommand(root *cobra.Command) {
	var configPath, checkpointPath, outputPath string
	command := &cobra.Command{
		Use: "prepare-capacity-config", Short: "Bind capacity mode to one passing live 72-hour formal checkpoint", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			formal, err := chatlifecycle.LoadConfig(configPath)
			if err != nil {
				return err
			}
			checkpoint, err := chatlifecycle.ReadReport(checkpointPath)
			if err != nil {
				return err
			}
			prepared, err := chatlifecycle.PrepareCapacityConfig(formal, checkpoint, checkpointPath)
			if err != nil {
				return err
			}
			body, err := yaml.Marshal(prepared)
			if err != nil {
				return err
			}
			return writePrivateAtomic(outputPath, body)
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "sealed formal chat-lifecycle YAML")
	command.Flags().StringVar(&checkpointPath, "checkpoint", "", "passing 72-hour formal report")
	command.Flags().StringVar(&outputPath, "output", "", "new capacity YAML output")
	for _, name := range []string{"config", "checkpoint", "output"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func writePrivateAtomic(path string, body []byte) error {
	clean := filepath.Clean(path)
	if strings.TrimSpace(path) == "" || len(body) == 0 || clean == "." {
		return chatlifecyclerun.ErrInvalidInput
	}
	if _, err := os.Stat(clean); err == nil || !os.IsNotExist(err) {
		return chatlifecyclerun.ErrInvalidInput
	}
	temporary, err := os.CreateTemp(filepath.Dir(clean), ".wkchatlifecycle-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, clean)
}

type materializeOptions struct {
	templatePath, sourceSHA, operator, codexPubKey, requestID string
	repository, bundleDigest, deploymentPubKey, nowValue      string
	transitionPath                                            string
	attempt, committedMicros, authorizedRepairBudgetCNY       int64
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
	flags.StringVar(&options.transitionPath, "transition", "", "authenticated prior-stage transition document")
	flags.Int64Var(&options.attempt, "attempt", 0, "trusted Lease attempt number")
	flags.Int64Var(&options.committedMicros, "committed-micros", 0, "prior aggregate budget commitment")
	flags.Int64Var(&options.authorizedRepairBudgetCNY, "authorized-repair-budget-cny", 0, "independently authorized whole-CNY direct repair budget")
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
		AuthorizedRepairBudgetCNY: options.authorizedRepairBudgetCNY,
	}
	if options.transitionPath != "" {
		var transition chatlifecyclerun.StageTransition
		if err := readStrict(options.transitionPath, &transition); err != nil {
			return err
		}
		trusted.Transition = &transition
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
	var path, runStartPath string
	command := &cobra.Command{
		Use: "validate-rehearsal-report", Short: "Validate one terminal bounded rehearsal report", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report, err := chatlifecycle.ReadReport(path)
			if err != nil || report.Stage != chatlifecycle.StageRehearsal || report.Kind != chatlifecycle.CheckpointFinal ||
				!report.Final || report.Continue || !report.Verdict.Terminal {
				return chatlifecyclerun.ErrInvalidInput
			}
			var runStart chatlifecycle.RunStartReceipt
			if err := readStrict(runStartPath, &runStart); err != nil {
				return err
			}
			if err := validateRehearsalReportRunStart(report, runStart); err != nil {
				return err
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(reportResult{
				Schema: "wukongim.chat_lifecycle.rehearsal_result/v1", Stage: report.Stage,
				Outcome: report.Verdict.Outcome, Cause: report.Verdict.Cause, End: report.Window.End,
			})
		},
	}
	command.Flags().StringVar(&path, "report", "", "bounded chat-lifecycle report")
	command.Flags().StringVar(&runStartPath, "run-start", "", "exact run-start receipt for the report generation")
	for _, name := range []string{"report", "run-start"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func validateRehearsalReportRunStart(report chatlifecycle.Report, runStart chatlifecycle.RunStartReceipt) error {
	if runStart.Schema != chatlifecycle.RunStartReceiptSchemaV1 ||
		runStart.Stage != chatlifecycle.StageRehearsal ||
		runStart.StartedAt.IsZero() || !runStart.ExpectedEndAt.After(runStart.StartedAt) ||
		report.Stage != runStart.Stage || report.Fence.RunHash != runStart.RunHash ||
		report.Fence.AssignmentHash != runStart.AssignmentHash ||
		report.Fence.Generation != runStart.Generation ||
		!report.Window.Start.Equal(runStart.StartedAt) {
		return chatlifecyclerun.ErrInvalidInput
	}
	return nil
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
