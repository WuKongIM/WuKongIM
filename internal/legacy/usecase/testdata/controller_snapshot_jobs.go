package testdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// ControllerSnapshotJobsDataset is the reusable e2e dataset for large Controller snapshots.
	ControllerSnapshotJobsDataset = "cluster/controller-snapshot-jobs"

	maxControllerSnapshotJobCount        = 1_000_000
	maxControllerSnapshotJobPayloadBytes = 1 << 20
	controllerSnapshotJobWriteTimeout    = 30 * time.Second
)

// GenerateControllerSnapshotJobsCommand describes deterministic payload-heavy controller metadata rows.
type GenerateControllerSnapshotJobsCommand struct {
	Prefix       string
	TargetNodeID uint64
	Count        int
	PayloadBytes int
	Seed         string
}

// GenerateControllerSnapshotJobsResult summarizes generated controller metadata rows.
type GenerateControllerSnapshotJobsResult struct {
	Dataset      string `json:"dataset"`
	Prefix       string `json:"prefix"`
	TargetNodeID uint64 `json:"target_node_id"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	FirstJobID   string `json:"first_job_id"`
	LastJobID    string `json:"last_job_id"`
}

// GenerateControllerSnapshotJobs creates payload-heavy planned onboarding jobs through Controller Raft.
func (a *App) GenerateControllerSnapshotJobs(ctx context.Context, cmd GenerateControllerSnapshotJobsCommand) (GenerateControllerSnapshotJobsResult, error) {
	if err := validateGenerateControllerSnapshotJobsCommand(cmd); err != nil {
		return GenerateControllerSnapshotJobsResult{}, err
	}
	if a == nil || a.controllerSnapshotJobs == nil {
		return GenerateControllerSnapshotJobsResult{}, errors.New("usecase/testdata: controller snapshot job cluster required")
	}

	var firstJobID, lastJobID string
	for i := 0; i < cmd.Count; i++ {
		payloadID := controllerSnapshotJobPayloadID(cmd.Prefix, i)
		writeCtx, cancel := context.WithTimeout(ctx, controllerSnapshotJobWriteTimeout)
		job, err := a.controllerSnapshotJobs.CreateNodeOnboardingPlan(writeCtx, cmd.TargetNodeID, deterministicPayload(cmd.Seed, payloadID, i, cmd.PayloadBytes))
		cancel()
		if err != nil {
			return GenerateControllerSnapshotJobsResult{}, err
		}
		if i == 0 {
			firstJobID = job.JobID
		}
		lastJobID = job.JobID
	}

	return GenerateControllerSnapshotJobsResult{
		Dataset:      ControllerSnapshotJobsDataset,
		Prefix:       cmd.Prefix,
		TargetNodeID: cmd.TargetNodeID,
		Count:        cmd.Count,
		PayloadBytes: cmd.PayloadBytes,
		FirstJobID:   firstJobID,
		LastJobID:    lastJobID,
	}, nil
}

func validateGenerateControllerSnapshotJobsCommand(cmd GenerateControllerSnapshotJobsCommand) error {
	if strings.TrimSpace(cmd.Prefix) == "" {
		return errors.New("usecase/testdata: prefix is required")
	}
	if cmd.TargetNodeID == 0 {
		return errors.New("usecase/testdata: target_node_id is required")
	}
	if cmd.Count <= 0 || cmd.Count > maxControllerSnapshotJobCount {
		return fmt.Errorf("usecase/testdata: count must be between 1 and %d", maxControllerSnapshotJobCount)
	}
	if cmd.PayloadBytes <= 0 || cmd.PayloadBytes > maxControllerSnapshotJobPayloadBytes {
		return fmt.Errorf("usecase/testdata: payload_bytes must be between 1 and %d", maxControllerSnapshotJobPayloadBytes)
	}
	return nil
}

func controllerSnapshotJobPayloadID(prefix string, index int) string {
	return fmt.Sprintf("%s-%06d", prefix, index)
}
