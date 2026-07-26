package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// CheckpointRestoreStagingQuota coordinates the one node-local restore
// staging budget across source downloads, target scratch, and replica
// transfers. Reservations cover bytes that have been admitted but are not yet
// visible to a filesystem scan.
type CheckpointRestoreStagingQuota struct {
	// root is the canonical node-local staging root shared by all restore work.
	root string
	// maxBytes is the hard aggregate byte ceiling below root.
	maxBytes uint64

	// mu serializes filesystem refreshes and claim admission.
	mu sync.Mutex
	// used excludes bytes beneath active claim paths.
	used uint64
	// claims charge admitted peak capacity by stable owner identity.
	claims map[string]checkpointRestoreStagingClaim
	// attemptLocks serialize target and receiver access to the same semantic
	// attempt path while allowing independent Slots to progress concurrently.
	attemptLocks checkpointRestoreAttemptLocks
	// sizePath measures a bounded claim path during normal admission and the
	// complete root only during startup or explicit validation.
	sizePath func(string, string) (uint64, error)
}

type checkpointRestoreStagingClaim struct {
	// path contains every byte that this claim may create while active.
	path string
	// capacity is the admitted peak size of path, including temporary files.
	capacity uint64
}

type checkpointRestoreAttemptLocks struct {
	// mu protects the keyed lock registry and waiter reference counts.
	mu    sync.Mutex
	locks map[string]*checkpointRestoreAttemptLock
}

type checkpointRestoreAttemptLock struct {
	// mu serializes one semantic restore attempt without blocking other Slots.
	mu sync.Mutex
	// refs counts the holder and waiters so registry removal is race-free.
	refs uint64
}

// lock acquires one attempt-scoped mutex and returns its release function.
func (l *checkpointRestoreAttemptLocks) lock(key string) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*checkpointRestoreAttemptLock)
	}
	entry := l.locks[key]
	if entry == nil {
		entry = &checkpointRestoreAttemptLock{}
		l.locks[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}

// NewCheckpointRestoreStagingQuota creates one shared node-local quota and
// removes crash-orphaned transient source download files before admitting
// restore work.
func NewCheckpointRestoreStagingQuota(
	root string,
	maxBytes uint64,
) (*CheckpointRestoreStagingQuota, error) {
	if strings.TrimSpace(root) == "" || maxBytes == 0 {
		return nil, fmt.Errorf(
			"backup checkpoint restore staging quota: invalid options",
		)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	quota := &CheckpointRestoreStagingQuota{
		root: resolved, maxBytes: maxBytes,
		claims:   make(map[string]checkpointRestoreStagingClaim),
		sizePath: checkpointRestoreStagingBytes,
	}
	if err := quota.removeOrphanedSourceFiles(); err != nil {
		return nil, err
	}
	if err := quota.refresh(); err != nil {
		return nil, err
	}
	return quota, nil
}

// reserveClaim atomically admits a path's peak capacity against the shared
// node budget before a producer creates or replaces any bytes beneath it.
func (q *CheckpointRestoreStagingQuota) reserveClaim(
	owner string,
	path string,
	capacity uint64,
) error {
	if q == nil || strings.TrimSpace(owner) == "" || capacity == 0 ||
		!q.contains(path) {
		return fmt.Errorf(
			"backup checkpoint restore staging quota: invalid claim",
		)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	cleanPath := filepath.Clean(path)
	previous, exists := q.claims[owner]
	if exists && previous.path != cleanPath {
		return fmt.Errorf(
			"%w: checkpoint restore staging claim path conflicts",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if !exists {
		for existingOwner, claim := range q.claims {
			if existingOwner != owner &&
				checkpointRestorePathsOverlap(cleanPath, claim.path) {
				return fmt.Errorf(
					"%w: checkpoint restore staging claims overlap",
					backupartifact.ErrObjectCorrupt,
				)
			}
		}
	}
	pathBytes, err := q.sizePath(cleanPath, "")
	if errors.Is(err, os.ErrNotExist) {
		pathBytes, err = 0, nil
	}
	if err != nil {
		return err
	}
	if pathBytes > capacity {
		return fmt.Errorf(
			"%w: checkpoint restore staging claim exceeded",
			backupartifact.ErrInvalidObject,
		)
	}
	if !exists {
		if pathBytes > q.used {
			return backupartifact.ErrObjectCorrupt
		}
		q.used -= pathBytes
	}
	q.claims[owner] = checkpointRestoreStagingClaim{
		path: cleanPath, capacity: capacity,
	}
	if err := q.validateLocked(); err != nil {
		if exists {
			q.claims[owner] = previous
		} else {
			delete(q.claims, owner)
			q.used += pathBytes
		}
		return err
	}
	return nil
}

// settleClaim converts one active path claim back into scanned committed bytes.
func (q *CheckpointRestoreStagingQuota) settleClaim(owner string) error {
	if q == nil || strings.TrimSpace(owner) == "" {
		return fmt.Errorf(
			"backup checkpoint restore staging quota: invalid claim",
		)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.claims[owner]; !exists {
		return nil
	}
	claim := q.claims[owner]
	pathBytes, err := q.sizePath(claim.path, "")
	if errors.Is(err, os.ErrNotExist) {
		pathBytes, err = 0, nil
	}
	if err != nil {
		return err
	}
	if pathBytes > claim.capacity ||
		pathBytes > ^uint64(0)-q.used {
		return fmt.Errorf(
			"%w: checkpoint restore staging claim exceeded",
			backupartifact.ErrInvalidObject,
		)
	}
	delete(q.claims, owner)
	q.used += pathBytes
	return q.validateLocked()
}

// removeCommittedPath deletes one settled file and decrements cached usage in
// the same quota critical section as explicit validation.
func (q *CheckpointRestoreStagingQuota) removeCommittedPath(
	path string,
	expectedBytes uint64,
) error {
	if expectedBytes == 0 {
		return fmt.Errorf(
			"backup checkpoint restore staging quota: invalid committed path",
		)
	}
	return q.removeTrackedPath(path, &expectedBytes)
}

// removeTrackedPath deletes one quota-owned file or subtree. Bytes covered by
// an active parent claim remain charged to that claim; settled bytes decrement
// cached usage atomically with deletion.
func (q *CheckpointRestoreStagingQuota) removeTrackedPath(
	path string,
	expectedBytes *uint64,
) error {
	if q == nil || !q.contains(path) {
		return fmt.Errorf(
			"backup checkpoint restore staging quota: invalid tracked path",
		)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	cleanPath := filepath.Clean(path)
	coveredByClaim := false
	for _, claim := range q.claims {
		if checkpointRestorePathContains(cleanPath, claim.path) &&
			cleanPath != claim.path {
			return fmt.Errorf(
				"%w: checkpoint restore deletion contains an active claim",
				backupartifact.ErrObjectCorrupt,
			)
		}
		if checkpointRestorePathContains(claim.path, cleanPath) {
			coveredByClaim = true
		}
	}
	info, err := os.Lstat(cleanPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pathBytes, err := q.sizePath(cleanPath, "")
	if err != nil {
		return err
	}
	if expectedBytes != nil && pathBytes != *expectedBytes {
		return backupartifact.ErrObjectCorrupt
	}
	if !coveredByClaim && pathBytes > q.used {
		return backupartifact.ErrObjectCorrupt
	}
	if info.IsDir() {
		err = os.RemoveAll(cleanPath)
	} else {
		err = os.Remove(cleanPath)
	}
	if err != nil {
		return err
	}
	if !coveredByClaim {
		q.used -= pathBytes
	}
	return q.validateLocked()
}

func (q *CheckpointRestoreStagingQuota) validate() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.refreshLocked(); err != nil {
		return err
	}
	return q.validateLocked()
}

func (q *CheckpointRestoreStagingQuota) refresh() error {
	if q == nil {
		return fmt.Errorf(
			"backup checkpoint restore staging quota: unavailable",
		)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.refreshLocked(); err != nil {
		return err
	}
	return q.validateLocked()
}

// refreshLocked recomputes committed bytes while subtracting active claim
// paths, whose full admitted capacities are accounted separately.
func (q *CheckpointRestoreStagingQuota) refreshLocked() error {
	used, err := q.sizePath(q.root, "")
	if err != nil {
		return err
	}
	for _, claim := range q.claims {
		claimedBytes, err := q.sizePath(claim.path, "")
		if errors.Is(err, os.ErrNotExist) {
			claimedBytes, err = 0, nil
		}
		if err != nil {
			return err
		}
		if claimedBytes > claim.capacity {
			return fmt.Errorf(
				"%w: checkpoint restore staging claim exceeded",
				backupartifact.ErrInvalidObject,
			)
		}
		if claimedBytes > used {
			return backupartifact.ErrObjectCorrupt
		}
		used -= claimedBytes
	}
	q.used = used
	return nil
}

func checkpointRestorePathsOverlap(left string, right string) bool {
	return checkpointRestorePathContains(left, right) ||
		checkpointRestorePathContains(right, left)
}

func checkpointRestorePathContains(parent string, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (q *CheckpointRestoreStagingQuota) validateLocked() error {
	claimed, err := q.claimedCapacityLocked()
	if err != nil {
		return err
	}
	if q.used > q.maxBytes ||
		claimed > q.maxBytes-q.used {
		return fmt.Errorf(
			"%w: checkpoint restore node staging quota exceeded",
			backupartifact.ErrInvalidObject,
		)
	}
	return nil
}

func (q *CheckpointRestoreStagingQuota) claimedCapacityLocked() (
	uint64,
	error,
) {
	var total uint64
	for _, claim := range q.claims {
		if claim.capacity > ^uint64(0)-total {
			return 0, backupartifact.ErrInvalidObject
		}
		total += claim.capacity
	}
	return total, nil
}

func (q *CheckpointRestoreStagingQuota) contains(path string) bool {
	if q == nil {
		return false
	}
	relative, err := filepath.Rel(q.root, filepath.Clean(path))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (q *CheckpointRestoreStagingQuota) removeOrphanedSourceFiles() error {
	return filepath.WalkDir(
		q.root,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if (strings.HasPrefix(name, "checkpoint-segment-") ||
				strings.HasPrefix(name, "checkpoint-baseline-")) &&
				strings.HasSuffix(name, ".stage") {
				return os.Remove(path)
			}
			return nil
		},
	)
}
