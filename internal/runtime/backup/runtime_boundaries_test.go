package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFullExportConstructorsAndTargetsFailClosed(t *testing.T) {
	store := newRuntimeArchiveStore()
	source := &runtimeSlotSource{capture: validRuntimeCapture()}
	for _, options := range []FullExporterOptions{
		{},
		{Store: store, TempDir: t.TempDir()},
		{Source: source, TempDir: t.TempDir()},
		{Store: store, Source: source},
	} {
		if _, err := NewFullExporter(options); err == nil {
			t.Fatalf("NewFullExporter(%+v) error = nil", options)
		}
	}
	if _, err := NewFullExporter(FullExporterOptions{Store: store, Source: source, TempDir: t.TempDir()}); err != nil {
		t.Fatalf("NewFullExporter(valid) error = %v", err)
	}

	for _, options := range []FullStreamWriterOptions{{}, {Store: store}, {TempDir: t.TempDir()}} {
		if _, err := NewFullStreamWriter(options); err == nil {
			t.Fatalf("NewFullStreamWriter(%+v) error = nil", options)
		}
	}
	writer, err := NewFullStreamWriter(FullStreamWriterOptions{Store: store, TempDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewFullStreamWriter(valid) error = %v", err)
	}
	references, err := writer.Write(context.Background(), "backup-1", 7, FullSlotStream{
		Kind: backupartifact.ChunkKindMetadata, Reader: io.NopCloser(strings.NewReader("metadata")), Records: 1,
	}, 1, 0)
	if err != nil || len(references) != 1 || !references[0].Final || references[0].Records != 1 {
		t.Fatalf("Write(valid) = (%#v, %v)", references, err)
	}

	for _, tt := range []struct {
		name     string
		backupID string
		slot     uint16
	}{
		{name: "empty ID", slot: 0},
		{name: "path ID", backupID: "../escape", slot: 0},
		{name: "out of range Slot", backupID: "backup-1", slot: backupartifact.DefaultHashSlotCount},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateFullExportTarget(tt.backupID, tt.slot); err == nil {
				t.Fatal("validateFullExportTarget() error = nil")
			}
		})
	}
}

func TestFullStreamWriterClosesRejectedStreams(t *testing.T) {
	validStore := newRuntimeArchiveStore()
	tests := []struct {
		name   string
		writer *FullStreamWriter
		id     string
		slot   uint16
		prefix string
		kind   backupartifact.ChunkKind
		seq    uint32
	}{
		{name: "nil writer", id: "backup-1", prefix: "slots/000", kind: backupartifact.ChunkKindMetadata, seq: 1},
		{name: "missing sequence", writer: &FullStreamWriter{store: validStore, tempDir: t.TempDir()}, id: "backup-1", prefix: "slots/000", kind: backupartifact.ChunkKindMetadata},
		{name: "invalid kind", writer: &FullStreamWriter{store: validStore, tempDir: t.TempDir()}, id: "backup-1", prefix: "slots/000", kind: backupartifact.ChunkKind("other"), seq: 1},
		{name: "invalid backup", writer: &FullStreamWriter{store: validStore, tempDir: t.TempDir()}, id: "../escape", prefix: "slots/000", kind: backupartifact.ChunkKindMetadata, seq: 1},
		{name: "wrong Slot prefix", writer: &FullStreamWriter{store: validStore, tempDir: t.TempDir()}, id: "backup-1", prefix: "slots/001", kind: backupartifact.ChunkKindMetadata, seq: 1},
		{name: "invalid attempt prefix", writer: &FullStreamWriter{store: validStore, tempDir: t.TempDir()}, id: "backup-1", prefix: "slots/000/attempts/../escape", kind: backupartifact.ChunkKindMetadata, seq: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &closeTrackingReader{Reader: strings.NewReader("payload")}
			_, err := tt.writer.WriteAt(context.Background(), tt.id, tt.slot, tt.prefix, FullSlotStream{Kind: tt.kind, Reader: reader}, tt.seq, 0)
			if err == nil {
				t.Fatal("WriteAt() error = nil")
			}
			if !reader.closed {
				t.Fatal("rejected stream was not closed")
			}
		})
	}
}

func TestFullExporterRejectsInvalidCaptureBeforePublishingManifest(t *testing.T) {
	wantErr := errors.New("injected failure")
	tests := []struct {
		name    string
		store   *runtimeArchiveStore
		source  *runtimeSlotSource
		wantSub string
	}{
		{name: "delete failure", store: &runtimeArchiveStore{objects: map[string][]byte{}, deletePrefixErr: wantErr}, source: &runtimeSlotSource{capture: validRuntimeCapture()}, wantSub: wantErr.Error()},
		{name: "open failure", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{err: wantErr}, wantSub: wantErr.Error()},
		{name: "invalid cut", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{capture: &runtimeSlotCapture{}}, wantSub: "invalid Hash Slot cut"},
		{name: "missing metadata", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{capture: &runtimeSlotCapture{cut: validRuntimeCut()}}, wantSub: "metadata stream is required"},
		{name: "messages before metadata", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{capture: &runtimeSlotCapture{cut: validRuntimeCut(), streams: []FullSlotStream{{Kind: backupartifact.ChunkKindMessages, Reader: io.NopCloser(strings.NewReader("message"))}}}}, wantSub: "invalid Slot stream order"},
		{name: "duplicate metadata", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{capture: &runtimeSlotCapture{cut: validRuntimeCut(), streams: []FullSlotStream{
			{Kind: backupartifact.ChunkKindMetadata, Reader: io.NopCloser(strings.NewReader("one"))},
			{Kind: backupartifact.ChunkKindMetadata, Reader: io.NopCloser(strings.NewReader("two"))},
		}}}, wantSub: "invalid Slot stream order"},
		{name: "capture iteration failure", store: newRuntimeArchiveStore(), source: &runtimeSlotSource{capture: &runtimeSlotCapture{cut: validRuntimeCut(), nextErr: wantErr}}, wantSub: wantErr.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter, err := NewFullExporter(FullExporterOptions{Store: tt.store, Source: tt.source, TempDir: t.TempDir()})
			if err != nil {
				t.Fatalf("NewFullExporter() error = %v", err)
			}
			_, err = exporter.ExportSlot(context.Background(), "backup-1", 0)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("ExportSlot() error = %v, want %q", err, tt.wantSub)
			}
			if body := tt.store.objects["backups/backup-1/slots/000/manifest.json"]; body != nil {
				t.Fatal("invalid capture published a Slot manifest")
			}
		})
	}
}

func TestScheduledRuntimeLifecycleAndOptionValidation(t *testing.T) {
	deps := newScheduledRuntimeDeps()
	valid := ScheduledRuntimeOptions{Scheduled: deps, State: deps, Runner: deps.runner, Restore: deps.restore, Leadership: deps, Tick: time.Second}
	invalid := []ScheduledRuntimeOptions{
		{},
		{State: deps, Runner: deps.runner, Restore: deps.restore, Leadership: deps, Tick: time.Second},
		{Scheduled: deps, Runner: deps.runner, Restore: deps.restore, Leadership: deps, Tick: time.Second},
		{Scheduled: deps, State: deps, Restore: deps.restore, Leadership: deps, Tick: time.Second},
		{Scheduled: deps, State: deps, Runner: deps.runner, Leadership: deps, Tick: time.Second},
		{Scheduled: deps, State: deps, Runner: deps.runner, Restore: deps.restore, Tick: time.Second},
	}
	for _, options := range invalid {
		if _, err := NewScheduledRuntime(options); err == nil {
			t.Fatal("NewScheduledRuntime(invalid) error = nil")
		}
	}
	zeroNode := *deps
	zeroNode.nodeID = 0
	options := valid
	options.Scheduled, options.State, options.Leadership = &zeroNode, &zeroNode, &zeroNode
	if _, err := NewScheduledRuntime(options); err == nil {
		t.Fatal("NewScheduledRuntime(zero node) error = nil")
	}
	for _, tick := range []time.Duration{time.Millisecond, time.Minute + time.Second} {
		options = valid
		options.Tick = tick
		if _, err := NewScheduledRuntime(options); err == nil {
			t.Fatalf("NewScheduledRuntime(tick=%v) error = nil", tick)
		}
	}
	options = valid
	options.Tick = 0
	runtime, err := NewScheduledRuntime(options)
	if err != nil || runtime.options.Tick != 10*time.Second {
		t.Fatalf("NewScheduledRuntime(default tick) = (%v, %v)", runtime, err)
	}

	var nilRuntime *ScheduledRuntime
	if err := nilRuntime.Start(context.Background()); err == nil {
		t.Fatal("nil ScheduledRuntime.Start() error = nil")
	}
	if err := nilRuntime.Stop(context.Background()); err != nil {
		t.Fatalf("nil ScheduledRuntime.Stop() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Start(canceled); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Start(canceled); err != nil {
		t.Fatalf("repeated Start() error = %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("repeated Stop() error = %v", err)
	}

	blockedContext, blockedCancel := context.WithCancel(context.Background())
	blocked := &ScheduledRuntime{cancel: blockedCancel, done: make(chan struct{})}
	stopContext, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	if err := blocked.Stop(stopContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop(canceled) error = %v, want context.Canceled", err)
	}
	_ = blockedContext
}

func TestScheduledRuntimeAdvanceHonorsLeaderFenceAndStageOrder(t *testing.T) {
	wantErr := errors.New("stage failed")
	tests := []struct {
		name        string
		configure   func(*scheduledRuntimeDeps)
		cancelInput bool
		wantActive  bool
		wantErrors  int
		wantCalls   string
	}{
		{name: "canceled", cancelInput: true},
		{name: "not leader", configure: func(d *scheduledRuntimeDeps) { d.leaderID = 2 }},
		{name: "fence error", configure: func(d *scheduledRuntimeDeps) { d.fenceErr = wantErr }, wantErrors: 1, wantCalls: "fence"},
		{name: "stale fence", configure: func(d *scheduledRuntimeDeps) { d.fenceLeader = 2 }, wantCalls: "fence"},
		{name: "zero fence term", configure: func(d *scheduledRuntimeDeps) { d.fenceTerm = 0 }, wantCalls: "fence"},
		{name: "state error", configure: func(d *scheduledRuntimeDeps) { d.stateErr = wantErr }, wantErrors: 1, wantCalls: "fence,state"},
		{name: "restore error", configure: func(d *scheduledRuntimeDeps) { d.restore.err = wantErr }, wantErrors: 1, wantCalls: "fence,state,restore"},
		{name: "restore remains active", configure: func(d *scheduledRuntimeDeps) { d.restore.active = true }, wantActive: true, wantCalls: "fence,state,restore"},
		{name: "schedule error", configure: func(d *scheduledRuntimeDeps) { d.scheduleErr = wantErr }, wantErrors: 1, wantCalls: "fence,state,restore,schedule"},
		{name: "backup active", configure: func(d *scheduledRuntimeDeps) { d.runner.active = true }, wantActive: true, wantCalls: "fence,state,restore,schedule,backup"},
		{name: "backup error", configure: func(d *scheduledRuntimeDeps) { d.runner.err = wantErr }, wantErrors: 1, wantCalls: "fence,state,restore,schedule,backup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newScheduledRuntimeDeps()
			if tt.configure != nil {
				tt.configure(deps)
			}
			runtime := &ScheduledRuntime{options: ScheduledRuntimeOptions{
				Scheduled: deps, State: deps, Runner: deps.runner, Restore: deps.restore, Leadership: deps,
				Tick: time.Second, OnError: func(err error) { deps.errors = append(deps.errors, err) },
			}}
			ctx := context.Background()
			if tt.cancelInput {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if got := runtime.advance(ctx); got != tt.wantActive {
				t.Fatalf("advance() = %v, want %v", got, tt.wantActive)
			}
			if len(deps.errors) != tt.wantErrors {
				t.Fatalf("reported errors = %v, want %d", deps.errors, tt.wantErrors)
			}
			if got := strings.Join(deps.calls, ","); got != tt.wantCalls {
				t.Fatalf("stage calls = %q, want %q", got, tt.wantCalls)
			}
			if strings.Contains(tt.wantCalls, "state") && deps.fenceSeen != (backupcontract.CoordinatorFence{NodeID: 1, Term: 7}) {
				t.Fatalf("coordinator fence = %+v", deps.fenceSeen)
			}
		})
	}
}

func TestCancellationDetectionRequiresSameActiveJobTransition(t *testing.T) {
	backupBefore := backupcontract.SystemState{ActiveBackup: &backupcontract.BackupJob{ID: "b1"}}
	backupAfter := backupBefore.Clone()
	backupAfter.ActiveBackup.CancelRequested = true
	if !cancellationBecameRequested(backupBefore, backupAfter) {
		t.Fatal("new backup cancellation was not detected")
	}
	restoreBefore := backupcontract.SystemState{ActiveRestore: &backupcontract.RestoreJob{ID: "r1"}}
	restoreAfter := restoreBefore.Clone()
	restoreAfter.ActiveRestore.CancelRequested = true
	if !cancellationBecameRequested(restoreBefore, restoreAfter) {
		t.Fatal("new restore cancellation was not detected")
	}
	for _, states := range [][2]backupcontract.SystemState{
		{{}, {}},
		{backupBefore, {ActiveBackup: &backupcontract.BackupJob{ID: "b2", CancelRequested: true}}},
		{backupAfter, backupAfter},
		{restoreBefore, {ActiveRestore: &backupcontract.RestoreJob{ID: "r2", CancelRequested: true}}},
		{restoreAfter, restoreAfter},
	} {
		if cancellationBecameRequested(states[0], states[1]) {
			t.Fatalf("unrelated state transition reported cancellation: %#v -> %#v", states[0], states[1])
		}
	}
}

type closeTrackingReader struct {
	io.Reader
	closed bool
}

func (r *closeTrackingReader) Close() error { r.closed = true; return nil }

type runtimeArchiveStore struct {
	mu              sync.Mutex
	objects         map[string][]byte
	deletePrefixErr error
}

func newRuntimeArchiveStore() *runtimeArchiveStore {
	return &runtimeArchiveStore{objects: map[string][]byte{}}
}

func (s *runtimeArchiveStore) Put(_ context.Context, object backupartifact.PutObject) error {
	body, err := io.ReadAll(object.Body)
	if err != nil {
		return err
	}
	if uint64(len(body)) != object.ExpectedBytes {
		return errors.New("size mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if object.IfAbsent {
		if _, ok := s.objects[object.Key]; ok {
			return backupartifact.ErrObjectExists
		}
	}
	s.objects[object.Key] = append([]byte(nil), body...)
	return nil
}

func (s *runtimeArchiveStore) Open(_ context.Context, key string) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, backupartifact.ArchiveObject{}, backupartifact.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), backupartifact.ArchiveObject{Key: key, Bytes: uint64(len(body))}, nil
}

func (s *runtimeArchiveStore) List(_ context.Context, prefix string) ([]backupartifact.ArchiveObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := make([]backupartifact.ArchiveObject, 0)
	for key, body := range s.objects {
		if strings.HasPrefix(key, prefix) {
			objects = append(objects, backupartifact.ArchiveObject{Key: key, Bytes: uint64(len(body))})
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return objects, nil
}

func (s *runtimeArchiveStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *runtimeArchiveStore) DeletePrefix(_ context.Context, prefix string) error {
	if s.deletePrefixErr != nil {
		return s.deletePrefixErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
		}
	}
	return nil
}

type runtimeSlotSource struct {
	capture *runtimeSlotCapture
	err     error
}

func (s *runtimeSlotSource) OpenFullSlot(context.Context, uint16) (FullSlotCapture, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.capture, nil
}

type runtimeSlotCapture struct {
	cut     backupartifact.SlotCut
	streams []FullSlotStream
	nextErr error
	index   int
}

func validRuntimeCut() backupartifact.SlotCut {
	return backupartifact.SlotCut{PhysicalSlotID: 1, LeaderTerm: 2, AppliedTerm: 2, ConfigurationVersion: 3, AppliedIndex: 4, CapturedAtUnixMillis: 1_800_000_000_000}
}

func validRuntimeCapture() *runtimeSlotCapture {
	return &runtimeSlotCapture{cut: validRuntimeCut(), streams: []FullSlotStream{{Kind: backupartifact.ChunkKindMetadata, Reader: io.NopCloser(strings.NewReader("metadata"))}}}
}

func (c *runtimeSlotCapture) Cut() backupartifact.SlotCut { return c.cut }
func (c *runtimeSlotCapture) Next(context.Context) (FullSlotStream, error) {
	if c.index < len(c.streams) {
		stream := c.streams[c.index]
		c.index++
		return stream, nil
	}
	if c.nextErr != nil {
		err := c.nextErr
		c.nextErr = nil
		return FullSlotStream{}, err
	}
	return FullSlotStream{}, io.EOF
}
func (*runtimeSlotCapture) Close() error { return nil }

type scheduledRuntimeDeps struct {
	nodeID      uint64
	leaderID    uint64
	fenceLeader uint64
	fenceTerm   uint64
	fenceErr    error
	state       backupcontract.SystemState
	stateErr    error
	scheduleErr error
	runner      *scheduledRunner
	restore     *scheduledRunner
	calls       []string
	errors      []error
	fenceSeen   backupcontract.CoordinatorFence
}

func newScheduledRuntimeDeps() *scheduledRuntimeDeps {
	d := &scheduledRuntimeDeps{nodeID: 1, leaderID: 1, fenceLeader: 1, fenceTerm: 7}
	d.runner = &scheduledRunner{name: "backup", owner: d}
	d.restore = &scheduledRunner{name: "restore", owner: d}
	return d
}

func (d *scheduledRuntimeDeps) NodeID() uint64                   { return d.nodeID }
func (d *scheduledRuntimeDeps) BackupControllerLeaderID() uint64 { return d.leaderID }
func (d *scheduledRuntimeDeps) BackupControllerFence(context.Context) (uint64, uint64, error) {
	d.calls = append(d.calls, "fence")
	return d.fenceLeader, d.fenceTerm, d.fenceErr
}
func (d *scheduledRuntimeDeps) State(ctx context.Context) (backupcontract.SystemState, error) {
	d.calls = append(d.calls, "state")
	d.fenceSeen, _ = backupcontract.CoordinatorFenceFromContext(ctx)
	return d.state.Clone(), d.stateErr
}
func (d *scheduledRuntimeDeps) Evaluate(context.Context, time.Duration) error {
	d.calls = append(d.calls, "schedule")
	return d.scheduleErr
}

type scheduledRunner struct {
	name   string
	owner  *scheduledRuntimeDeps
	active bool
	err    error
}

func (r *scheduledRunner) RunOnce(context.Context) (bool, error) {
	r.owner.calls = append(r.owner.calls, r.name)
	return r.active, r.err
}
