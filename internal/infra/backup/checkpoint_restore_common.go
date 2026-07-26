package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

const maxCheckpointRestoreObjectBytes = 256 << 20

// RestoreTargetState is authoritative successor identity and emptiness evidence.
type RestoreTargetState struct {
	// ClusterID identifies the fresh target cluster.
	ClusterID string
	// Generation identifies the fresh successor incarnation.
	Generation string
	// HashSlotCount is the immutable physical Hash Slot fence.
	HashSlotCount uint16
	// Empty proves that no application data exists before restore.
	Empty bool
}

// RestoreTargetProbe inspects the real successor before a plan is created.
type RestoreTargetProbe interface {
	InspectRestoreTarget(context.Context) (RestoreTargetState, error)
}

// RestoreInstallNode owns restore-only local database installation methods.
type RestoreInstallNode interface {
	InstallRestoreHashSlotMetadata(
		context.Context, uint16, io.ReadSeeker, int64, bool,
	) (uint64, error)
	InstallRestoreMessageStream(
		context.Context, io.ReadSeeker, int64,
	) (channelstore.BackupSnapshotStats, error)
	ApplyRestorePermanentErasures(
		context.Context, uint16, []clusterpkg.RestorePermanentErasure,
	) error
	InstallRestoreChannelRuntimeMeta(
		context.Context, uint16, []clusterpkg.RestoreVerifyBoundary,
	) error
	RestoreHashSlotMetadataDigest(context.Context, uint16) (string, error)
}

func loadRestorePartitionLayers(
	ctx context.Context,
	repository backupartifact.Repository,
	tip backupartifact.PartitionReference,
) ([]backupartifact.PartitionManifest, error) {
	body, err := readRepositoryObject(
		ctx, repository, tip.Key, tip.Bytes, tip.SHA256,
	)
	if err != nil {
		return nil, err
	}
	baseline, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return nil, err
	}
	if baseline.Cut.HashSlot != tip.HashSlot ||
		uint64(len(baseline.Objects)) != tip.ObjectCount ||
		baseline.Evidence != tip.Evidence {
		return nil, fmt.Errorf(
			"%w: checkpoint baseline summary mismatch",
			backupartifact.ErrInvalidManifest,
		)
	}
	return []backupartifact.PartitionManifest{baseline}, nil
}

type restoreObjectGroup struct {
	Name    string
	Objects []backupartifact.ObjectEntry
}

func restoreObjectGroups(
	objects []backupartifact.ObjectEntry,
	kind backupartifact.ObjectKind,
) ([]restoreObjectGroup, error) {
	groups := make(map[string]map[int]backupartifact.ObjectEntry)
	for _, object := range objects {
		if object.Kind != kind {
			continue
		}
		name, ordinal, err := parseRestoreObjectStreamKey(object.Key)
		if err != nil ||
			(kind == backupartifact.ObjectKindMessages &&
				name != string(kind) &&
				!strings.HasPrefix(name, string(kind)+"-")) {
			return nil, fmt.Errorf(
				"%w: invalid %s stream key %q",
				backupartifact.ErrInvalidManifest, kind, object.Key,
			)
		}
		if groups[name] == nil {
			groups[name] = make(map[int]backupartifact.ObjectEntry)
		}
		if _, exists := groups[name][ordinal]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate stream ordinal",
				backupartifact.ErrInvalidManifest,
			)
		}
		groups[name][ordinal] = object
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]restoreObjectGroup, 0, len(names))
	for _, name := range names {
		ordinals := groups[name]
		entries := make([]backupartifact.ObjectEntry, len(ordinals))
		for ordinal := range entries {
			entry, exists := ordinals[ordinal]
			if !exists {
				return nil, fmt.Errorf(
					"%w: non-contiguous stream ordinals",
					backupartifact.ErrInvalidManifest,
				)
			}
			entries[ordinal] = entry
		}
		result = append(
			result, restoreObjectGroup{Name: name, Objects: entries},
		)
	}
	return result, nil
}

func parseRestoreObjectStreamKey(key string) (string, int, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "objects" {
		return "", 0, fmt.Errorf("invalid object key")
	}
	filename := parts[4]
	if len(filename) < 12 || !strings.HasSuffix(filename, ".bin") ||
		filename[len(filename)-11] != '-' {
		return "", 0, fmt.Errorf("invalid stream filename")
	}
	ordinal, err := strconv.Atoi(
		filename[len(filename)-10 : len(filename)-4],
	)
	if err != nil || ordinal < 0 || ordinal >= maxBackupChunksPerStream {
		return "", 0, fmt.Errorf("invalid stream ordinal")
	}
	name := filename[:len(filename)-11]
	if name == "" {
		return "", 0, fmt.Errorf("missing stream name")
	}
	return name, ordinal, nil
}

func readRepositoryObject(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
	size int64,
	checksum string,
) ([]byte, error) {
	if size <= 0 || size > maxCheckpointRestoreObjectBytes {
		return nil, fmt.Errorf(
			"%w: repository object size is invalid",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: repository object %q: %v",
			backupartifact.ErrRepositoryIncomplete, key, err,
		)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, size+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	hash := sha256.Sum256(body)
	if int64(len(body)) != size || object.Key != key ||
		object.Size != size || object.SHA256 != checksum ||
		hex.EncodeToString(hash[:]) != checksum {
		return nil, fmt.Errorf(
			"%w: repository object %q verification mismatch",
			backupartifact.ErrRepositoryIncomplete, key,
		)
	}
	return body, nil
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
