package channelmigration

import (
	"time"

	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// PhaseTransition describes one durable executor phase or status update.
type PhaseTransition struct {
	// TaskID identifies the migration task that was updated.
	TaskID string
	// ChannelID identifies the channel whose task was updated.
	ChannelID string
	// FromStatus is the task status before the update.
	FromStatus slotmeta.ChannelMigrationStatus
	// ToStatus is the task status after the update.
	ToStatus slotmeta.ChannelMigrationStatus
	// FromPhase is the executor phase before the update.
	FromPhase slotmeta.ChannelMigrationPhase
	// ToPhase is the executor phase after the update.
	ToPhase slotmeta.ChannelMigrationPhase
}

// Metrics records executor activity without coupling the runtime to a metrics backend.
type Metrics interface {
	// RecordActiveTasks records the number of tasks observed in one scan.
	RecordActiveTasks(active int)
	// RecordPhaseTransition records a durable phase/status update; phases may match for retry bookkeeping.
	RecordPhaseTransition(transition PhaseTransition)
	// RecordRetry records a retryable task error.
	RecordRetry(task Task, err error)
	// RecordBlocker records a stable blocker code for operator visibility.
	RecordBlocker(task Task, code string)
	// RecordFenceDuration records how long a task-owned write fence has been held.
	RecordFenceDuration(task Task, duration time.Duration)
	// RecordTaskDuration records total task age once it is observed.
	RecordTaskDuration(task Task, duration time.Duration)
	// RecordGarbageCollection records the number of terminal tasks removed in one cleanup pass.
	RecordGarbageCollection(deleted int)
}

type noopMetrics struct{}

func (noopMetrics) RecordActiveTasks(active int) {}

func (noopMetrics) RecordPhaseTransition(transition PhaseTransition) {}

func (noopMetrics) RecordRetry(task Task, err error) {}

func (noopMetrics) RecordBlocker(task Task, code string) {}

func (noopMetrics) RecordFenceDuration(task Task, duration time.Duration) {}

func (noopMetrics) RecordTaskDuration(task Task, duration time.Duration) {}

func (noopMetrics) RecordGarbageCollection(deleted int) {}
