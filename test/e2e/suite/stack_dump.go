//go:build e2e

package suite

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// goroutineDumpFile is written next to stderr, so any NodeProcess built
	// from the same NodeSpec finds it without extra state.
	goroutineDumpFile = "goroutines.txt"
	// goroutineSummaryGroups bounds the diagnostics summary for CI logs.
	goroutineSummaryGroups = 30
	// productFramePrefix selects the first repository frame as a group key.
	productFramePrefix = "github.com/WuKongIM/WuKongIM/"
)

// goroutineDumpWait bounds how long capture waits for the Go runtime to write
// its SIGQUIT dump and exit. Tests may shorten it.
var goroutineDumpWait = 5 * time.Second

// goroutineDumpPath returns where a node's captured goroutine dump is stored.
func goroutineDumpPath(spec NodeSpec) string {
	return filepath.Join(filepath.Dir(spec.StderrPath), goroutineDumpFile)
}

// CaptureGoroutineDump asks a still-running product process for a full
// goroutine dump through SIGQUIT, which the Go runtime answers by writing all
// stacks to stderr and exiting. It is a last step for a node that already
// missed its readiness deadline: the node does not survive capture. The dump
// is copied from stderr into goroutineDumpPath and summarized by diagnostics.
func (p *NodeProcess) CaptureGoroutineDump() (string, error) {
	if p == nil || !p.Running() {
		return "", errors.New("goroutine dump: process is not running")
	}
	path := goroutineDumpPath(p.Spec)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	p.mu.RLock()
	cmd := p.Cmd
	p.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return "", errors.New("goroutine dump: process is not started")
	}
	offset := int64(0)
	if info, err := os.Stat(p.Spec.StderrPath); err == nil {
		offset = info.Size()
	}
	if err := signalGoroutineDump(cmd.Process); err != nil {
		return "", fmt.Errorf("goroutine dump: signal: %w", err)
	}
	select {
	case <-p.Done():
	case <-time.After(goroutineDumpWait):
		return "", fmt.Errorf("goroutine dump: process did not exit within %s after SIGQUIT", goroutineDumpWait)
	}
	data, err := readFrom(p.Spec.StderrPath, offset)
	if err != nil {
		return "", fmt.Errorf("goroutine dump: read stderr: %w", err)
	}
	if !bytes.Contains(data, []byte("goroutine ")) {
		return "", errors.New("goroutine dump: stderr has no goroutine stacks")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("goroutine dump: write: %w", err)
	}
	return path, nil
}

// captureStuckNodeForDiagnostics records a dump for a node that is still
// running after a readiness failure; capture problems never mask that failure.
func captureStuckNodeForDiagnostics(process *NodeProcess) {
	if process != nil && process.Running() {
		_, _ = process.CaptureGoroutineDump()
	}
}

func readFrom(path string, offset int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	_, err = buf.ReadFrom(file)
	return buf.Bytes(), err
}

// appendGoroutineDumpSummary adds the dump path and a bounded grouped summary.
func appendGoroutineDumpSummary(b *strings.Builder, spec NodeSpec) {
	path := goroutineDumpPath(spec)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fmt.Fprintf(b, "goroutine-dump: %s\n", path)
	fmt.Fprintf(b, "goroutine-summary (count [state] first product frame):\n")
	for _, line := range summarizeGoroutineDump(data, goroutineSummaryGroups) {
		fmt.Fprintf(b, "%s\n", line)
	}
}

// summarizeGoroutineDump groups goroutines by wait state and their first
// repository frame (or first frame), most frequent first, at most limit groups.
func summarizeGoroutineDump(data []byte, limit int) []string {
	type group struct {
		key   string
		count int
	}
	counts := make(map[string]int)
	var order []string
	add := func(state, frame string) {
		if state == "" {
			return
		}
		if frame == "" {
			frame = "<unknown>"
		}
		key := fmt.Sprintf("[%s]  %s", state, frame)
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
	}

	var state, first, product string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, ":"):
			if product != "" {
				first = product
			}
			add(state, first)
			state, first, product = goroutineState(line), "", ""
		case state == "" || line == "" || strings.HasPrefix(line, "\t"):
		default:
			frame := line
			if i := strings.LastIndex(frame, "("); i > 0 {
				frame = frame[:i]
			}
			frame = strings.TrimPrefix(frame, "created by ")
			if first == "" {
				first = frame
			}
			if product == "" && strings.HasPrefix(frame, productFramePrefix) {
				product = frame
			}
		}
	}
	if product != "" {
		first = product
	}
	add(state, first)

	groups := make([]group, 0, len(order))
	for _, key := range order {
		groups = append(groups, group{key: key, count: counts[key]})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].count > groups[j].count })
	lines := make([]string, 0, limit+1)
	for i, g := range groups {
		if i == limit {
			lines = append(lines, fmt.Sprintf("   …  %d more groups omitted", len(groups)-limit))
			break
		}
		lines = append(lines, fmt.Sprintf("%4d  %s", g.count, g.key))
	}
	return lines
}

// goroutineState extracts "semacquire" from "goroutine 7 [semacquire, 2 minutes]:".
func goroutineState(header string) string {
	start := strings.Index(header, "[")
	end := strings.Index(header, "]")
	if start < 0 || end <= start {
		return "unknown"
	}
	state := header[start+1 : end]
	if i := strings.Index(state, ","); i >= 0 {
		state = state[:i]
	}
	return state
}
