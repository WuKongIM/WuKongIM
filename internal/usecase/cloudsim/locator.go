package cloudsim

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// RunLocatorSchemaV1 is the only accepted minimal locator schema.
	RunLocatorSchemaV1 = "wukongim.cloud_sim.run_locator/v1"
	maxRunLocatorBytes = 16 << 10
)

// ErrInvalidRunLocator reports a malformed, unsupported, or non-minimal locator.
var ErrInvalidRunLocator = errors.New("internal/usecase/cloudsim: invalid run locator")

// RunLocator is the minimal non-diagnostic record retained by GitHub.
type RunLocator struct {
	// Schema identifies the strict locator wire contract.
	Schema string `json:"schema"`
	// RunID is the exact globally unique Simulation Run identity.
	RunID string `json:"run_id"`
	// Provider identifies the Cloud Provider Adapter used by the run.
	Provider string `json:"provider"`
	// Region identifies where live provider inventory must be queried.
	Region string `json:"region"`
	// AccountIDHash is a non-secret hash of the cloud account binding.
	AccountIDHash string `json:"account_id_hash"`
	// Repository is the trusted owner/name identity.
	Repository string `json:"repository"`
	// SourceSHA is the immutable deployed source commit.
	SourceSHA string `json:"source_sha"`
	// ScenarioDigest identifies the selected Scenario Profile.
	ScenarioDigest string `json:"scenario_digest"`
	// CreatedAt records when provisioning began.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt records the immutable Run Lease deadline.
	ExpiresAt time.Time `json:"expires_at"`
	// ProvisionWorkflowRunID links to the originating GitHub workflow run.
	ProvisionWorkflowRunID int64 `json:"provision_workflow_run_id"`
}

// Validate verifies the strict minimal Run Locator contract.
func (l RunLocator) Validate() error {
	if l.Schema != RunLocatorSchemaV1 || strings.TrimSpace(l.RunID) == "" || strings.TrimSpace(l.Provider) == "" ||
		strings.TrimSpace(l.Region) == "" || strings.TrimSpace(l.AccountIDHash) == "" || strings.TrimSpace(l.Repository) == "" ||
		strings.TrimSpace(l.SourceSHA) == "" || strings.TrimSpace(l.ScenarioDigest) == "" || l.CreatedAt.IsZero() ||
		!l.ExpiresAt.After(l.CreatedAt) || l.ProvisionWorkflowRunID <= 0 {
		return ErrInvalidRunLocator
	}
	return nil
}

// EncodeRunLocator writes a validated locator as one bounded JSON document.
func EncodeRunLocator(w io.Writer, locator RunLocator) error {
	if err := locator.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(locator)
	if err != nil {
		return fmt.Errorf("marshal run locator: %w", err)
	}
	if len(data)+1 > maxRunLocatorBytes {
		return ErrInvalidRunLocator
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// DecodeRunLocator reads one strict bounded JSON locator and rejects extra fields.
func DecodeRunLocator(r io.Reader) (RunLocator, error) {
	decoder := json.NewDecoder(io.LimitReader(r, maxRunLocatorBytes+1))
	decoder.DisallowUnknownFields()
	var locator RunLocator
	if err := decoder.Decode(&locator); err != nil {
		return RunLocator{}, fmt.Errorf("%w: %v", ErrInvalidRunLocator, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RunLocator{}, ErrInvalidRunLocator
	}
	if err := locator.Validate(); err != nil {
		return RunLocator{}, err
	}
	return locator, nil
}
