package chatlifecycle

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	lifecycleUIDPrefix = "wku-"
	// MaxLifecycleUIDLength is the maximum UID length, including a uint64 index.
	MaxLifecycleUIDLength = len(lifecycleUIDPrefix) + 16 + 1 + 13
)

var (
	errIdentityRunIDRequired = errors.New("chat lifecycle identity: run ID is required")
	errIdentitySeedRequired  = errors.New("chat lifecycle identity: seed must be nonzero")
	errIdentityWorkers       = errors.New("chat lifecycle identity: workers must be positive")
	errIdentityWorkerID      = errors.New("chat lifecycle identity: worker ID is outside the zero-based worker range")
	errIdentityIndexOverflow = errors.New("chat lifecycle identity: global index overflows uint64")
)

// IdentitySpace deterministically partitions a run's collision-free identity
// indexes. Worker IDs are zero-based in the half-open range [0, Workers()).
type IdentitySpace struct {
	rootKey   [sha256.Size]byte
	namespace string
	workers   uint64
}

// NewIdentitySpace validates a run identity and creates its deterministic
// zero-based worker partition. UIDs intentionally do not depend on worker count.
func NewIdentitySpace(runID string, seed, workers uint64) (*IdentitySpace, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errIdentityRunIDRequired
	}
	if seed == 0 {
		return nil, errIdentitySeedRequired
	}
	if workers == 0 {
		return nil, errIdentityWorkers
	}

	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/run-key/v1"))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(len(runID)))
	_, _ = h.Write(encoded[:])
	_, _ = h.Write([]byte(runID))
	binary.BigEndian.PutUint64(encoded[:], seed)
	_, _ = h.Write(encoded[:])
	var rootKey [sha256.Size]byte
	copy(rootKey[:], h.Sum(nil))

	namespaceHash := sha256.Sum256(append([]byte("identity-namespace/v1"), rootKey[:]...))
	namespace := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(namespaceHash[:10])
	return &IdentitySpace{
		rootKey:   rootKey,
		namespace: strings.ToLower(namespace),
		workers:   workers,
	}, nil
}

// Workers returns the number of zero-based worker partitions.
func (s *IdentitySpace) Workers() uint64 { return s.workers }

// GlobalIndex maps a zero-based worker ID and local index onto the monotonically
// interleaved global index sequence, checking both multiplication and addition.
func (s *IdentitySpace) GlobalIndex(workerID, localIndex uint64) (uint64, error) {
	if workerID >= s.workers {
		return 0, errIdentityWorkerID
	}
	if localIndex > (math.MaxUint64-workerID)/s.workers {
		return 0, errIdentityIndexOverflow
	}
	return localIndex*s.workers + workerID, nil
}

// Owner inverts a global index into its zero-based worker ID and local index.
func (s *IdentitySpace) Owner(globalIndex uint64) (workerID, localIndex uint64) {
	return globalIndex % s.workers, globalIndex / s.workers
}

// UID returns a bounded protocol-safe UID whose final base-36 component is the
// exact reversible global index and whose namespace binds the run ID and seed.
func (s *IdentitySpace) UID(globalIndex uint64) string {
	return lifecycleUIDPrefix + s.namespace + "-" + strconv.FormatUint(globalIndex, 36)
}

// IndexFromUID recovers the global index only when uid belongs to this run namespace.
func (s *IdentitySpace) IndexFromUID(uid string) (uint64, bool) {
	prefix := lifecycleUIDPrefix + s.namespace + "-"
	if !strings.HasPrefix(uid, prefix) || len(uid) == len(prefix) {
		return 0, false
	}
	index, err := strconv.ParseUint(uid[len(prefix):], 36, 64)
	return index, err == nil
}

// decisionUint64 derives an independent deterministic decision stream for a
// semantic purpose. Adding another purpose cannot consume or shift this value.
func (s *IdentitySpace) decisionUint64(purpose string, values ...uint64) uint64 {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/decision/v1"))
	_, _ = h.Write(s.rootKey[:])
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(len(purpose)))
	_, _ = h.Write(encoded[:])
	_, _ = h.Write([]byte(purpose))
	for _, value := range values {
		binary.BigEndian.PutUint64(encoded[:], value)
		_, _ = h.Write(encoded[:])
	}
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

func checkedAddIndex(index, offset uint64) (uint64, error) {
	if offset > math.MaxUint64-index {
		return 0, fmt.Errorf("%w: %d + %d", errIdentityIndexOverflow, index, offset)
	}
	return index + offset, nil
}
