package backup

import (
	"context"
	"errors"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestContinuousCoordinatorPublishUsesTypedDoctorGate(t *testing.T) {
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 1},
			CheckpointInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	if _, err := coordinator.PublishCheckpoint(
		context.Background(),
	); !errors.Is(err, ErrContinuousDoctorUnhealthy) {
		t.Fatalf(
			"PublishCheckpoint() error = %v, want ErrContinuousDoctorUnhealthy",
			err,
		)
	}
}

type idleContinuousCapture struct{}

func (idleContinuousCapture) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (idleContinuousCapture) Status() []backupcontract.SlotCaptureStatus {
	return nil
}

type recordingContinuousCheckpointPublisher struct{}

func (recordingContinuousCheckpointPublisher) Publish(
	context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	return backupartifact.CheckpointCatalogCommit{}, nil
}
