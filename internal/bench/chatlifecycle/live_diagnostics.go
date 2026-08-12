package chatlifecycle

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// LiveDiagnosticStatusSchemaV1 is the strict running-workload contract
	// consumed by Analysis MCP before the final diagnostic summary exists.
	LiveDiagnosticStatusSchemaV1 = "wukongim/chat-lifecycle-diagnostic-status/v1"
	// LiveDiagnosticStatusFile is atomically replaced in the report directory.
	LiveDiagnosticStatusFile = "diagnostic-status.json"

	maxLiveDiagnosticStatusBytes  = 32 << 10
	maxLiveDiagnosticRecentEvents = 64
)

type liveDiagnosticState string
type liveDiagnosticStage string
type liveDiagnosticEventKind string

const (
	liveDiagnosticRunning  liveDiagnosticState = "running"
	liveDiagnosticMeasured liveDiagnosticStage = "measured"

	liveDiagnosticConnectionsChanged  liveDiagnosticEventKind = "worker_connections_changed"
	liveDiagnosticCloseReasonsChanged liveDiagnosticEventKind = "worker_close_reasons_changed"
)

type liveDiagnosticConnectionCounts struct {
	Target       int `json:"target"`
	Online       int `json:"online"`
	Starting     int `json:"starting"`
	Closing      int `json:"closing"`
	TrafficReady int `json:"traffic_ready"`
}

type liveDiagnosticWorker struct {
	WorkerID         uint64                         `json:"worker_id"`
	Phase            WorkerPhase                    `json:"phase"`
	SnapshotSequence uint64                         `json:"snapshot_sequence"`
	Connections      liveDiagnosticConnectionCounts `json:"connections"`
	CloseReasons     SessionCloseReasonSnapshot     `json:"close_reasons"`
}

type liveDiagnosticEvent struct {
	At           time.Time                      `json:"at"`
	Kind         liveDiagnosticEventKind        `json:"kind"`
	WorkerID     uint64                         `json:"worker_id"`
	Connections  liveDiagnosticConnectionCounts `json:"connections"`
	CloseReasons SessionCloseReasonSnapshot     `json:"close_reasons"`
}

type liveDiagnosticStatus struct {
	Schema       string                         `json:"schema"`
	RunID        string                         `json:"run_id"`
	State        liveDiagnosticState            `json:"state"`
	Stage        liveDiagnosticStage            `json:"stage"`
	StartedAt    time.Time                      `json:"started_at"`
	UpdatedAt    time.Time                      `json:"updated_at"`
	Cut          CoordinatorCutKind             `json:"cut"`
	Totals       liveDiagnosticConnectionCounts `json:"totals"`
	CloseReasons SessionCloseReasonSnapshot     `json:"close_reasons"`
	Workers      []liveDiagnosticWorker         `json:"workers"`
	RecentEvents []liveDiagnosticEvent          `json:"recent_events"`
}

// liveDiagnosticRecorder owns one current document and a fixed-size recent
// change ring. It never retains a UID, channel, message, address, or raw error.
type liveDiagnosticRecorder struct {
	mu       sync.Mutex
	path     string
	runID    string
	start    time.Time
	seen     [coordinatorWorkerCount]bool
	previous [coordinatorWorkerCount]liveDiagnosticWorker
	events   []liveDiagnosticEvent
	log      io.Writer
}

func newLiveDiagnosticRecorder(outputDir, runID string, start time.Time, log io.Writer) *liveDiagnosticRecorder {
	if log == nil {
		log = io.Discard
	}
	return &liveDiagnosticRecorder{
		path:  filepath.Join(filepath.Clean(outputDir), LiveDiagnosticStatusFile),
		runID: runID, start: start, log: log,
	}
}

// Observe records one exact three-worker evidence cut and atomically replaces
// the current status. Only aggregate changes enter the bounded recent log.
func (r *liveDiagnosticRecorder) Observe(at time.Time, cut CoordinatorCutKind, snapshots []WorkerSnapshot) error {
	if r == nil || r.path == "" || r.runID == "" || r.start.IsZero() || at.Before(r.start) ||
		(cut != CoordinatorCutPeriodic && cut != CoordinatorCutQualification && cut != CoordinatorCutTerminal) ||
		len(snapshots) != coordinatorWorkerCount {
		return errProductionController
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	workers, totals, closes, ok := projectLiveDiagnosticWorkers(snapshots)
	if !ok {
		return errProductionController
	}
	for workerID, worker := range workers {
		previous := r.previous[workerID]
		if !r.seen[workerID] || worker.Connections != previous.Connections {
			r.appendEvent(liveDiagnosticEvent{
				At: at, Kind: liveDiagnosticConnectionsChanged, WorkerID: uint64(workerID),
				Connections: worker.Connections, CloseReasons: worker.CloseReasons,
			})
		}
		if r.seen[workerID] && worker.CloseReasons != previous.CloseReasons {
			r.appendEvent(liveDiagnosticEvent{
				At: at, Kind: liveDiagnosticCloseReasonsChanged, WorkerID: uint64(workerID),
				Connections: worker.Connections, CloseReasons: worker.CloseReasons,
			})
		}
		r.previous[workerID], r.seen[workerID] = worker, true
	}
	document := liveDiagnosticStatus{
		Schema: LiveDiagnosticStatusSchemaV1, RunID: r.runID, State: liveDiagnosticRunning, Stage: liveDiagnosticMeasured,
		StartedAt: r.start, UpdatedAt: at, Cut: cut, Totals: totals, CloseReasons: closes,
		Workers:      append([]liveDiagnosticWorker(nil), workers[:]...),
		RecentEvents: append([]liveDiagnosticEvent(nil), r.events...),
	}
	if err := writeLiveDiagnosticStatus(r.path, document); err != nil {
		return err
	}
	r.writeLog(document)
	return nil
}

func (r *liveDiagnosticRecorder) writeLog(document liveDiagnosticStatus) {
	record := struct {
		Event        string                         `json:"event"`
		RunID        string                         `json:"run_id"`
		At           time.Time                      `json:"at"`
		Cut          CoordinatorCutKind             `json:"cut"`
		Totals       liveDiagnosticConnectionCounts `json:"totals"`
		CloseReasons SessionCloseReasonSnapshot     `json:"close_reasons"`
	}{
		Event: "wkbench.chat_lifecycle.worker_status_cut", RunID: document.RunID,
		At: document.UpdatedAt, Cut: document.Cut, Totals: document.Totals, CloseReasons: document.CloseReasons,
	}
	body, err := json.Marshal(record)
	if err == nil {
		_, _ = r.log.Write(append(body, '\n'))
	}
}

func (r *liveDiagnosticRecorder) appendEvent(event liveDiagnosticEvent) {
	r.events = append(r.events, event)
	if extra := len(r.events) - maxLiveDiagnosticRecentEvents; extra > 0 {
		copy(r.events, r.events[extra:])
		r.events = r.events[:maxLiveDiagnosticRecentEvents]
	}
}

func projectLiveDiagnosticWorkers(snapshots []WorkerSnapshot) (
	[coordinatorWorkerCount]liveDiagnosticWorker,
	liveDiagnosticConnectionCounts,
	SessionCloseReasonSnapshot,
	bool,
) {
	var workers [coordinatorWorkerCount]liveDiagnosticWorker
	var totals liveDiagnosticConnectionCounts
	var closes SessionCloseReasonSnapshot
	var seen [coordinatorWorkerCount]bool
	for _, snapshot := range snapshots {
		if snapshot.WorkerID >= coordinatorWorkerCount || seen[snapshot.WorkerID] || snapshot.Phase != WorkerPhaseRunning ||
			snapshot.SnapshotSequence == 0 {
			return workers, totals, closes, false
		}
		connections := liveDiagnosticConnectionCounts{
			Target: snapshot.Sessions.Target, Online: snapshot.Sessions.Online, Starting: snapshot.Sessions.Starting,
			Closing: snapshot.Sessions.Closing, TrafficReady: snapshot.Sessions.TrafficReady,
		}
		if !validLiveDiagnosticConnections(connections) || !addLiveDiagnosticConnections(&totals, connections) ||
			!addLiveDiagnosticCloseReasons(&closes, snapshot.Sessions.CloseReasons) {
			return workers, totals, closes, false
		}
		seen[snapshot.WorkerID] = true
		workers[snapshot.WorkerID] = liveDiagnosticWorker{
			WorkerID: snapshot.WorkerID, Phase: snapshot.Phase, SnapshotSequence: snapshot.SnapshotSequence,
			Connections: connections, CloseReasons: snapshot.Sessions.CloseReasons,
		}
	}
	return workers, totals, closes, seen == [coordinatorWorkerCount]bool{true, true, true}
}

func validLiveDiagnosticConnections(value liveDiagnosticConnectionCounts) bool {
	if value.Target <= 0 || value.Online < 0 || value.Starting < 0 || value.Closing < 0 || value.TrafficReady < 0 ||
		value.Online > value.Target || value.Starting > value.Target || value.Closing > value.Target || value.TrafficReady > value.Online {
		return false
	}
	return value.Online <= value.Target-value.Starting-value.Closing
}

func addLiveDiagnosticConnections(total *liveDiagnosticConnectionCounts, value liveDiagnosticConnectionCounts) bool {
	fields := [5]struct{ destination, source *int }{
		{&total.Target, &value.Target}, {&total.Online, &value.Online}, {&total.Starting, &value.Starting},
		{&total.Closing, &value.Closing}, {&total.TrafficReady, &value.TrafficReady},
	}
	for _, field := range fields {
		if *field.source > math.MaxInt-*field.destination {
			return false
		}
		*field.destination += *field.source
	}
	return true
}

func addLiveDiagnosticCloseReasons(total *SessionCloseReasonSnapshot, value SessionCloseReasonSnapshot) bool {
	fields := [7]struct{ destination, source *uint64 }{
		{&total.Expired, &value.Expired}, {&total.HeartbeatFailed, &value.HeartbeatFailed},
		{&total.RemoteTerminal, &value.RemoteTerminal}, {&total.ReadFailed, &value.ReadFailed},
		{&total.GenerationStop, &value.GenerationStop}, {&total.ExplicitLogout, &value.ExplicitLogout},
		{&total.TransportCloseFailed, &value.TransportCloseFailed},
	}
	for _, field := range fields {
		if math.MaxUint64-*field.destination < *field.source {
			return false
		}
		*field.destination += *field.source
	}
	return true
}

func writeLiveDiagnosticStatus(path string, document liveDiagnosticStatus) error {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if len(body) > maxLiveDiagnosticStatusBytes {
		return errProductionController
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".diagnostic-status.tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(body)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
