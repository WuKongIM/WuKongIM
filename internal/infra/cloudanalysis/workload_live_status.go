package cloudanalysis

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

const (
	maxWorkloadLiveStatusBytes  = 64 << 10
	maxWorkloadLiveRecentEvents = 64
	workloadLiveStatusSchema    = "wukongim/chat-lifecycle-diagnostic-status/v1"
)

var errInvalidWorkloadLiveStatus = errors.New("internal/infra/cloudanalysis: invalid workload live status")

type workloadLiveStatusDocument struct {
	Schema       string                              `json:"schema"`
	RunID        string                              `json:"run_id"`
	State        string                              `json:"state"`
	Stage        string                              `json:"stage"`
	StartedAt    time.Time                           `json:"started_at"`
	UpdatedAt    time.Time                           `json:"updated_at"`
	Cut          string                              `json:"cut"`
	Totals       analysis.WorkloadConnectionCounts   `json:"totals"`
	CloseReasons analysis.WorkloadSessionCloseCounts `json:"close_reasons"`
	Workers      []analysis.WorkloadLiveWorker       `json:"workers"`
	RecentEvents []analysis.WorkloadLiveEvent        `json:"recent_events"`
}

func decodeWorkloadLiveStatus(reader io.Reader, expectedRunID string) (analysis.WorkloadLiveStatus, error) {
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > maxWorkloadLiveStatusBytes {
		return analysis.WorkloadLiveStatus{}, errInvalidWorkloadLiveStatus
	}
	if !hasRequiredWorkloadLiveFields(data) {
		return analysis.WorkloadLiveStatus{}, errInvalidWorkloadLiveStatus
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var document workloadLiveStatusDocument
	if err := decoder.Decode(&document); err != nil {
		return analysis.WorkloadLiveStatus{}, errInvalidWorkloadLiveStatus
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return analysis.WorkloadLiveStatus{}, errInvalidWorkloadLiveStatus
	}
	if !validWorkloadLiveStatus(document, expectedRunID) {
		return analysis.WorkloadLiveStatus{}, errInvalidWorkloadLiveStatus
	}
	return analysis.WorkloadLiveStatus{
		Stage: document.Stage, Cut: document.Cut, StartedAt: document.StartedAt, UpdatedAt: document.UpdatedAt,
		Totals: document.Totals, CloseReasons: document.CloseReasons,
		Workers:      append([]analysis.WorkloadLiveWorker(nil), document.Workers...),
		RecentEvents: append([]analysis.WorkloadLiveEvent(nil), document.RecentEvents...),
	}, nil
}

func validWorkloadLiveStatus(document workloadLiveStatusDocument, expectedRunID string) bool {
	if document.Schema != workloadLiveStatusSchema || document.RunID != expectedRunID ||
		document.State != "running" || document.Stage != "measured" ||
		(document.Cut != "periodic" && document.Cut != "qualification" && document.Cut != "terminal") ||
		document.StartedAt.IsZero() || document.UpdatedAt.Before(document.StartedAt) ||
		len(document.Workers) != 3 || len(document.RecentEvents) > maxWorkloadLiveRecentEvents ||
		!validWorkloadConnections(document.Totals) {
		return false
	}
	var seen [3]bool
	var totals analysis.WorkloadConnectionCounts
	var closes analysis.WorkloadSessionCloseCounts
	for index, worker := range document.Workers {
		if worker.WorkerID != uint64(index) || worker.WorkerID >= 3 || seen[worker.WorkerID] ||
			(worker.Phase != "running" && worker.Phase != "final") ||
			worker.SnapshotSequence == 0 || !validWorkloadConnections(worker.Connections) {
			return false
		}
		seen[worker.WorkerID] = true
		if !addWorkloadConnections(&totals, worker.Connections) || !addWorkloadCloseCounts(&closes, worker.CloseReasons) {
			return false
		}
	}
	if totals != document.Totals || closes != document.CloseReasons {
		return false
	}
	lastEventAt := document.StartedAt
	var eventCloses [3]analysis.WorkloadSessionCloseCounts
	var eventSeen [3]bool
	for _, event := range document.RecentEvents {
		if event.At.Before(document.StartedAt) || event.At.After(document.UpdatedAt) || event.WorkerID >= 3 ||
			event.At.Before(lastEventAt) ||
			(event.Kind != "worker_connections_changed" && event.Kind != "worker_close_reasons_changed") ||
			!validWorkloadConnections(event.Connections) || event.Connections.Target != document.Workers[event.WorkerID].Connections.Target ||
			!workloadCloseCountsWithin(event.CloseReasons, document.Workers[event.WorkerID].CloseReasons) {
			return false
		}
		if eventSeen[event.WorkerID] && !workloadCloseCountsMonotonic(event.CloseReasons, eventCloses[event.WorkerID]) {
			return false
		}
		eventCloses[event.WorkerID], eventSeen[event.WorkerID] = event.CloseReasons, true
		lastEventAt = event.At
	}
	return true
}

func workloadCloseCountsMonotonic(current, previous analysis.WorkloadSessionCloseCounts) bool {
	return current.Expired >= previous.Expired && current.HeartbeatFailed >= previous.HeartbeatFailed &&
		current.RemoteTerminal >= previous.RemoteTerminal && current.ReadFailed >= previous.ReadFailed &&
		current.GenerationStop >= previous.GenerationStop && current.ExplicitLogout >= previous.ExplicitLogout &&
		current.TransportCloseFailed >= previous.TransportCloseFailed
}

func hasRequiredWorkloadLiveFields(data []byte) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || !hasNonNullJSONFields(root,
		"schema", "run_id", "state", "stage", "started_at", "updated_at", "cut", "totals", "close_reasons", "workers", "recent_events",
	) || !hasRequiredWorkloadConnections(root["totals"]) || !hasRequiredWorkloadCloseCounts(root["close_reasons"]) {
		return false
	}
	var workers []map[string]json.RawMessage
	if err := json.Unmarshal(root["workers"], &workers); err != nil {
		return false
	}
	for _, worker := range workers {
		if !hasNonNullJSONFields(worker, "worker_id", "phase", "snapshot_sequence", "connections", "close_reasons") ||
			!hasRequiredWorkloadConnections(worker["connections"]) || !hasRequiredWorkloadCloseCounts(worker["close_reasons"]) {
			return false
		}
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal(root["recent_events"], &events); err != nil {
		return false
	}
	for _, event := range events {
		if !hasNonNullJSONFields(event, "at", "kind", "worker_id", "connections", "close_reasons") ||
			!hasRequiredWorkloadConnections(event["connections"]) || !hasRequiredWorkloadCloseCounts(event["close_reasons"]) {
			return false
		}
	}
	return true
}

func hasRequiredWorkloadConnections(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && hasNonNullJSONFields(value, "target", "online", "starting", "closing", "traffic_ready")
}

func hasRequiredWorkloadCloseCounts(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && hasNonNullJSONFields(value,
		"expired", "heartbeat_failed", "remote_terminal", "read_failed", "generation_stop", "explicit_logout", "transport_close_failed",
	)
}

func workloadCloseCountsWithin(value, limit analysis.WorkloadSessionCloseCounts) bool {
	return value.Expired <= limit.Expired && value.HeartbeatFailed <= limit.HeartbeatFailed &&
		value.RemoteTerminal <= limit.RemoteTerminal && value.ReadFailed <= limit.ReadFailed &&
		value.GenerationStop <= limit.GenerationStop && value.ExplicitLogout <= limit.ExplicitLogout &&
		value.TransportCloseFailed <= limit.TransportCloseFailed
}

func validWorkloadConnections(value analysis.WorkloadConnectionCounts) bool {
	if value.Target <= 0 || value.Online < 0 || value.Starting < 0 || value.Closing < 0 || value.TrafficReady < 0 ||
		value.Online > value.Target || value.Starting > value.Target || value.Closing > value.Target ||
		value.TrafficReady > value.Online {
		return false
	}
	return value.Online <= value.Target-value.Starting-value.Closing
}

func addWorkloadConnections(total *analysis.WorkloadConnectionCounts, value analysis.WorkloadConnectionCounts) bool {
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

func addWorkloadCloseCounts(total *analysis.WorkloadSessionCloseCounts, value analysis.WorkloadSessionCloseCounts) bool {
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
