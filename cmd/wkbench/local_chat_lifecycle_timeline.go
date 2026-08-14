package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/spf13/cobra"
)

const (
	localChatLifecycleUnifiedTimelineSchemaV1 = "wukongim/chat-lifecycle-unified-timeline/v1"
	localChatLifecycleCutQuerySchemaV1        = "wukongim/chat-lifecycle-worker-cut-query/v1"
	localChatLifecycleWorkerCutEvent          = "wkbench.chat_lifecycle.worker_status_cut"
	maxLocalTimelineLineBytes                 = 1 << 20
	maxLocalTimelineCuts                      = 4096
	maxLocalTimelineBoundaries                = 4096
	maxLocalBoundaryTimelineBytes             = 2 << 20
)

var localBoundaryTimelineHeader = []string{"observed_at_utc", "phase", "node", "status"}

type localTimelineTriggerKind string

const (
	localTimelineTriggerActualOfferedRatio     localTimelineTriggerKind = "actual_offered_ratio"
	localTimelineTriggerTerminalProductFailure localTimelineTriggerKind = "terminal_product_failure"
)

type localTimelineConnectionCounts struct {
	Target       int `json:"target"`
	Online       int `json:"online"`
	Starting     int `json:"starting"`
	Closing      int `json:"closing"`
	TrafficReady int `json:"traffic_ready"`
}

type localTimelineWorkerCut struct {
	Event        string                                   `json:"event"`
	RunID        string                                   `json:"run_id"`
	At           time.Time                                `json:"at"`
	Cut          chatlifecycle.CoordinatorCutKind         `json:"cut"`
	Totals       localTimelineConnectionCounts            `json:"totals"`
	CloseReasons chatlifecycle.SessionCloseReasonSnapshot `json:"close_reasons"`
	Messages     chatlifecycle.WorkerMessageSnapshot      `json:"messages"`
}

type localTimelineBoundary struct {
	At     time.Time
	Kind   string
	Node   string
	Status string
}

type localTimelineWindow struct {
	StartAt     *time.Time `json:"start_at"`
	EndAt       *time.Time `json:"end_at"`
	Complete    bool       `json:"complete"`
	StartSource string     `json:"start_source"`
	EndSource   string     `json:"end_source"`
}

type localTimelinePoint struct {
	At                  time.Time                                 `json:"observed_at_utc"`
	Phase               string                                    `json:"phase"`
	Source              string                                    `json:"source"`
	Kind                string                                    `json:"kind"`
	Connections         *localTimelineConnectionCounts            `json:"connections,omitempty"`
	Messages            *chatlifecycle.WorkerMessageSnapshot      `json:"messages,omitempty"`
	CloseReasons        *chatlifecycle.SessionCloseReasonSnapshot `json:"close_reasons,omitempty"`
	RetryDelta          uint64                                    `json:"retry_delta,omitempty"`
	GenerationStopDelta uint64                                    `json:"generation_stop_delta,omitempty"`
	SessionClosedDelta  uint64                                    `json:"session_closed_delta,omitempty"`
	BoundaryNode        string                                    `json:"boundary_node,omitempty"`
	BoundaryStatus      string                                    `json:"boundary_status,omitempty"`
	BracketStartAt      *time.Time                                `json:"bracket_start_at,omitempty"`
	OverlapNodes        []string                                  `json:"overlap_nodes,omitempty"`
}

type localTimelineFirstBreach struct {
	Observed                bool                     `json:"observed"`
	TriggerKind             localTimelineTriggerKind `json:"trigger_kind,omitempty"`
	Phase                   string                   `json:"phase,omitempty"`
	PreviousAt              *time.Time               `json:"previous_at"`
	CurrentAt               *time.Time               `json:"current_at"`
	SentDelta               uint64                   `json:"sent_delta"`
	AcknowledgedDelta       uint64                   `json:"acknowledged_delta"`
	RetryDelta              uint64                   `json:"retry_delta"`
	TerminalFailureDelta    uint64                   `json:"terminal_product_failure_delta"`
	CorrectnessFailureDelta uint64                   `json:"correctness_failure_delta"`
	AcknowledgedPercent     float64                  `json:"acknowledged_percent"`
	IntervalSeconds         float64                  `json:"interval_seconds"`
	ExpectedOffered         float64                  `json:"expected_offered"`
	ActualOfferedPercent    float64                  `json:"actual_offered_percent"`
	MinimumThroughputPct    uint64                   `json:"minimum_throughput_percent"`
}

type localTimelineAmplification struct {
	RetryAfterFirstBreachDelta  uint64 `json:"retry_after_first_breach_delta"`
	ShutdownGenerationStopDelta uint64 `json:"shutdown_generation_stop_delta"`
	ShutdownCancellationDelta   uint64 `json:"shutdown_cancellation_delta"`
	ShutdownSessionClosedDelta  uint64 `json:"shutdown_session_closed_delta"`
	CancellationSource          string `json:"cancellation_source"`
}

type localTimelineSourceCompleteness struct {
	WorkerStatusCutsComplete bool `json:"worker_status_cuts_complete"`
	BoundaryTimelineComplete bool `json:"boundary_timeline_complete"`
	StorageOverlapComplete   bool `json:"storage_overlap_complete"`
	TerminalCutPresent       bool `json:"terminal_cut_present"`
	PartialWorkerLogLine     bool `json:"partial_worker_log_line"`
	FirstBreachObservable    bool `json:"first_breach_observable"`
}

type localChatLifecycleUnifiedTimeline struct {
	Schema                    string                          `json:"schema"`
	RunID                     string                          `json:"run_id"`
	OfferedRatePerSecond      uint64                          `json:"offered_rate_per_second"`
	MinimumThroughputPercent  uint64                          `json:"minimum_throughput_percent"`
	QualificationCutPresent   bool                            `json:"qualification_cut_present"`
	QualificationSentBoundary uint64                          `json:"qualification_sent_boundary"`
	SourceCompleteness        localTimelineSourceCompleteness `json:"source_completeness"`
	Windows                   map[string]localTimelineWindow  `json:"windows"`
	FirstBreach               localTimelineFirstBreach        `json:"first_breach"`
	MeasuredFirstBreach       localTimelineFirstBreach        `json:"measured_first_breach"`
	Amplification             localTimelineAmplification      `json:"amplification"`
	Overlap                   struct {
		Compaction localTimelineOverlapEvidence `json:"compaction"`
		Snapshot   localTimelineOverlapEvidence `json:"snapshot"`
	} `json:"overlap"`
	Points []localTimelinePoint `json:"points"`
}

type localChatLifecycleCutQuery struct {
	Schema                    string                       `json:"schema"`
	RunID                     string                       `json:"run_id"`
	Cursor                    int64                        `json:"cursor"`
	NextCursor                int64                        `json:"next_cursor"`
	CutsObserved              int                          `json:"cuts_observed"`
	PartialLine               bool                         `json:"partial_line"`
	TerminalCutPresent        bool                         `json:"terminal_cut_present"`
	QualificationCutPresent   bool                         `json:"qualification_cut_present"`
	QualificationSentBoundary uint64                       `json:"qualification_sent_boundary"`
	OfferedRatePerSecond      uint64                       `json:"offered_rate_per_second"`
	MinimumThroughputPercent  uint64                       `json:"minimum_throughput_percent"`
	Transition                localTimelineCutTransition   `json:"transition"`
	Transitions               []localTimelineCutTransition `json:"transitions"`
	PreviousCut               *localTimelineWorkerCut      `json:"previous_cut"`
	LatestCut                 *localTimelineWorkerCut      `json:"latest_cut"`
}

type localTimelineCutTransition struct {
	Available                   bool                             `json:"available"`
	PreviousAt                  *time.Time                       `json:"previous_at"`
	CurrentAt                   *time.Time                       `json:"current_at"`
	PreviousCutKind             chatlifecycle.CoordinatorCutKind `json:"previous_cut_kind,omitempty"`
	CurrentCutKind              chatlifecycle.CoordinatorCutKind `json:"current_cut_kind,omitempty"`
	SentDelta                   uint64                           `json:"sent_delta"`
	AcknowledgedDelta           uint64                           `json:"acknowledged_delta"`
	RetryDelta                  uint64                           `json:"retry_delta"`
	TerminalProductFailureDelta uint64                           `json:"terminal_product_failure_delta"`
	CorrectnessFailureDelta     uint64                           `json:"correctness_failure_delta"`
	IntervalSeconds             float64                          `json:"interval_seconds"`
	ExpectedOffered             float64                          `json:"expected_offered"`
	ActualOfferedPercent        float64                          `json:"actual_offered_percent"`
	MinimumThroughputPercent    uint64                           `json:"minimum_throughput_percent"`
	TriggerKind                 localTimelineTriggerKind         `json:"trigger_kind,omitempty"`
	MeasurementEligible         bool                             `json:"measurement_eligible"`
}

func newLocalChatLifecycleTimelineReportCommand() *cobra.Command {
	var workerLog, boundaryTimeline, storageOverlap, runID, outputJSON, outputTSV string
	var minimumAckPercent, offeredRate uint64
	cmd := &cobra.Command{
		Use:   "chat-lifecycle-timeline",
		Short: "Build a typed UTC timeline from local chat-lifecycle evidence",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(runID) == "" || offeredRate == 0 || minimumAckPercent == 0 || minimumAckPercent > 100 {
				return commandExit{code: exitConfig, message: "--run-id, --offered-rate, and --minimum-throughput-percent in [1,100] are required"}
			}
			cuts, nextCursor, partial, err := readLocalTimelineWorkerCuts(workerLog, runID, 0)
			if err != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("worker status evidence: %v", err)}
			}
			_ = nextCursor
			boundaries, err := readLocalTimelineBoundaries(boundaryTimeline)
			if err != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("boundary timeline evidence: %v", err)}
			}
			var storageSamples []localTimelineStorageOverlapSample
			var storageComplete bool
			if strings.TrimSpace(storageOverlap) != "" {
				storageSamples, storageComplete, err = readLocalTimelineStorageOverlap(storageOverlap, runID)
				if err != nil {
					return commandExit{code: exitConfig, message: fmt.Sprintf("storage overlap evidence: %v", err)}
				}
			}
			result, err := buildLocalChatLifecycleUnifiedTimeline(runID, offeredRate, minimumAckPercent, cuts, boundaries, storageSamples, storageComplete, partial)
			if err != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("timeline evidence: %v", err)}
			}
			jsonBody, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return commandExit{code: exitInternal, message: "timeline JSON encoding failed"}
			}
			jsonBody = append(jsonBody, '\n')
			tsvBody := renderLocalTimelineTSV(result.Points)
			if err := writeLocalTimelineFile(outputJSON, jsonBody); err != nil {
				return commandExit{code: exitInternal, message: "timeline JSON write failed"}
			}
			if err := writeLocalTimelineFile(outputTSV, tsvBody); err != nil {
				return commandExit{code: exitInternal, message: "timeline TSV write failed"}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workerLog, "worker-log", "", "mixed coordinator log containing typed worker-status cuts")
	cmd.Flags().StringVar(&boundaryTimeline, "boundary-timeline", "", "wrapper boundary timeline TSV")
	cmd.Flags().StringVar(&storageOverlap, "storage-overlap", "", "typed Pebble compaction and Raft snapshot sample TSV")
	cmd.Flags().StringVar(&runID, "run-id", "", "exact chat-lifecycle run ID")
	cmd.Flags().Uint64Var(&offeredRate, "offered-rate", 0, "offered SEND rate per second")
	cmd.Flags().Uint64Var(&minimumAckPercent, "minimum-throughput-percent", 90, "minimum interval actual/offered percentage")
	cmd.Flags().StringVar(&outputJSON, "output-json", "", "versioned unified timeline JSON")
	cmd.Flags().StringVar(&outputTSV, "output-tsv", "", "normalized unified timeline TSV")
	for _, name := range []string{"worker-log", "boundary-timeline", "run-id", "offered-rate", "output-json", "output-tsv"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func newLocalChatLifecycleCutQueryCommand() *cobra.Command {
	var workerLog, runID, previousQuery, output string
	var cursor int64
	var offeredRate, minimumThroughput uint64
	cmd := &cobra.Command{
		Use:   "chat-lifecycle-cut-query",
		Short: "Read complete typed worker-status cuts after a byte cursor",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(runID) == "" || cursor < 0 || offeredRate == 0 || minimumThroughput == 0 || minimumThroughput > 100 {
				return commandExit{code: exitConfig, message: "--run-id, a non-negative --cursor, --offered-rate, and --minimum-throughput-percent in [1,100] are required"}
			}
			cuts, nextCursor, partial, err := readLocalTimelineWorkerCuts(workerLog, runID, cursor)
			if err != nil {
				return commandExit{code: exitConfig, message: fmt.Sprintf("worker status evidence: %v", err)}
			}
			var prior localChatLifecycleCutQuery
			if strings.TrimSpace(previousQuery) != "" {
				prior, err = readLocalChatLifecycleCutQuery(previousQuery, runID, cursor, offeredRate, minimumThroughput)
				if err != nil {
					return commandExit{code: exitConfig, message: fmt.Sprintf("previous worker cut query: %v", err)}
				}
			}
			result := localChatLifecycleCutQuery{
				Schema: localChatLifecycleCutQuerySchemaV1, RunID: runID, Cursor: cursor,
				NextCursor: nextCursor, CutsObserved: len(cuts), PartialLine: partial,
				OfferedRatePerSecond: offeredRate, MinimumThroughputPercent: minimumThroughput,
				TerminalCutPresent: prior.TerminalCutPresent, PreviousCut: cloneLocalTimelineWorkerCut(prior.PreviousCut),
				QualificationCutPresent:   prior.QualificationCutPresent,
				QualificationSentBoundary: prior.QualificationSentBoundary, LatestCut: cloneLocalTimelineWorkerCut(prior.LatestCut),
			}
			if result.LatestCut != nil && len(cuts) > 0 {
				ordered := make([]localTimelineWorkerCut, 0, len(cuts)+1)
				ordered = append(ordered, *result.LatestCut)
				ordered = append(ordered, cuts...)
				if err := validateLocalTimelineCutOrder(ordered); err != nil {
					return commandExit{code: exitConfig, message: fmt.Sprintf("worker status evidence: %v", err)}
				}
			}
			for index := range cuts {
				previous := cloneLocalTimelineWorkerCut(result.LatestCut)
				result.PreviousCut = previous
				cut := cuts[index]
				result.LatestCut = &cut
				measurementEligible := result.QualificationCutPresent && cut.Cut != chatlifecycle.CoordinatorCutQualification
				var acknowledgementBoundary *uint64
				if measurementEligible {
					acknowledgementBoundary = &result.QualificationSentBoundary
				}
				transition := localTimelineCutTransitionFor(previous, result.LatestCut, offeredRate, minimumThroughput, acknowledgementBoundary)
				transition.MeasurementEligible = measurementEligible
				if cut.Cut == chatlifecycle.CoordinatorCutQualification {
					result.QualificationCutPresent = true
					result.QualificationSentBoundary = cut.Messages.Sent
				}
				if transition.Available {
					result.Transitions = append(result.Transitions, transition)
					result.Transition = transition
				}
				if cut.Cut == chatlifecycle.CoordinatorCutTerminal {
					result.TerminalCutPresent = true
				}
			}
			body, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return commandExit{code: exitInternal, message: "worker cut query encoding failed"}
			}
			if err := writeLocalTimelineFile(output, append(body, '\n')); err != nil {
				return commandExit{code: exitInternal, message: "worker cut query write failed"}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workerLog, "worker-log", "", "mixed coordinator log containing typed worker-status cuts")
	cmd.Flags().StringVar(&runID, "run-id", "", "exact chat-lifecycle run ID")
	cmd.Flags().Int64Var(&cursor, "cursor", 0, "byte cursor at the start of a complete log line")
	cmd.Flags().StringVar(&previousQuery, "previous-query", "", "previous query JSON whose next_cursor equals --cursor")
	cmd.Flags().Uint64Var(&offeredRate, "offered-rate", 0, "offered SEND rate per second")
	cmd.Flags().Uint64Var(&minimumThroughput, "minimum-throughput-percent", 90, "minimum interval actual/offered percentage")
	cmd.Flags().StringVar(&output, "output", "", "versioned worker-cut query JSON")
	for _, name := range []string{"worker-log", "run-id", "offered-rate", "output"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func readLocalChatLifecycleCutQuery(path, runID string, cursor int64, offeredRate, minimumThroughput uint64) (localChatLifecycleCutQuery, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return localChatLifecycleCutQuery{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxLocalTimelineLineBytes+1))
	decoder.DisallowUnknownFields()
	var result localChatLifecycleCutQuery
	if err := decoder.Decode(&result); err != nil {
		return localChatLifecycleCutQuery{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return localChatLifecycleCutQuery{}, errors.New("previous query has trailing JSON")
	}
	if result.Schema != localChatLifecycleCutQuerySchemaV1 || result.RunID != runID || result.NextCursor != cursor ||
		result.OfferedRatePerSecond != offeredRate || result.MinimumThroughputPercent != minimumThroughput ||
		result.Cursor < 0 || result.NextCursor < result.Cursor || result.CutsObserved < 0 ||
		(result.QualificationCutPresent && result.QualificationSentBoundary == 0) ||
		(!result.QualificationCutPresent && result.QualificationSentBoundary != 0) {
		return localChatLifecycleCutQuery{}, errors.New("previous query identity or cursor is invalid")
	}
	for _, cut := range []*localTimelineWorkerCut{result.PreviousCut, result.LatestCut} {
		if cut == nil {
			continue
		}
		if err := validateDecodedLocalTimelineWorkerCut(*cut, runID); err != nil {
			return localChatLifecycleCutQuery{}, fmt.Errorf("previous query cut: %w", err)
		}
	}
	if result.PreviousCut != nil && result.LatestCut != nil {
		if err := validateLocalTimelineCutOrder([]localTimelineWorkerCut{*result.PreviousCut, *result.LatestCut}); err != nil {
			return localChatLifecycleCutQuery{}, err
		}
	}
	return result, nil
}

func cloneLocalTimelineWorkerCut(source *localTimelineWorkerCut) *localTimelineWorkerCut {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func localTimelineCutTransitionFor(
	previous, latest *localTimelineWorkerCut,
	offeredRate, minimumThroughput uint64,
	acknowledgementBoundary *uint64,
) localTimelineCutTransition {
	if previous == nil || latest == nil {
		return localTimelineCutTransition{}
	}
	previousTerminal, previousCorrectness, previousOK := localTimelineProductFailureCounters(*previous)
	latestTerminal, latestCorrectness, latestOK := localTimelineProductFailureCounters(*latest)
	if !previousOK || !latestOK || latest.Messages.Sent < previous.Messages.Sent ||
		latest.Messages.SendAcknowledged < previous.Messages.SendAcknowledged ||
		latest.Messages.RetryAttempts < previous.Messages.RetryAttempts || latestTerminal < previousTerminal ||
		latestCorrectness < previousCorrectness {
		return localTimelineCutTransition{}
	}
	acknowledgedDelta := latest.Messages.SendAcknowledged - previous.Messages.SendAcknowledged
	if acknowledgementBoundary != nil {
		previousAcknowledged := localTimelineCounterAfterBoundary(previous.Messages.SendAcknowledged, *acknowledgementBoundary)
		latestAcknowledged := localTimelineCounterAfterBoundary(latest.Messages.SendAcknowledged, *acknowledgementBoundary)
		acknowledgedDelta = latestAcknowledged - previousAcknowledged
	}
	transition := localTimelineCutTransition{
		Available: true, PreviousAt: &previous.At, CurrentAt: &latest.At,
		PreviousCutKind: previous.Cut, CurrentCutKind: latest.Cut,
		SentDelta:                   latest.Messages.Sent - previous.Messages.Sent,
		AcknowledgedDelta:           acknowledgedDelta,
		RetryDelta:                  latest.Messages.RetryAttempts - previous.Messages.RetryAttempts,
		TerminalProductFailureDelta: latestTerminal - previousTerminal,
		CorrectnessFailureDelta:     latestCorrectness - previousCorrectness,
		MinimumThroughputPercent:    minimumThroughput,
	}
	transition.IntervalSeconds, transition.ExpectedOffered, transition.ActualOfferedPercent =
		localTimelineActualOffered(previous.At, latest.At, transition.AcknowledgedDelta, offeredRate)
	if transition.TerminalProductFailureDelta > 0 || transition.CorrectnessFailureDelta > 0 {
		transition.TriggerKind = localTimelineTriggerTerminalProductFailure
	} else if latest.Cut != chatlifecycle.CoordinatorCutTerminal && latest.CloseReasons.GenerationStop == previous.CloseReasons.GenerationStop &&
		transition.ExpectedOffered > 0 && transition.ActualOfferedPercent < float64(minimumThroughput) {
		transition.TriggerKind = localTimelineTriggerActualOfferedRatio
	}
	return transition
}

func readLocalTimelineWorkerCuts(path, runID string, cursor int64) ([]localTimelineWorkerCut, int64, bool, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, cursor, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, cursor, false, err
	}
	if cursor < 0 || cursor > info.Size() {
		return nil, cursor, false, errors.New("cursor is outside the log")
	}
	if cursor > 0 {
		if _, err := file.Seek(cursor-1, io.SeekStart); err != nil {
			return nil, cursor, false, err
		}
		var previous [1]byte
		if _, err := io.ReadFull(file, previous[:]); err != nil || previous[0] != '\n' {
			return nil, cursor, false, errors.New("cursor is not at a complete log-line boundary")
		}
	}
	if _, err := file.Seek(cursor, io.SeekStart); err != nil {
		return nil, cursor, false, err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	nextCursor := cursor
	cuts := make([]localTimelineWorkerCut, 0, 32)
	for {
		line, complete, err := readBoundedLocalTimelineLine(reader)
		if err != nil {
			return nil, nextCursor, false, err
		}
		if len(line) == 0 && !complete {
			return cuts, nextCursor, false, nil
		}
		if !complete {
			return cuts, nextCursor, true, nil
		}
		nextCursor += int64(len(line))
		cut, matched, err := decodeLocalTimelineWorkerCut(bytes.TrimSuffix(line, []byte{'\n'}), runID)
		if err != nil {
			return nil, nextCursor - int64(len(line)), false, err
		}
		if matched {
			cuts = append(cuts, cut)
			if len(cuts) > maxLocalTimelineCuts {
				return nil, nextCursor, false, errors.New("worker status cut limit exceeded")
			}
		}
	}
}

func readBoundedLocalTimelineLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxLocalTimelineLineBytes {
			return nil, false, errors.New("log line exceeds bounded decoder limit")
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, false, nil
		default:
			return nil, false, err
		}
	}
}

func decodeLocalTimelineWorkerCut(line []byte, runID string) (localTimelineWorkerCut, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return localTimelineWorkerCut{}, false, nil
	}
	var header struct {
		Event string `json:"event"`
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(line, &header); err != nil {
		if resemblesLocalTimelineWorkerCut(line, runID) {
			return localTimelineWorkerCut{}, false, errors.New("matching worker status cut is malformed JSON")
		}
		return localTimelineWorkerCut{}, false, nil
	}
	if header.Event != localChatLifecycleWorkerCutEvent {
		return localTimelineWorkerCut{}, false, nil
	}
	if header.RunID == "" {
		return localTimelineWorkerCut{}, false, errors.New("worker status cut run_id is required")
	}
	if header.RunID != runID {
		return localTimelineWorkerCut{}, false, nil
	}
	if err := validateLocalWorkerCutKeys(line); err != nil {
		return localTimelineWorkerCut{}, false, err
	}
	var cut localTimelineWorkerCut
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cut); err != nil {
		return localTimelineWorkerCut{}, false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return localTimelineWorkerCut{}, false, errors.New("worker status cut has trailing JSON")
	}
	if err := validateDecodedLocalTimelineWorkerCut(cut, runID); err != nil {
		return localTimelineWorkerCut{}, false, err
	}
	cut.At = cut.At.UTC()
	return cut, true, nil
}

// resemblesLocalTimelineWorkerCut reports whether an otherwise undecodable log
// line claims the cut event or the run identity being queried. Such a line
// cannot be treated as unrelated because it may hide a required evidence cut.
func resemblesLocalTimelineWorkerCut(line []byte, runID string) bool {
	if bytes.Contains(line, []byte(localChatLifecycleWorkerCutEvent)) {
		return true
	}
	return bytes.Contains(line, []byte(`"run_id"`)) && bytes.Contains(line, []byte(strconv.Quote(runID)))
}

func validateDecodedLocalTimelineWorkerCut(cut localTimelineWorkerCut, runID string) error {
	if cut.Event != localChatLifecycleWorkerCutEvent || cut.RunID != runID || cut.At.IsZero() ||
		(cut.Cut != chatlifecycle.CoordinatorCutPeriodic && cut.Cut != chatlifecycle.CoordinatorCutQualification &&
			cut.Cut != chatlifecycle.CoordinatorCutTerminal) {
		return errors.New("worker status cut identity is invalid")
	}
	if !validLocalTimelineConnections(cut.Totals) || cut.Messages.SendAcknowledged > cut.Messages.Sent ||
		cut.Messages.FirstAttempts > cut.Messages.Sent || cut.Messages.SendAttempts < cut.Messages.FirstAttempts ||
		cut.Messages.RetryAttempts > cut.Messages.SendAttempts {
		return errors.New("worker status cut counters are invalid")
	}
	terminalTotal, ok := addLocalTimelineCounters(
		cut.Messages.TerminalReasons.RetryExhausted.Total,
		cut.Messages.TerminalReasons.NonRetriable,
		cut.Messages.TerminalReasons.SessionClosed,
	)
	if !ok || terminalTotal != cut.Messages.Terminal {
		return errors.New("worker status terminal counters are invalid")
	}
	retryReasonTotal, ok := addLocalTimelineCounters(
		cut.Messages.TerminalReasons.RetryExhausted.AttemptTimeout,
		cut.Messages.TerminalReasons.RetryExhausted.LocalAdmission,
		cut.Messages.TerminalReasons.RetryExhausted.TransportError,
		cut.Messages.TerminalReasons.RetryExhausted.RetriableSendack,
		cut.Messages.TerminalReasons.RetryExhausted.Unclassified,
	)
	expectedAttempts, attemptsOK := addLocalTimelineCounters(cut.Messages.FirstAttempts, cut.Messages.RetryAttempts)
	if !ok || retryReasonTotal != cut.Messages.TerminalReasons.RetryExhausted.Total ||
		!attemptsOK || expectedAttempts != cut.Messages.SendAttempts {
		return errors.New("worker status retry counters are invalid")
	}
	return nil
}

func validateLocalWorkerCutKeys(line []byte) error {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(line, &outer); err != nil {
		return err
	}
	if err := requireExactLocalTimelineKeys(outer, "event", "run_id", "at", "cut", "totals", "close_reasons", "messages"); err != nil {
		return err
	}
	checks := []struct {
		raw  json.RawMessage
		keys []string
	}{
		{outer["totals"], []string{"target", "online", "starting", "closing", "traffic_ready"}},
		{outer["close_reasons"], []string{"expired", "heartbeat_failed", "remote_terminal", "read_failed", "generation_stop", "explicit_logout", "transport_close_failed"}},
		{outer["messages"], []string{"sent", "send_attempts", "first_attempts", "first_attempt_failures", "send_acknowledged", "send_rejected", "received", "receive_acknowledged", "receive_ack_failures", "retry_attempts", "terminal", "terminal_reasons", "losses", "duplicates", "corruptions", "sequence_regressions"}},
	}
	for _, check := range checks {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(check.raw, &object); err != nil {
			return err
		}
		if err := requireExactLocalTimelineKeys(object, check.keys...); err != nil {
			return err
		}
	}
	var messages map[string]json.RawMessage
	if err := json.Unmarshal(outer["messages"], &messages); err != nil {
		return err
	}
	var terminal map[string]json.RawMessage
	if err := json.Unmarshal(messages["terminal_reasons"], &terminal); err != nil {
		return err
	}
	if err := requireExactLocalTimelineKeys(terminal, "retry_exhausted", "non_retriable", "session_closed"); err != nil {
		return err
	}
	var exhausted map[string]json.RawMessage
	if err := json.Unmarshal(terminal["retry_exhausted"], &exhausted); err != nil {
		return err
	}
	return requireExactLocalTimelineKeys(exhausted, "total", "attempt_timeout", "local_admission", "transport_error", "retryable_sendack", "unclassified")
}

func requireExactLocalTimelineKeys(object map[string]json.RawMessage, keys ...string) error {
	if len(object) != len(keys) {
		return errors.New("worker status cut has missing or unknown fields")
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("worker status cut field %q is required", key)
		}
	}
	return nil
}

func validLocalTimelineConnections(value localTimelineConnectionCounts) bool {
	return value.Target > 0 && value.Online >= 0 && value.Starting >= 0 && value.Closing >= 0 &&
		value.TrafficReady >= 0 && value.Online <= value.Target && value.TrafficReady <= value.Online &&
		value.Starting <= value.Target && value.Closing <= value.Target && value.Online <= value.Target-value.Starting-value.Closing
}

func addLocalTimelineCounters(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		if ^uint64(0)-total < value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func readLocalTimelineBoundaries(path string) ([]localTimelineBoundary, error) {
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > maxLocalBoundaryTimelineBytes || body[len(body)-1] != '\n' {
		return nil, errors.New("boundary timeline is empty, oversized, or partial")
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) < 2 || !equalLocalTimelineColumns(strings.Split(lines[0], "\t"), localBoundaryTimelineHeader) {
		return nil, errors.New("boundary timeline header is invalid")
	}
	if len(lines)-1 > maxLocalTimelineBoundaries {
		return nil, errors.New("boundary timeline row limit exceeded")
	}
	boundaries := make([]localTimelineBoundary, 0, len(lines)-1)
	var previous time.Time
	for _, line := range lines[1:] {
		columns := strings.Split(line, "\t")
		if len(columns) != len(localBoundaryTimelineHeader) || strings.TrimSpace(columns[1]) == "" || strings.TrimSpace(columns[2]) == "" ||
			(columns[3] != "complete" && columns[3] != "failed") {
			return nil, errors.New("boundary timeline row is invalid")
		}
		at, err := time.Parse(time.RFC3339Nano, columns[0])
		if err != nil || at.Location() != time.UTC || (!previous.IsZero() && at.Before(previous)) {
			return nil, errors.New("boundary timeline UTC order is invalid")
		}
		previous = at
		boundaries = append(boundaries, localTimelineBoundary{At: at.UTC(), Kind: columns[1], Node: columns[2], Status: columns[3]})
	}
	return boundaries, nil
}

func equalLocalTimelineColumns(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func buildLocalChatLifecycleUnifiedTimeline(
	runID string,
	offeredRate uint64,
	minimumAckPercent uint64,
	cuts []localTimelineWorkerCut,
	boundaries []localTimelineBoundary,
	storageSamples []localTimelineStorageOverlapSample,
	storageComplete bool,
	partialWorkerLine bool,
) (localChatLifecycleUnifiedTimeline, error) {
	if strings.TrimSpace(runID) == "" || offeredRate == 0 || minimumAckPercent == 0 || minimumAckPercent > 100 || len(cuts) == 0 || len(boundaries) == 0 {
		return localChatLifecycleUnifiedTimeline{}, errors.New("required timeline evidence is absent")
	}
	sort.SliceStable(cuts, func(left, right int) bool { return cuts[left].At.Before(cuts[right].At) })
	if err := validateLocalTimelineCutOrder(cuts); err != nil {
		return localChatLifecycleUnifiedTimeline{}, err
	}
	marks := localTimelineBoundaryMarks(boundaries, cuts)
	boundaryComplete := true
	for _, boundary := range boundaries {
		if boundary.Status != "complete" {
			boundaryComplete = false
			break
		}
	}
	result := localChatLifecycleUnifiedTimeline{
		Schema: localChatLifecycleUnifiedTimelineSchemaV1, RunID: runID,
		OfferedRatePerSecond: offeredRate, MinimumThroughputPercent: minimumAckPercent,
		Windows: buildLocalTimelineWindows(marks, cuts),
		SourceCompleteness: localTimelineSourceCompleteness{
			WorkerStatusCutsComplete: !partialWorkerLine, BoundaryTimelineComplete: boundaryComplete,
			StorageOverlapComplete: storageComplete, PartialWorkerLogLine: partialWorkerLine,
		},
	}
	var overlapPoints []localTimelinePoint
	result.Overlap.Compaction, result.Overlap.Snapshot, overlapPoints = analyzeLocalTimelineStorageOverlap(storageSamples, storageComplete, marks)
	result.SourceCompleteness.StorageOverlapComplete = result.Overlap.Compaction.SourceComplete && result.Overlap.Snapshot.SourceComplete
	phases := localTimelineCutPhases(cuts, marks)
	previousByPhase := make(map[string]*localTimelineWorkerCut, 5)
	var previousCut *localTimelineWorkerCut
	var previousPhase string
	var qualificationSeen bool
	for index := range cuts {
		cut := &cuts[index]
		phase := phases[index]
		previous := previousByPhase[phase]
		// A qualification cut is the measured window's cumulative baseline.
		// It is never compared with warmup or an earlier measured cut.
		if cut.Cut == chatlifecycle.CoordinatorCutQualification {
			previous = nil
		}
		var acknowledgementBoundary *uint64
		if phase == "measured" && qualificationSeen {
			acknowledgementBoundary = &result.QualificationSentBoundary
		}
		var retryDelta, generationDelta, sessionDelta uint64
		if previous != nil {
			retryDelta = cut.Messages.RetryAttempts - previous.Messages.RetryAttempts
			generationDelta = cut.CloseReasons.GenerationStop - previous.CloseReasons.GenerationStop
			sessionDelta = cut.Messages.TerminalReasons.SessionClosed - previous.Messages.TerminalReasons.SessionClosed
			result.SourceCompleteness.FirstBreachObservable = true
		}
		connections, messages, closes := cut.Totals, cut.Messages, cut.CloseReasons
		result.Points = append(result.Points, localTimelinePoint{
			At: cut.At, Phase: phase, Source: "worker_status", Kind: string(cut.Cut),
			Connections: &connections, Messages: &messages, CloseReasons: &closes,
			RetryDelta: retryDelta, GenerationStopDelta: generationDelta, SessionClosedDelta: sessionDelta,
		})
		if cut.Cut == chatlifecycle.CoordinatorCutTerminal {
			result.SourceCompleteness.TerminalCutPresent = true
		}
		if !result.FirstBreach.Observed {
			if previous != nil {
				result.FirstBreach = classifyLocalTimelineInterval(*previous, *cut, phase, offeredRate, minimumAckPercent, acknowledgementBoundary, true)
			} else if previousCut != nil && previousPhase != phase {
				result.SourceCompleteness.FirstBreachObservable = true
				// Product/correctness failures remain meaningful across a phase
				// boundary. Preserve the bracket, but never compare actual/offered
				// throughput across qualification, drain, or shutdown.
				result.FirstBreach = classifyLocalTimelineInterval(*previousCut, *cut, previousPhase+"_to_"+phase, offeredRate, minimumAckPercent, nil, false)
			}
		}
		if !result.MeasuredFirstBreach.Observed {
			if phase == "measured" && previous != nil {
				result.MeasuredFirstBreach = classifyLocalTimelineInterval(*previous, *cut, phase, offeredRate, minimumAckPercent, acknowledgementBoundary, true)
			} else if phase == "shutdown" && previousCut != nil && previousPhase == "measured" {
				result.MeasuredFirstBreach = classifyLocalTimelineInterval(*previousCut, *cut, "measured_to_shutdown", offeredRate, minimumAckPercent, nil, false)
			}
		}
		if cut.Cut == chatlifecycle.CoordinatorCutQualification {
			qualificationSeen = true
			result.QualificationCutPresent = true
			result.QualificationSentBoundary = cut.Messages.Sent
		}
		previousByPhase[phase] = cut
		previousCut, previousPhase = cut, phase
	}
	for _, boundary := range boundaries {
		result.Points = append(result.Points, localTimelinePoint{
			At: boundary.At, Phase: localTimelinePhase(boundary.At, marks), Source: "boundary", Kind: boundary.Kind,
			BoundaryNode: boundary.Node, BoundaryStatus: boundary.Status,
		})
	}
	result.Points = append(result.Points, overlapPoints...)
	sort.SliceStable(result.Points, func(left, right int) bool {
		if result.Points[left].At.Equal(result.Points[right].At) {
			return result.Points[left].Source < result.Points[right].Source
		}
		return result.Points[left].At.Before(result.Points[right].At)
	})
	result.Amplification = localTimelineAmplificationFor(cuts, result.FirstBreach, marks.shutdownStart)
	if !result.FirstBreach.Observed {
		result.FirstBreach.MinimumThroughputPct = minimumAckPercent
	}
	if !result.MeasuredFirstBreach.Observed {
		result.MeasuredFirstBreach.MinimumThroughputPct = minimumAckPercent
	}
	return result, nil
}

func classifyLocalTimelineInterval(
	previous localTimelineWorkerCut,
	current localTimelineWorkerCut,
	phase string,
	offeredRate uint64,
	minimumAckPercent uint64,
	acknowledgementBoundary *uint64,
	allowRatio bool,
) localTimelineFirstBreach {
	previousAt, currentAt := previous.At, current.At
	acknowledgedDelta := current.Messages.SendAcknowledged - previous.Messages.SendAcknowledged
	if acknowledgementBoundary != nil {
		previousAcknowledged := localTimelineCounterAfterBoundary(previous.Messages.SendAcknowledged, *acknowledgementBoundary)
		currentAcknowledged := localTimelineCounterAfterBoundary(current.Messages.SendAcknowledged, *acknowledgementBoundary)
		acknowledgedDelta = currentAcknowledged - previousAcknowledged
	}
	result := localTimelineFirstBreach{
		Phase: phase, PreviousAt: &previousAt, CurrentAt: &currentAt,
		SentDelta:            current.Messages.Sent - previous.Messages.Sent,
		AcknowledgedDelta:    acknowledgedDelta,
		RetryDelta:           current.Messages.RetryAttempts - previous.Messages.RetryAttempts,
		MinimumThroughputPct: minimumAckPercent,
	}
	previousTerminal, previousCorrectness, previousOK := localTimelineProductFailureCounters(previous)
	currentTerminal, currentCorrectness, currentOK := localTimelineProductFailureCounters(current)
	if !previousOK || !currentOK || currentTerminal < previousTerminal || currentCorrectness < previousCorrectness {
		return localTimelineFirstBreach{}
	}
	result.TerminalFailureDelta = currentTerminal - previousTerminal
	result.CorrectnessFailureDelta = currentCorrectness - previousCorrectness
	if result.SentDelta > 0 {
		result.AcknowledgedPercent = float64(result.AcknowledgedDelta) * 100 / float64(result.SentDelta)
	}
	result.IntervalSeconds, result.ExpectedOffered, result.ActualOfferedPercent =
		localTimelineActualOffered(previous.At, current.At, result.AcknowledgedDelta, offeredRate)
	if result.TerminalFailureDelta > 0 || result.CorrectnessFailureDelta > 0 {
		result.Observed = true
		result.TriggerKind = localTimelineTriggerTerminalProductFailure
		return result
	}
	if allowRatio && result.ExpectedOffered > 0 && result.ActualOfferedPercent < float64(minimumAckPercent) {
		result.Observed = true
		result.TriggerKind = localTimelineTriggerActualOfferedRatio
		return result
	}
	return localTimelineFirstBreach{}
}

func localTimelineActualOffered(previous, current time.Time, acknowledged, offeredRate uint64) (seconds, expected, percent float64) {
	if offeredRate == 0 || !current.After(previous) {
		return 0, 0, 0
	}
	seconds = current.Sub(previous).Seconds()
	expected = float64(offeredRate) * seconds
	if expected <= 0 || math.IsNaN(expected) || math.IsInf(expected, 0) {
		return 0, 0, 0
	}
	percent = float64(acknowledged) * 100 / expected
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 {
		return 0, 0, 0
	}
	return seconds, expected, percent
}

func localTimelineCounterAfterBoundary(value, boundary uint64) uint64 {
	if value <= boundary {
		return 0
	}
	return value - boundary
}

func localTimelineProductFailureCounters(cut localTimelineWorkerCut) (terminal, correctness uint64, ok bool) {
	terminal, terminalOK := addLocalTimelineCounters(
		cut.Messages.TerminalReasons.RetryExhausted.Total,
		cut.Messages.TerminalReasons.NonRetriable,
	)
	correctness, correctnessOK := addLocalTimelineCounters(
		cut.Messages.Losses,
		cut.Messages.Duplicates,
		cut.Messages.Corruptions,
		cut.Messages.SequenceRegressions,
	)
	return terminal, correctness, terminalOK && correctnessOK
}

func validateLocalTimelineCutOrder(cuts []localTimelineWorkerCut) error {
	for index := range cuts {
		if index == 0 {
			continue
		}
		previous, current := cuts[index-1], cuts[index]
		previousTerminal, previousCorrectness, previousOK := localTimelineProductFailureCounters(previous)
		currentTerminal, currentCorrectness, currentOK := localTimelineProductFailureCounters(current)
		if !previousOK || !currentOK || !current.At.After(previous.At) || current.Messages.Sent < previous.Messages.Sent ||
			current.Messages.SendAcknowledged < previous.Messages.SendAcknowledged || current.Messages.RetryAttempts < previous.Messages.RetryAttempts ||
			current.CloseReasons.GenerationStop < previous.CloseReasons.GenerationStop ||
			current.Messages.TerminalReasons.SessionClosed < previous.Messages.TerminalReasons.SessionClosed ||
			currentTerminal < previousTerminal || currentCorrectness < previousCorrectness {
			return errors.New("worker status cuts are not strictly ordered and monotonic")
		}
	}
	return nil
}

type localTimelineMarks struct {
	warmupStart, warmupEnd, measurementStart, measurementEnd, drainStart, drainEnd, shutdownStart *time.Time
}

func localTimelineBoundaryMarks(boundaries []localTimelineBoundary, cuts []localTimelineWorkerCut) localTimelineMarks {
	var marks localTimelineMarks
	for index := range boundaries {
		boundary := boundaries[index]
		if boundary.Node != "boundary" || boundary.Status != "complete" {
			continue
		}
		target := map[string]**time.Time{
			"warmup_start": &marks.warmupStart, "warmup_end": &marks.warmupEnd,
			"measurement_start": &marks.measurementStart, "measurement_end": &marks.measurementEnd,
			"drain_start": &marks.drainStart, "drain_end": &marks.drainEnd, "shutdown_start": &marks.shutdownStart,
		}[boundary.Kind]
		if target != nil && *target == nil {
			at := boundary.At
			*target = &at
		}
	}
	for index := range cuts {
		if localTimelineCutStartsShutdown(cuts[index]) && (marks.shutdownStart == nil || cuts[index].At.Before(*marks.shutdownStart)) {
			at := cuts[index].At
			marks.shutdownStart = &at
		}
	}
	return marks
}

func localTimelineCutStartsShutdown(cut localTimelineWorkerCut) bool {
	return cut.CloseReasons.GenerationStop > 0
}

func localTimelineCutPhases(cuts []localTimelineWorkerCut, marks localTimelineMarks) []string {
	phases := make([]string, len(cuts))
	qualificationIndex := -1
	for index := range cuts {
		if cuts[index].Cut == chatlifecycle.CoordinatorCutQualification {
			qualificationIndex = index
			break
		}
	}
	qualificationSeen := false
	for index := range cuts {
		cut := cuts[index]
		phase := localTimelinePhase(cut.At, marks)
		if qualificationIndex >= 0 && index < qualificationIndex && phase == "measured" {
			// A second-precision measurement_start written after qualification
			// can sort before nanosecond-precision warmup cuts from that second.
			phase = "warmup"
		}
		if cut.Cut == chatlifecycle.CoordinatorCutQualification {
			qualificationSeen = true
			phase = "measured"
		} else if qualificationSeen && (phase == "startup" || phase == "warmup") {
			// The wrapper writes second-precision boundaries after the
			// nanosecond-precision qualification cut. Sequence is authoritative
			// when timestamp truncation makes that boundary appear earlier/later.
			phase = "measured"
		}
		if localTimelineCutStartsShutdown(cut) || (marks.shutdownStart != nil && !cut.At.Before(*marks.shutdownStart)) {
			phase = "shutdown"
		} else if marks.drainStart != nil && !cut.At.Before(*marks.drainStart) {
			phase = "drain"
		}
		phases[index] = phase
	}
	return phases
}

func localTimelinePhase(at time.Time, marks localTimelineMarks) string {
	switch {
	case marks.shutdownStart != nil && !at.Before(*marks.shutdownStart):
		return "shutdown"
	case marks.drainStart != nil && !at.Before(*marks.drainStart):
		return "drain"
	case marks.measurementStart != nil && !at.Before(*marks.measurementStart):
		return "measured"
	case marks.warmupStart != nil && !at.Before(*marks.warmupStart):
		return "warmup"
	default:
		return "startup"
	}
}

func buildLocalTimelineWindows(marks localTimelineMarks, cuts []localTimelineWorkerCut) map[string]localTimelineWindow {
	windows := map[string]localTimelineWindow{
		"startup":  {EndAt: marks.warmupStart, EndSource: "warmup_start"},
		"warmup":   localTimelineClosedWindow(marks.warmupStart, marks.warmupEnd, "warmup_start", "warmup_end"),
		"measured": localTimelineClosedWindow(marks.measurementStart, marks.measurementEnd, "measurement_start", "measurement_end"),
		"drain":    localTimelineClosedWindow(marks.drainStart, marks.drainEnd, "drain_start", "drain_end"),
		"shutdown": {StartAt: marks.shutdownStart, StartSource: "shutdown_start_or_terminal_cut"},
	}
	for index := len(cuts) - 1; index >= 0; index-- {
		if cuts[index].Cut == chatlifecycle.CoordinatorCutTerminal && marks.shutdownStart != nil && !cuts[index].At.Before(*marks.shutdownStart) {
			window := windows["shutdown"]
			at := cuts[index].At
			window.EndAt, window.EndSource, window.Complete = &at, "terminal_cut", true
			windows["shutdown"] = window
			break
		}
	}
	return windows
}

func localTimelineClosedWindow(start, end *time.Time, startSource, endSource string) localTimelineWindow {
	return localTimelineWindow{
		StartAt: start, EndAt: end, Complete: start != nil && end != nil && !end.Before(*start),
		StartSource: startSource, EndSource: endSource,
	}
}

func localTimelineAmplificationFor(cuts []localTimelineWorkerCut, breach localTimelineFirstBreach, shutdownStart *time.Time) localTimelineAmplification {
	if len(cuts) == 0 {
		return localTimelineAmplification{}
	}
	last := cuts[len(cuts)-1]
	result := localTimelineAmplification{CancellationSource: "messages.terminal_reasons.session_closed"}
	if breach.Observed && breach.PreviousAt != nil {
		foundBaseline := false
		for index := range cuts {
			if cuts[index].At.Equal(*breach.PreviousAt) {
				result.RetryAfterFirstBreachDelta = last.Messages.RetryAttempts - cuts[index].Messages.RetryAttempts
				foundBaseline = true
				break
			}
		}
		if !foundBaseline {
			result.RetryAfterFirstBreachDelta = last.Messages.RetryAttempts
		}
	}
	if shutdownStart == nil {
		return result
	}
	var baseline localTimelineWorkerCut
	for index := range cuts {
		if cuts[index].At.Before(*shutdownStart) {
			baseline = cuts[index]
		}
	}
	result.ShutdownGenerationStopDelta = last.CloseReasons.GenerationStop - baseline.CloseReasons.GenerationStop
	result.ShutdownSessionClosedDelta = last.Messages.TerminalReasons.SessionClosed - baseline.Messages.TerminalReasons.SessionClosed
	result.ShutdownCancellationDelta = result.ShutdownSessionClosedDelta
	return result
}

func renderLocalTimelineTSV(points []localTimelinePoint) []byte {
	var output strings.Builder
	output.WriteString("observed_at_utc\tphase\tsource\tkind\tsent\tacknowledged\tretry_attempts\tretry_delta\tgeneration_stop\tgeneration_stop_delta\tsession_closed\tsession_closed_delta\tboundary_node\tboundary_status\tbracket_start_at\toverlap_nodes\n")
	for _, point := range points {
		values := []string{point.At.UTC().Format(time.RFC3339Nano), point.Phase, point.Source, point.Kind, "", "", "", "", "", "", "", "", point.BoundaryNode, point.BoundaryStatus, "", strings.Join(point.OverlapNodes, ",")}
		if point.BracketStartAt != nil {
			values[14] = point.BracketStartAt.UTC().Format(time.RFC3339Nano)
		}
		if point.Messages != nil && point.CloseReasons != nil {
			values[4] = strconv.FormatUint(point.Messages.Sent, 10)
			values[5] = strconv.FormatUint(point.Messages.SendAcknowledged, 10)
			values[6] = strconv.FormatUint(point.Messages.RetryAttempts, 10)
			values[7] = strconv.FormatUint(point.RetryDelta, 10)
			values[8] = strconv.FormatUint(point.CloseReasons.GenerationStop, 10)
			values[9] = strconv.FormatUint(point.GenerationStopDelta, 10)
			values[10] = strconv.FormatUint(point.Messages.TerminalReasons.SessionClosed, 10)
			values[11] = strconv.FormatUint(point.SessionClosedDelta, 10)
		}
		output.WriteString(strings.Join(values, "\t"))
		output.WriteByte('\n')
	}
	return []byte(output.String())
}

func writeLocalTimelineFile(path string, body []byte) error {
	if strings.TrimSpace(path) == "" || filepath.Base(filepath.Clean(path)) == "." {
		return errors.New("timeline output path is invalid")
	}
	directory := filepath.Dir(filepath.Clean(path))
	temporary, err := os.CreateTemp(directory, ".wkbench-timeline-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Clean(path)); err != nil {
		return err
	}
	remove = false
	return nil
}
