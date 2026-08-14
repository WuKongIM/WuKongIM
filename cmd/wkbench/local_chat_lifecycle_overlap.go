package main

import (
	"bufio"
	"crypto/sha256"
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
)

const (
	maxLocalStorageOverlapBytes = 2 << 20
	maxLocalStorageOverlapRows  = 4096
)

var localStorageOverlapHeader = []string{
	"observed_at_utc", "run_id", "sample", "node", "status", "compaction_count",
	"compactions_in_progress", "snapshot_files", "snapshot_bytes", "snapshot_identity", "snapshot_inventory",
}

type localTimelineStorageOverlapSample struct {
	At                    time.Time
	RunID                 string
	Sample                string
	Node                  string
	Status                string
	CompactionCount       uint64
	CompactionsInProgress uint64
	SnapshotFiles         uint64
	SnapshotBytes         uint64
	SnapshotIdentity      string
	SnapshotInventory     string
}

// localTimelineOverlapWindow brackets background storage activity between two
// bounded observations. It does not claim an exact start or end instant.
type localTimelineOverlapWindow struct {
	PreviousAt time.Time `json:"previous_at"`
	CurrentAt  time.Time `json:"current_at"`
	Phase      string    `json:"phase"`
	Nodes      []string  `json:"nodes"`
}

// localTimelineOverlapEvidence distinguishes observed, not-observed, and
// unknown background storage activity without treating missing data as zero.
type localTimelineOverlapEvidence struct {
	Status         string                       `json:"status"`
	SourceComplete bool                         `json:"source_complete"`
	Windows        []localTimelineOverlapWindow `json:"windows"`
}

// readLocalTimelineStorageOverlap strictly validates the retained raw sample
// inventory so the normalized timeline remains reproducible after data pruning.
func readLocalTimelineStorageOverlap(path, expectedRunID string) ([]localTimelineStorageOverlapSample, bool, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxLocalStorageOverlapBytes+1))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	lineNumber := 0
	complete := true
	var previous time.Time
	seen := make(map[string]struct{})
	rows := make([]localTimelineStorageOverlapSample, 0, 32)
	for scanner.Scan() {
		lineNumber++
		if lineNumber > maxLocalStorageOverlapRows+1 {
			return nil, false, errors.New("storage overlap evidence has too many rows")
		}
		columns := strings.Split(scanner.Text(), "\t")
		if lineNumber == 1 {
			if !equalLocalTimelineColumns(columns, localStorageOverlapHeader) {
				return nil, false, errors.New("storage overlap header is invalid")
			}
			continue
		}
		if len(columns) != len(localStorageOverlapHeader) {
			return nil, false, fmt.Errorf("storage overlap row %d is invalid", lineNumber)
		}
		at, parseErr := time.Parse(time.RFC3339Nano, columns[0])
		if parseErr != nil || at.Location() != time.UTC || (!previous.IsZero() && at.Before(previous)) {
			return nil, false, fmt.Errorf("storage overlap row %d UTC order is invalid", lineNumber)
		}
		previous = at
		if columns[1] != expectedRunID || !validLocalStorageSampleName(columns[2]) || !validLocalStorageNode(columns[3]) ||
			(columns[4] != "complete" && columns[4] != "missing") {
			return nil, false, fmt.Errorf("storage overlap row %d identity is invalid", lineNumber)
		}
		key := columns[0] + "\x00" + columns[1] + "\x00" + columns[2] + "\x00" + columns[3]
		if _, exists := seen[key]; exists {
			return nil, false, fmt.Errorf("storage overlap row %d is duplicated", lineNumber)
		}
		seen[key] = struct{}{}
		row := localTimelineStorageOverlapSample{At: at.UTC(), RunID: columns[1], Sample: columns[2], Node: columns[3], Status: columns[4]}
		if row.Status == "missing" {
			for _, value := range columns[5:] {
				if value != "unavailable" {
					return nil, false, fmt.Errorf("storage overlap row %d missing values are invalid", lineNumber)
				}
			}
			complete = false
		} else {
			values := []*uint64{&row.CompactionCount, &row.CompactionsInProgress, &row.SnapshotFiles, &row.SnapshotBytes}
			for index, destination := range values {
				value, valueErr := strconv.ParseUint(columns[index+5], 10, 64)
				if valueErr != nil {
					return nil, false, fmt.Errorf("storage overlap row %d value is invalid", lineNumber)
				}
				*destination = value
			}
			row.SnapshotIdentity = columns[9]
			if !validLocalSnapshotIdentity(row.SnapshotIdentity) {
				return nil, false, fmt.Errorf("storage overlap row %d snapshot identity is invalid", lineNumber)
			}
			row.SnapshotInventory = columns[10]
			if err := validateLocalSnapshotInventory(filepath.Dir(filepath.Clean(path)), row); err != nil {
				return nil, false, fmt.Errorf("storage overlap row %d inventory is invalid: %w", lineNumber, err)
			}
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	if lineNumber == 0 || len(rows) < 6 {
		return nil, false, errors.New("storage overlap evidence is absent")
	}
	groups := make(map[string]map[string]struct{})
	for _, row := range rows {
		key := row.At.Format(time.RFC3339Nano) + "\x00" + row.Sample
		if groups[key] == nil {
			groups[key] = make(map[string]struct{}, 3)
		}
		groups[key][row.Node] = struct{}{}
	}
	if len(groups) < 2 {
		return nil, false, errors.New("storage overlap evidence needs two sample cuts")
	}
	for _, nodes := range groups {
		if len(nodes) != 3 {
			return nil, false, errors.New("storage overlap sample cut is incomplete")
		}
		for _, node := range []string{"node-1", "node-2", "node-3"} {
			if _, ok := nodes[node]; !ok {
				return nil, false, errors.New("storage overlap sample cut is incomplete")
			}
		}
	}
	return rows, complete, nil
}

func validLocalStorageSampleName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character != '-' && character != '_' && (character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func validLocalStorageNode(value string) bool {
	return value == "node-1" || value == "node-2" || value == "node-3"
}

func validLocalSnapshotIdentity(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateLocalSnapshotInventory(directory string, row localTimelineStorageOverlapSample) error {
	relative := filepath.Clean(row.SnapshotInventory)
	expectedRelative := filepath.Join("snapshot-inventory", row.Sample+"-"+row.Node+".tsv")
	if row.SnapshotInventory == "" || filepath.IsAbs(relative) || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.Contains(relative, "\t") {
		return errors.New("inventory path is unsafe")
	}
	if relative != expectedRelative || filepath.ToSlash(row.SnapshotInventory) != filepath.ToSlash(expectedRelative) {
		return errors.New("inventory path does not match sample identity")
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("evidence directory is unavailable")
	}
	inventoryDirectory := filepath.Join(directory, "snapshot-inventory")
	inventoryDirectoryInfo, err := os.Lstat(inventoryDirectory)
	if err != nil || !inventoryDirectoryInfo.IsDir() || inventoryDirectoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("inventory directory is unavailable")
	}
	path := filepath.Join(directory, relative)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 512<<10 {
		return errors.New("inventory file is unavailable")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	identity := fmt.Sprintf("%x", sha256.Sum256(body))
	if identity != row.SnapshotIdentity {
		return errors.New("inventory digest does not match")
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(body) == 0 {
		lines = nil
	}
	if uint64(len(lines)) != row.SnapshotFiles || len(lines) > 4096 {
		return errors.New("inventory count does not match")
	}
	var totalBytes uint64
	previous := ""
	for _, line := range lines {
		columns := strings.Split(line, "\t")
		if len(columns) != 2 || columns[0] == "" || columns[0] <= previous || filepath.IsAbs(columns[0]) ||
			strings.Contains(columns[0], "..") || strings.ContainsAny(columns[0], "\r\n") {
			return errors.New("inventory entry is invalid")
		}
		size, parseErr := strconv.ParseUint(columns[1], 10, 64)
		if parseErr != nil || math.MaxUint64-totalBytes < size {
			return errors.New("inventory size is invalid")
		}
		totalBytes += size
		previous = columns[0]
	}
	if totalBytes != row.SnapshotBytes {
		return errors.New("inventory bytes do not match")
	}
	return nil
}

// analyzeLocalTimelineStorageOverlap derives compaction and snapshot brackets
// independently and propagates any reset, missing cut, or boundary gap.
func analyzeLocalTimelineStorageOverlap(
	rows []localTimelineStorageOverlapSample,
	sourceComplete bool,
	marks localTimelineMarks,
) (localTimelineOverlapEvidence, localTimelineOverlapEvidence, []localTimelinePoint) {
	sourceComplete = sourceComplete && localTimelineStorageCoverageComplete(rows, marks)
	compaction := localTimelineOverlapEvidence{Status: "unknown", SourceComplete: sourceComplete}
	snapshot := localTimelineOverlapEvidence{Status: "unknown", SourceComplete: sourceComplete}
	previous := make(map[string]localTimelineStorageOverlapSample, 3)
	compactionByBracket := make(map[string]*localTimelineOverlapWindow)
	snapshotByBracket := make(map[string]*localTimelineOverlapWindow)
	for _, current := range rows {
		prior, exists := previous[current.Node]
		previous[current.Node] = current
		if !exists || prior.Status != "complete" || current.Status != "complete" {
			continue
		}
		phase := localTimelineIntervalPhase(prior.At, current.At, marks)
		if current.CompactionCount < prior.CompactionCount {
			sourceComplete = false
		} else if current.CompactionCount > prior.CompactionCount || prior.CompactionsInProgress > 0 || current.CompactionsInProgress > 0 {
			appendLocalTimelineOverlapNode(compactionByBracket, prior.At, current.At, phase, current.Node)
		}
		if current.SnapshotFiles != prior.SnapshotFiles || current.SnapshotBytes != prior.SnapshotBytes ||
			current.SnapshotIdentity != prior.SnapshotIdentity {
			appendLocalTimelineOverlapNode(snapshotByBracket, prior.At, current.At, phase, current.Node)
		}
	}
	compaction.SourceComplete, snapshot.SourceComplete = sourceComplete, sourceComplete
	compaction.Windows = sortedLocalTimelineOverlapWindows(compactionByBracket)
	snapshot.Windows = sortedLocalTimelineOverlapWindows(snapshotByBracket)
	if len(compaction.Windows) > 0 {
		compaction.Status = "observed"
	} else if sourceComplete {
		compaction.Status = "not_observed"
	}
	if len(snapshot.Windows) > 0 {
		snapshot.Status = "observed"
	} else if sourceComplete {
		snapshot.Status = "not_observed"
	}
	points := make([]localTimelinePoint, 0, len(compaction.Windows)+len(snapshot.Windows))
	for _, item := range []struct {
		kind    string
		windows []localTimelineOverlapWindow
	}{{"compaction", compaction.Windows}, {"snapshot", snapshot.Windows}} {
		for _, window := range item.windows {
			start := window.PreviousAt
			points = append(points, localTimelinePoint{
				At: window.CurrentAt, Phase: window.Phase, Source: "storage_overlap", Kind: item.kind,
				BracketStartAt: &start, OverlapNodes: append([]string(nil), window.Nodes...),
			})
		}
	}
	return compaction, snapshot, points
}

func localTimelineStorageCoverageComplete(rows []localTimelineStorageOverlapSample, marks localTimelineMarks) bool {
	if marks.warmupStart == nil || marks.drainStart == nil || marks.shutdownStart == nil {
		return false
	}
	groups := make(map[string]struct {
		at    time.Time
		nodes map[string]struct{}
	})
	for _, row := range rows {
		if row.Sample != "warmup-before" && row.Sample != "before" && row.Sample != "after" {
			continue
		}
		group := groups[row.Sample]
		if group.nodes == nil {
			group.at, group.nodes = row.At, make(map[string]struct{}, 3)
		}
		if !group.at.Equal(row.At) {
			return false
		}
		group.nodes[row.Node] = struct{}{}
		groups[row.Sample] = group
	}
	baselineName, baselineStart := "warmup-before", marks.warmupStart
	if marks.measurementStart != nil || marks.measurementEnd != nil {
		if marks.measurementStart == nil || marks.measurementEnd == nil {
			return false
		}
		baselineName, baselineStart = "before", marks.measurementStart
	}
	before, beforeOK := groups[baselineName]
	after, afterOK := groups["after"]
	return beforeOK && afterOK && len(before.nodes) == 3 && len(after.nodes) == 3 &&
		!before.at.Before(*baselineStart) && !before.at.After(*marks.drainStart) &&
		!after.at.Before(*marks.shutdownStart) && !after.at.Before(before.at)
}

func appendLocalTimelineOverlapNode(
	windows map[string]*localTimelineOverlapWindow,
	previousAt, currentAt time.Time,
	phase, node string,
) {
	key := previousAt.Format(time.RFC3339Nano) + "\x00" + currentAt.Format(time.RFC3339Nano) + "\x00" + phase
	window := windows[key]
	if window == nil {
		window = &localTimelineOverlapWindow{PreviousAt: previousAt, CurrentAt: currentAt, Phase: phase}
		windows[key] = window
	}
	window.Nodes = append(window.Nodes, node)
}

func sortedLocalTimelineOverlapWindows(values map[string]*localTimelineOverlapWindow) []localTimelineOverlapWindow {
	result := make([]localTimelineOverlapWindow, 0, len(values))
	for _, value := range values {
		sort.Strings(value.Nodes)
		result = append(result, *value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CurrentAt.Equal(result[right].CurrentAt) {
			return result[left].PreviousAt.Before(result[right].PreviousAt)
		}
		return result[left].CurrentAt.Before(result[right].CurrentAt)
	})
	return result
}

func localTimelineIntervalPhase(previousAt, currentAt time.Time, marks localTimelineMarks) string {
	previousPhase, currentPhase := localTimelinePhase(previousAt, marks), localTimelinePhase(currentAt, marks)
	if previousPhase == currentPhase {
		return currentPhase
	}
	phases := []string{previousPhase}
	for _, candidate := range []struct {
		at    *time.Time
		phase string
	}{{marks.warmupStart, "warmup"}, {marks.measurementStart, "measured"}, {marks.drainStart, "drain"}, {marks.shutdownStart, "shutdown"}} {
		if candidate.at != nil && candidate.at.After(previousAt) && !candidate.at.After(currentAt) && phases[len(phases)-1] != candidate.phase {
			phases = append(phases, candidate.phase)
		}
	}
	if phases[len(phases)-1] != currentPhase {
		phases = append(phases, currentPhase)
	}
	return strings.Join(phases, "_to_")
}
