package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const maxDeploymentJSONBytes = 1 << 20

func addDeploymentCommands(root *cobra.Command, stdout io.Writer) {
	var receiptPath, manifestPath, planNow string
	planCommand := &cobra.Command{
		Use: "deployment-plan", Short: "Bind an active Cloud Lease Receipt to the native deployment bundle", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var receipt cloudlease.Receipt
			if err := readStrictDeploymentJSON(receiptPath, &receipt); err != nil {
				return fmt.Errorf("read Lease Receipt: %w", err)
			}
			if err := cloudlease.ValidateReceipt(receipt); err != nil {
				return fmt.Errorf("validate Lease Receipt: %w", err)
			}
			var manifest clouddeploy.Manifest
			if err := readStrictDeploymentJSON(manifestPath, &manifest); err != nil {
				return fmt.Errorf("read bundle manifest: %w", err)
			}
			now, err := deploymentNow(planNow)
			if err != nil {
				return err
			}
			plan, err := clouddeploy.BuildPlan(normalizeLeaseReceipt(receipt), manifest, now)
			if err != nil {
				return err
			}
			return writeDeploymentJSON(stdout, plan)
		},
	}
	planCommand.Flags().StringVar(&receiptPath, "lease-receipt", "", "strict active Cloud Lease Receipt JSON")
	planCommand.Flags().StringVar(&manifestPath, "bundle-manifest", "", "strict offline bundle manifest JSON")
	planCommand.Flags().StringVar(&planNow, "now", "", "optional RFC3339 validation time")
	_ = planCommand.MarkFlagRequired("lease-receipt")
	_ = planCommand.MarkFlagRequired("bundle-manifest")

	var deploymentPlanPath, snapshotPath, gateManifestPath, gateNow string
	gateCommand := &cobra.Command{
		Use: "deployment-gate", Short: "Evaluate four-host native deployment readiness", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var plan clouddeploy.DeploymentPlan
			if err := readStrictDeploymentJSON(deploymentPlanPath, &plan); err != nil {
				return fmt.Errorf("read deployment plan: %w", err)
			}
			var manifest clouddeploy.Manifest
			if err := readStrictDeploymentJSON(gateManifestPath, &manifest); err != nil {
				return fmt.Errorf("read bundle manifest: %w", err)
			}
			var snapshot clouddeploy.ReadinessSnapshot
			if err := readStrictDeploymentJSON(snapshotPath, &snapshot); err != nil {
				return fmt.Errorf("read readiness snapshot: %w", err)
			}
			now, err := deploymentNow(gateNow)
			if err != nil {
				return err
			}
			if err := clouddeploy.ValidatePlan(plan, manifest, now); err != nil {
				outcome := clouddeploy.Outcome{Failure: &clouddeploy.DeploymentFailure{
					Schema: clouddeploy.FailureSchemaV1, Code: clouddeploy.FailureInvalidPlan,
					LastCompletedGate: clouddeploy.GateNone, Evidence: []string{"deployment plan validation failed"},
				}}
				if writeErr := writeDeploymentJSON(stdout, outcome); writeErr != nil {
					return writeErr
				}
				return errors.New("deployment gate failed")
			}
			outcome := clouddeploy.EvaluateReadiness(plan, snapshot, now)
			if err := writeDeploymentJSON(stdout, outcome); err != nil {
				return err
			}
			if !outcome.Passed {
				return errors.New("deployment gate failed")
			}
			return nil
		},
	}
	gateCommand.Flags().StringVar(&deploymentPlanPath, "plan", "", "strict Deployment Plan JSON")
	gateCommand.Flags().StringVar(&gateManifestPath, "bundle-manifest", "", "strict offline bundle manifest JSON")
	gateCommand.Flags().StringVar(&snapshotPath, "snapshot", "", "strict Readiness Snapshot JSON")
	gateCommand.Flags().StringVar(&gateNow, "now", "", "optional RFC3339 validation time")
	_ = gateCommand.MarkFlagRequired("plan")
	_ = gateCommand.MarkFlagRequired("bundle-manifest")
	_ = gateCommand.MarkFlagRequired("snapshot")
	root.AddCommand(planCommand, gateCommand)
}

func normalizeLeaseReceipt(receipt cloudlease.Receipt) clouddeploy.LeaseInventory {
	result := clouddeploy.LeaseInventory{
		LeaseID: receipt.LeaseID, RequestID: receipt.RequestID, Repository: receipt.Repository,
		Provider: receipt.Provider, Region: receipt.Region, Zone: receipt.Zone, PlanDigest: deploymentDigest(receipt.PlanDigest),
		SourceSHA: receipt.Provenance.SourceSHA, BundleDigest: receipt.Provenance.BundleDigest,
		State: string(receipt.State), ExpiresAt: receipt.ExpiresAt,
		CreatedAt: receipt.CreatedAt,
		Budget: clouddeploy.DeploymentBudget{
			Currency: receipt.Budget.Currency, LimitMicros: receipt.Budget.LimitMicros,
			OperationalStopMicros: receipt.Budget.OperationalStopMicros,
			CommittedMicros:       receipt.Budget.CommittedMicros,
			EstimatedCostMicros:   receipt.Quote.EstimatedCostMicros,
		},
	}
	for _, item := range receipt.Quote.LineItems {
		result.Budget.LineItems = append(result.Budget.LineItems, clouddeploy.DeploymentBudgetLineItem{
			Kind: item.Kind, Role: item.Role, Quantity: item.Quantity, CostMicros: item.CostMicros,
		})
	}
	for _, resource := range receipt.Resources {
		normalized := clouddeploy.LeaseResource{
			ID: resource.ID, Role: resource.Role, ParentID: resource.ParentID, SizeBytes: resource.SizeBytes,
			PrivateAddress: resource.PrivateAddress, PublicAddress: resource.PublicAddress,
		}
		switch resource.Kind {
		case "instance", "compute":
			normalized.Kind = "instance"
		case "disk":
			if resource.Attributes["disk_type"] != "data" && resource.Attributes["disk_role"] != "data" {
				continue
			}
			normalized.Kind = "data_disk"
		case "eip", "public-address":
			normalized.Kind = "public_address"
		default:
			continue
		}
		result.Resources = append(result.Resources, normalized)
	}
	return result
}

func deploymentDigest(value string) string {
	if strings.HasPrefix(value, "sha256:") {
		return value
	}
	return "sha256:" + value
}

func readStrictDeploymentJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxDeploymentJSONBytes {
		return errors.New("JSON input exceeds the bounded file size")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxDeploymentJSONBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func writeDeploymentJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func deploymentNow(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse --now: %w", err)
	}
	return parsed.UTC(), nil
}
