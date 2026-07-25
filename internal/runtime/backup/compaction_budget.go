package backup

import (
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"
)

const (
	// DefaultGenerationMaxDeltaBytes is the fallback compaction threshold when
	// a materialized baseline does not provide a smaller logical size.
	DefaultGenerationMaxDeltaBytes uint64 = 64 << 30
	// DefaultGenerationMaxSegments bounds one Slot Generation's immutable fanout.
	DefaultGenerationMaxSegments uint64 = 1024
	// DefaultGenerationCompactionConcurrency limits simultaneous Slot materializations per node.
	DefaultGenerationCompactionConcurrency int64 = 2
	// DefaultGenerationCompactionIOBytes bounds estimated in-flight source and repository I/O.
	DefaultGenerationCompactionIOBytes int64 = 1 << 40
	// DefaultGenerationCompactionNetworkBytes bounds estimated in-flight dual-repository traffic.
	DefaultGenerationCompactionNetworkBytes int64 = 2 << 40
)

// DefaultGenerationMaxAge is the default maximum lifetime of one Slot Generation.
const DefaultGenerationMaxAge = 24 * time.Hour

// GenerationCompactionCost is one conservative node-level admission estimate.
type GenerationCompactionCost struct {
	// IOBytes is the conservative source-read plus repository-write total.
	IOBytes int64
	// NetworkBytes is the conservative dual-repository transfer total.
	NetworkBytes int64
}

// GenerationCompactionBudget bounds overlapping Slot baseline materialization.
type GenerationCompactionBudget interface {
	TryAcquire(GenerationCompactionCost) bool
	Release(GenerationCompactionCost)
}

type weightedGenerationCompactionBudget struct {
	concurrency *semaphore.Weighted
	io          *semaphore.Weighted
	network     *semaphore.Weighted
	maxIO       int64
	maxNetwork  int64
}

// NewGenerationCompactionBudget creates one shared non-blocking node budget.
func NewGenerationCompactionBudget(maxConcurrent, maxIOBytes, maxNetworkBytes int64) (GenerationCompactionBudget, error) {
	if maxConcurrent <= 0 || maxIOBytes <= 0 || maxNetworkBytes <= 0 {
		return nil, fmt.Errorf("%w: compaction budget must be positive", ErrInvalidCapture)
	}
	return &weightedGenerationCompactionBudget{
		concurrency: semaphore.NewWeighted(maxConcurrent),
		io:          semaphore.NewWeighted(maxIOBytes),
		network:     semaphore.NewWeighted(maxNetworkBytes),
		maxIO:       maxIOBytes,
		maxNetwork:  maxNetworkBytes,
	}, nil
}

func (b *weightedGenerationCompactionBudget) TryAcquire(cost GenerationCompactionCost) bool {
	if b == nil || cost.IOBytes <= 0 || cost.NetworkBytes <= 0 ||
		!b.concurrency.TryAcquire(1) {
		return false
	}
	ioBytes, networkBytes := b.chargedBytes(cost)
	if !b.io.TryAcquire(ioBytes) {
		b.concurrency.Release(1)
		return false
	}
	if !b.network.TryAcquire(networkBytes) {
		b.io.Release(ioBytes)
		b.concurrency.Release(1)
		return false
	}
	return true
}

func (b *weightedGenerationCompactionBudget) Release(cost GenerationCompactionCost) {
	ioBytes, networkBytes := b.chargedBytes(cost)
	b.network.Release(networkBytes)
	b.io.Release(ioBytes)
	b.concurrency.Release(1)
}

func (b *weightedGenerationCompactionBudget) chargedBytes(
	cost GenerationCompactionCost,
) (int64, int64) {
	return min(cost.IOBytes, b.maxIO), min(cost.NetworkBytes, b.maxNetwork)
}
