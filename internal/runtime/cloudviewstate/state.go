// Package cloudviewstate persists run-scoped Cloud View interaction state.
package cloudviewstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State records whether a Simulation Run can still be treated as a pure
// benchmark and whether an operator changed run state through Manager.
type State struct {
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// Interactive becomes true after successful Demo or public WebSocket use.
	Interactive bool `json:"interactive"`
	// OperatorModified becomes true after a successful Manager write request.
	OperatorModified bool `json:"operator_modified"`
	// UpdatedAt records the most recent durable state transition.
	UpdatedAt time.Time `json:"updated_at"`
}

// Recorder owns monotonic interaction state and its durable projections.
type Recorder struct {
	// mu protects state and persistence retry metadata.
	mu sync.Mutex
	// state is the latest conservative in-memory run state.
	state State
	// statePath is the optional durable JSON projection.
	statePath string
	// metricsPath is the optional node_exporter textfile projection.
	metricsPath string
	// dirty reports that at least one projection needs retry.
	dirty bool
	// retrying reports that one background retry loop is active.
	retrying bool
}

// New creates a recorder and restores matching state when it already exists.
func New(runID, statePath, metricsPath string) (*Recorder, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("cloud view run ID is required")
	}
	state := State{RunID: runID, UpdatedAt: time.Now().UTC()}
	if statePath != "" {
		persisted, found, err := read(statePath, runID)
		if err != nil {
			return nil, err
		}
		if found {
			state = persisted
		}
	}
	recorder := &Recorder{state: state, statePath: statePath, metricsPath: metricsPath}
	if err := recorder.persistLocked(); err != nil {
		return nil, err
	}
	return recorder, nil
}

func read(path, expectedRunID string) (State, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, false, fmt.Errorf("decode cloud view run state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, false, errors.New("cloud view run state contains trailing data")
	}
	if strings.TrimSpace(state.RunID) == "" || state.UpdatedAt.IsZero() ||
		(expectedRunID != "" && state.RunID != expectedRunID) {
		return State{}, false, errors.New("cloud view run state identity mismatch")
	}
	return state, true, nil
}

// Snapshot returns the current in-memory state.
func (r *Recorder) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// StatusSnapshot atomically returns current state and projection health.
func (r *Recorder) StatusSnapshot() (State, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, !r.dirty
}

// PersistenceHealthy reports whether every current state projection is durable.
func (r *Recorder) PersistenceHealthy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.dirty
}

// MarkInteractive durably records Demo or public WebSocket use.
func (r *Recorder) MarkInteractive() error {
	return r.update(func(state *State) bool {
		if state.Interactive {
			return false
		}
		state.Interactive = true
		return true
	})
}

// MarkOperatorModified durably records a successful Manager write.
func (r *Recorder) MarkOperatorModified() error {
	return r.update(func(state *State) bool {
		if state.OperatorModified {
			return false
		}
		state.OperatorModified = true
		return true
	})
}

func (r *Recorder) update(mutate func(*State) bool) error {
	r.mu.Lock()
	changed := mutate(&r.state)
	if !changed && !r.dirty {
		r.mu.Unlock()
		return nil
	}
	if changed {
		r.state.UpdatedAt = time.Now().UTC()
	}
	if err := r.persistLocked(); err != nil {
		r.dirty = true
		startRetry := !r.retrying
		r.retrying = true
		r.mu.Unlock()
		if startRetry {
			go r.retryPersistence()
		}
		return err
	}
	r.dirty = false
	r.mu.Unlock()
	return nil
}

func (r *Recorder) retryPersistence() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		if !r.dirty {
			r.retrying = false
			r.mu.Unlock()
			return
		}
		if err := r.persistLocked(); err == nil {
			r.dirty = false
			r.retrying = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
	}
}

func (r *Recorder) persistLocked() error {
	if r.statePath != "" {
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(r.state); err != nil {
			return err
		}
		if err := writeAtomic(r.statePath, body.Bytes(), 0o600); err != nil {
			return err
		}
	}
	if r.metricsPath != "" {
		interactive := 0
		if r.state.Interactive {
			interactive = 1
		}
		operatorModified := 0
		if r.state.OperatorModified {
			operatorModified = 1
		}
		body := fmt.Sprintf("# HELP wukongim_cloud_view_interactive Whether Demo or public WebSocket traffic used this run.\n"+
			"# TYPE wukongim_cloud_view_interactive gauge\n"+
			"wukongim_cloud_view_interactive %d\n"+
			"# HELP wukongim_cloud_view_operator_modified Whether a successful Manager write modified this run.\n"+
			"# TYPE wukongim_cloud_view_operator_modified gauge\n"+
			"wukongim_cloud_view_operator_modified %d\n", interactive, operatorModified)
		if err := writeAtomic(r.metricsPath, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".cloud-view-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
