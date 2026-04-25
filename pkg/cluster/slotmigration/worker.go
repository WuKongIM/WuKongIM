package slotmigration

import (
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type Phase uint8

const (
	PhaseSnapshot Phase = iota
	PhaseDelta
	PhaseSwitching
	PhaseDone
)

const (
	maxConcurrentMigrations          = 4
	maxConcurrentMigrationsPerSource = 2
	defaultMigrationStallTimeout     = 10 * time.Minute
)

type Worker struct {
	stableThreshold int64
	stableWindow    time.Duration
	stallTimeout    time.Duration
	migrations      map[uint16]*activeMigration
	now             func() time.Time
}

type Migration struct {
	HashSlot uint16
	Source   multiraft.SlotID
	Target   multiraft.SlotID
	Phase    Phase
}

type Transition struct {
	HashSlot uint16
	Source   multiraft.SlotID
	Target   multiraft.SlotID
	From     Phase
	To       Phase
	TimedOut bool
}

type activeMigration struct {
	hashSlot       uint16
	source         multiraft.SlotID
	target         multiraft.SlotID
	phase          Phase
	snapshotAt     uint64
	switchReady    bool
	lastActivityAt time.Time
	progress       *Progress
}

func NewWorker(stableThreshold int64, stableWindow time.Duration) *Worker {
	return &Worker{
		stableThreshold: stableThreshold,
		stableWindow:    stableWindow,
		stallTimeout:    defaultMigrationStallTimeout,
		migrations:      make(map[uint16]*activeMigration),
		now:             time.Now,
	}
}

func (w *Worker) StartMigration(hashSlot uint16, source, target multiraft.SlotID) error {
	if w == nil || hashSlot == 0 || source == 0 || target == 0 || source == target {
		return errors.New("slotmigration: invalid migration")
	}
	w.ensureState()
	if existing, exists := w.migrations[hashSlot]; exists {
		if existing.source == source && existing.target == target {
			return nil
		}
		return errors.New("slotmigration: migration already exists")
	}
	if len(w.migrations) >= maxConcurrentMigrations {
		return nil
	}
	if w.countActiveFromSource(source) >= maxConcurrentMigrationsPerSource {
		return nil
	}
	w.migrations[hashSlot] = &activeMigration{
		hashSlot:       hashSlot,
		source:         source,
		target:         target,
		phase:          PhaseSnapshot,
		lastActivityAt: w.now(),
		progress:       &Progress{},
	}
	return nil
}

func (w *Worker) MarkSnapshotComplete(hashSlot uint16, sourceApplyIndex uint64, bytesTransferred int64) error {
	migration, err := w.lookup(hashSlot)
	if err != nil {
		return err
	}
	migration.snapshotAt = sourceApplyIndex
	migration.lastActivityAt = w.now()
	migration.progress.Update(sourceApplyIndex, sourceApplyIndex, bytesTransferred, w.stableThreshold, w.now())
	return nil
}

func (w *Worker) UpdateProgress(hashSlot uint16, sourceApplyIndex, targetApplyIndex uint64, bytesTransferred int64) error {
	migration, err := w.lookup(hashSlot)
	if err != nil {
		return err
	}
	migration.progress.Update(sourceApplyIndex, targetApplyIndex, bytesTransferred, w.stableThreshold, w.now())
	migration.lastActivityAt = w.now()
	return nil
}

func (w *Worker) MarkSwitchComplete(hashSlot uint16) error {
	migration, err := w.lookup(hashSlot)
	if err != nil {
		return err
	}
	migration.switchReady = true
	migration.lastActivityAt = w.now()
	return nil
}

func (w *Worker) ActiveMigrations() []Migration {
	if w == nil || len(w.migrations) == 0 {
		return nil
	}

	hashSlots := make([]uint16, 0, len(w.migrations))
	for hashSlot := range w.migrations {
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool {
		return hashSlots[i] < hashSlots[j]
	})

	out := make([]Migration, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		migration := w.migrations[hashSlot]
		if migration == nil {
			continue
		}
		out = append(out, Migration{
			HashSlot: migration.hashSlot,
			Source:   migration.source,
			Target:   migration.target,
			Phase:    migration.phase,
		})
	}
	return out
}

func (w *Worker) AbortMigration(hashSlot uint16) error {
	if w == nil {
		return errors.New("slotmigration: nil worker")
	}
	w.ensureState()
	w.removeMigration(hashSlot)
	return nil
}

func (w *Worker) Tick() []Transition {
	if w == nil || len(w.migrations) == 0 {
		return nil
	}

	hashSlots := make([]uint16, 0, len(w.migrations))
	for hashSlot := range w.migrations {
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool {
		return hashSlots[i] < hashSlots[j]
	})

	transitions := make([]Transition, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		migration := w.migrations[hashSlot]
		if migration == nil {
			continue
		}
		transition, remove, ok := w.tickMigration(migration)
		if remove {
			w.removeMigration(hashSlot)
		}
		if ok {
			transitions = append(transitions, transition)
		}
	}
	return transitions
}

func (w *Worker) tickMigration(m *activeMigration) (Transition, bool, bool) {
	from := m.phase
	switch m.phase {
	case PhaseDone:
		return Transition{}, true, false
	}
	if w.stallTimeout <= 0 {
		w.stallTimeout = defaultMigrationStallTimeout
	}
	if !m.lastActivityAt.IsZero() && w.now().Sub(m.lastActivityAt) > w.stallTimeout {
		return Transition{
			HashSlot: m.hashSlot,
			Source:   m.source,
			Target:   m.target,
			From:     from,
			To:       from,
			TimedOut: true,
		}, true, true
	}
	switch m.phase {
	case PhaseSnapshot:
		if m.snapshotAt != 0 {
			m.phase = PhaseDelta
		}
	case PhaseDelta:
		if m.progress != nil && m.progress.isStableAt(w.stableThreshold, w.stableWindow, w.now()) {
			m.phase = PhaseSwitching
		}
	case PhaseSwitching:
		if m.switchReady {
			m.phase = PhaseDone
		}
	}
	if m.phase == from {
		return Transition{}, false, false
	}
	return Transition{
		HashSlot: m.hashSlot,
		Source:   m.source,
		Target:   m.target,
		From:     from,
		To:       m.phase,
	}, false, true
}

func (w *Worker) lookup(hashSlot uint16) (*activeMigration, error) {
	if w == nil {
		return nil, errors.New("slotmigration: nil worker")
	}
	migration, ok := w.migrations[hashSlot]
	if !ok || migration == nil {
		return nil, errors.New("slotmigration: migration not found")
	}
	return migration, nil
}

func (w *Worker) ensureState() {
	if w == nil {
		return
	}
	if w.migrations == nil {
		w.migrations = make(map[uint16]*activeMigration)
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.stallTimeout <= 0 {
		w.stallTimeout = defaultMigrationStallTimeout
	}
}

func (w *Worker) countActiveFromSource(source multiraft.SlotID) int {
	if w == nil || len(w.migrations) == 0 {
		return 0
	}
	count := 0
	for _, migration := range w.migrations {
		if migration != nil && migration.source == source {
			count++
		}
	}
	return count
}

func (w *Worker) removeMigration(hashSlot uint16) {
	if w == nil || len(w.migrations) == 0 {
		return
	}
	delete(w.migrations, hashSlot)
}
