package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const incompleteBackupOrphanTTL = 72 * time.Hour

// ArchiveFinalizerOptions configures publication identity and time.
type ArchiveFinalizerOptions struct {
	ClusterID   string
	Application string
	Now         func() time.Time
}

// ArchiveFinalizer verifies every Slot before publishing COMPLETE and then
// applies the configured bounded retention policy.
type ArchiveFinalizer struct {
	clusterID   string
	application string
	now         func() time.Time
}

// NewArchiveFinalizer creates the terminal archive stage.
func NewArchiveFinalizer(
	options ArchiveFinalizerOptions,
) (*ArchiveFinalizer, error) {
	if options.ClusterID == "" || options.Application == "" ||
		options.Now == nil {
		return nil, fmt.Errorf("backup archive finalizer: invalid options")
	}
	return &ArchiveFinalizer{
		clusterID:   options.ClusterID,
		application: options.Application,
		now:         options.Now,
	}, nil
}

// Publish verifies and makes one completed job discoverable.
func (f *ArchiveFinalizer) Publish(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	job backupcontract.BackupJob,
) error {
	trigger := backupartifact.Trigger(job.Trigger)
	switch trigger {
	case backupartifact.TriggerInitial,
		backupartifact.TriggerScheduled,
		backupartifact.TriggerManual:
	default:
		return fmt.Errorf("backup archive finalizer: invalid trigger")
	}
	if len(job.Slots) != backupartifact.DefaultHashSlotCount {
		return fmt.Errorf("backup archive finalizer: incomplete Slot progress")
	}
	slots := make(
		[]backupartifact.SlotReference,
		backupartifact.DefaultHashSlotCount,
	)
	for index, slot := range job.Slots {
		if index >= len(slots) || slot.HashSlot != uint16(index) ||
			slot.Status != backupcontract.SlotStatusComplete {
			return fmt.Errorf("backup archive finalizer: incomplete Slot progress")
		}
		slots[index] = backupartifact.SlotReference{
			HashSlot: slot.HashSlot, ManifestKey: slot.ManifestKey,
			ManifestSHA256: slot.ManifestSHA256,
			LogicalBytes:   slot.LogicalBytes, StoredBytes: slot.StoredBytes,
			Records: slot.Records, MaxMessageID: slot.MaxMessageID,
		}
	}
	_, err := runtimebackup.PublishArchive(
		ctx, store, runtimebackup.PublishArchiveRequest{
			ID:                  job.ID,
			Trigger:             trigger,
			SourceClusterID:     f.clusterID,
			SourceApplication:   f.application,
			StartedUnixMillis:   job.StartedAtUnixMillis,
			CompletedUnixMillis: job.UpdatedUnixMillis,
			Slots:               slots,
		},
	)
	return err
}

// ApplyRetention removes only complete, non-held archives older than the
// newest configured count.
func (f *ArchiveFinalizer) ApplyRetention(
	ctx context.Context,
	store backupartifact.ArchiveStore,
	retentionCount int,
) error {
	if err := f.pruneOrphans(ctx, store); err != nil {
		return err
	}
	_, err := backupusecase.ApplyRetention(ctx, store, retentionCount)
	return err
}

func (f *ArchiveFinalizer) pruneOrphans(
	ctx context.Context,
	store backupartifact.ArchiveStore,
) error {
	objects, err := store.List(ctx, "pending")
	if err != nil {
		return err
	}
	cutoff := f.now().UTC().Add(-incompleteBackupOrphanTTL)
	for _, object := range objects {
		id := strings.TrimPrefix(object.Key, "pending/")
		if id == "" || strings.Contains(id, "/") ||
			object.Modified.IsZero() || !object.Modified.Before(cutoff) {
			continue
		}
		complete, _, openErr := store.Open(
			ctx, "backups/"+id+"/COMPLETE",
		)
		if openErr == nil {
			if closeErr := complete.Close(); closeErr != nil {
				return closeErr
			}
		} else if errors.Is(openErr, backupartifact.ErrObjectNotFound) {
			if err := store.DeletePrefix(ctx, "backups/"+id); err != nil {
				return err
			}
		} else {
			return openErr
		}
		if err := store.Delete(ctx, object.Key); err != nil {
			return err
		}
	}
	return nil
}
