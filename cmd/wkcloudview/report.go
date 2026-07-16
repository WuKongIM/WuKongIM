package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	benchreport "github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/runtime/cloudviewstate"
)

const maxReportBytes = 16 << 20

// benchmarkPurity records whether public Cloud View use affected the run.
type benchmarkPurity struct {
	// Pure is true only when current healthy state proves no interaction.
	Pure bool `json:"pure"`
	// StateKnown reports whether live Cloud View state was available and matched.
	StateKnown bool `json:"state_known"`
	// PersistenceHealthy reports whether every state projection is durable.
	PersistenceHealthy bool `json:"persistence_healthy"`
	// Interactive reports successful Demo or public WebSocket use.
	Interactive bool `json:"interactive"`
	// OperatorModified reports a successful non-login Manager write.
	OperatorModified bool `json:"operator_modified"`
	// EvaluatedAt records when the report annotation was attempted.
	EvaluatedAt string `json:"evaluated_at"`
}

type cloudViewStatus struct {
	cloudviewstate.State
	PersistenceHealthy bool `json:"persistence_healthy"`
}

func annotateReport(ctx context.Context, statusURL, reportPath string) error {
	file, err := os.Open(reportPath)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxReportBytes+1))
	var report map[string]json.RawMessage
	decodeErr := decoder.Decode(&report)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	closeErr := file.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if !errors.Is(trailingErr, io.EOF) {
		return errors.New("benchmark report contains trailing data")
	}
	if closeErr != nil {
		return closeErr
	}
	var runID string
	if err := json.Unmarshal(report["run_id"], &runID); err != nil || runID == "" {
		return errors.New("benchmark report lacks a valid run identity")
	}
	status, statusErr := readCloudViewStatus(ctx, statusURL)
	stateKnown := statusErr == nil && status.RunID == runID
	if statusErr == nil && !stateKnown {
		statusErr = errors.New("benchmark report and Cloud View status identity mismatch")
	}
	persistenceHealthy := stateKnown && status.PersistenceHealthy
	purity, err := json.Marshal(benchmarkPurity{
		Pure:       stateKnown && persistenceHealthy && !status.Interactive && !status.OperatorModified,
		StateKnown: stateKnown, PersistenceHealthy: persistenceHealthy,
		Interactive: status.Interactive, OperatorModified: status.OperatorModified,
		EvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	report["benchmark_purity"] = purity
	var stabilityVerdict benchreport.StabilityVerdict
	if status.OperatorModified && stateKnown {
		stabilityVerdict = benchreport.VerdictOperatorModified
	} else if !stateKnown || !persistenceHealthy {
		stabilityVerdict = benchreport.VerdictInsufficientEvidence
	}
	if stabilityVerdict != "" {
		encodedVerdict, err := json.Marshal(stabilityVerdict)
		if err != nil {
			return err
		}
		report["stability_verdict"] = encodedVerdict
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	info, err := os.Stat(reportPath)
	if err != nil {
		return err
	}
	if err := replaceFile(reportPath, body, info.Mode().Perm()); err != nil {
		return errors.Join(statusErr, err)
	}
	if statusErr != nil {
		return fmt.Errorf("read live Cloud View status: %w", statusErr)
	}
	if !persistenceHealthy {
		return errors.New("Cloud View state persistence is degraded")
	}
	return nil
}

func readCloudViewStatus(ctx context.Context, rawURL string) (cloudViewStatus, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return cloudViewStatus{}, errors.New("status URL must be a loopback Cloud View status endpoint")
	}
	address := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || address == nil || !address.IsLoopback() ||
		parsed.Path != "/cloud-view/status" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return cloudViewStatus{}, errors.New("status URL must be a loopback Cloud View status endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return cloudViewStatus{}, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return cloudViewStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return cloudViewStatus{}, fmt.Errorf("status HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, (64<<10)+1))
	decoder.DisallowUnknownFields()
	var status cloudViewStatus
	if err := decoder.Decode(&status); err != nil {
		return cloudViewStatus{}, err
	}
	if status.RunID == "" || status.UpdatedAt.IsZero() {
		return cloudViewStatus{}, errors.New("Cloud View status is incomplete")
	}
	return status, nil
}

func replaceFile(path string, body []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
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
	return os.Rename(temporaryPath, path)
}
