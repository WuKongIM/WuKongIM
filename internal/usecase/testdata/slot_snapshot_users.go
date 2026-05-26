package testdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// SlotSnapshotUsersDataset is the reusable e2e dataset for large Slot snapshots.
	SlotSnapshotUsersDataset = "cluster/slot-snapshot-users"

	maxSlotSnapshotUserCount        = 1_000_000
	maxSlotSnapshotUserPayloadBytes = 1 << 20
	slotSnapshotUserWriteTimeout    = 30 * time.Second
)

// SlotSnapshotUserStore persists generated user/device rows through the Slot store path.
type SlotSnapshotUserStore interface {
	UpsertUser(ctx context.Context, u metadb.User) error
	UpsertDevice(ctx context.Context, d metadb.Device) error
}

// ControllerSnapshotJobCluster persists generated controller metadata through Controller Raft.
type ControllerSnapshotJobCluster interface {
	CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error)
}

// AppOptions configures the e2e test-data usecase.
type AppOptions struct {
	// SlotSnapshotUsers writes user/device rows used to inflate Slot snapshots.
	SlotSnapshotUsers SlotSnapshotUserStore
	// ControllerSnapshotJobs writes payload-heavy onboarding jobs used to inflate Controller snapshots.
	ControllerSnapshotJobs ControllerSnapshotJobCluster
}

// App generates deterministic e2e test data through normal storage usecases.
type App struct {
	slotSnapshotUsers      SlotSnapshotUserStore
	controllerSnapshotJobs ControllerSnapshotJobCluster
}

// New creates a test-data generation usecase.
func New(opts AppOptions) *App {
	return &App{
		slotSnapshotUsers:      opts.SlotSnapshotUsers,
		controllerSnapshotJobs: opts.ControllerSnapshotJobs,
	}
}

// GenerateSlotSnapshotUsersCommand describes deterministic user/device rows for a large Slot snapshot.
type GenerateSlotSnapshotUsersCommand struct {
	Prefix       string
	Count        int
	PayloadBytes int
	Seed         string
	DeviceFlag   uint8
	DeviceLevel  uint8
}

// GenerateSlotSnapshotUsersResult summarizes generated Slot snapshot rows.
type GenerateSlotSnapshotUsersResult struct {
	Dataset      string `json:"dataset"`
	Prefix       string `json:"prefix"`
	Count        int    `json:"count"`
	PayloadBytes int    `json:"payload_bytes"`
	FirstUID     string `json:"first_uid"`
	LastUID      string `json:"last_uid"`
}

// GenerateSlotSnapshotUsers upserts deterministic users and payload-heavy devices.
func (a *App) GenerateSlotSnapshotUsers(ctx context.Context, cmd GenerateSlotSnapshotUsersCommand) (GenerateSlotSnapshotUsersResult, error) {
	if err := validateGenerateSlotSnapshotUsersCommand(cmd); err != nil {
		return GenerateSlotSnapshotUsersResult{}, err
	}
	if a == nil || a.slotSnapshotUsers == nil {
		return GenerateSlotSnapshotUsersResult{}, errors.New("usecase/testdata: slot snapshot user store required")
	}

	for i := 0; i < cmd.Count; i++ {
		uid := slotSnapshotUserUID(cmd.Prefix, i)
		writeCtx, cancel := context.WithTimeout(ctx, slotSnapshotUserWriteTimeout)
		err := a.slotSnapshotUsers.UpsertUser(writeCtx, metadb.User{UID: uid})
		cancel()
		if err != nil {
			return GenerateSlotSnapshotUsersResult{}, err
		}
		writeCtx, cancel = context.WithTimeout(ctx, slotSnapshotUserWriteTimeout)
		err = a.slotSnapshotUsers.UpsertDevice(writeCtx, metadb.Device{
			UID:         uid,
			DeviceFlag:  int64(cmd.DeviceFlag),
			Token:       deterministicPayload(cmd.Seed, uid, i, cmd.PayloadBytes),
			DeviceLevel: int64(cmd.DeviceLevel),
		})
		cancel()
		if err != nil {
			return GenerateSlotSnapshotUsersResult{}, err
		}
	}

	return GenerateSlotSnapshotUsersResult{
		Dataset:      SlotSnapshotUsersDataset,
		Prefix:       cmd.Prefix,
		Count:        cmd.Count,
		PayloadBytes: cmd.PayloadBytes,
		FirstUID:     slotSnapshotUserUID(cmd.Prefix, 0),
		LastUID:      slotSnapshotUserUID(cmd.Prefix, cmd.Count-1),
	}, nil
}

func validateGenerateSlotSnapshotUsersCommand(cmd GenerateSlotSnapshotUsersCommand) error {
	if strings.TrimSpace(cmd.Prefix) == "" {
		return errors.New("usecase/testdata: prefix is required")
	}
	if cmd.Count <= 0 || cmd.Count > maxSlotSnapshotUserCount {
		return fmt.Errorf("usecase/testdata: count must be between 1 and %d", maxSlotSnapshotUserCount)
	}
	if cmd.PayloadBytes <= 0 || cmd.PayloadBytes > maxSlotSnapshotUserPayloadBytes {
		return fmt.Errorf("usecase/testdata: payload_bytes must be between 1 and %d", maxSlotSnapshotUserPayloadBytes)
	}
	return nil
}

func slotSnapshotUserUID(prefix string, index int) string {
	return fmt.Sprintf("%s-%06d", prefix, index)
}

func deterministicPayload(seed, uid string, index int, size int) string {
	if seed == "" {
		seed = "wk-e2e"
	}
	prefix := fmt.Sprintf("%s:%s:%06d:", seed, uid, index)
	if len(prefix) >= size {
		return prefix[:size]
	}
	var b strings.Builder
	b.Grow(size)
	b.WriteString(prefix)
	pattern := fmt.Sprintf("%s:%06d:", seed, index)
	for b.Len()+len(pattern) <= size {
		b.WriteString(pattern)
	}
	if b.Len() < size {
		b.WriteString(pattern[:size-b.Len()])
	}
	return b.String()
}
