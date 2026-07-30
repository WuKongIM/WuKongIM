package reviewagentverify

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

const maxLedgerRecordBytes = 1 << 20

// LedgerRecord is one append-only hash-chained named-check result.
type LedgerRecord struct {
	Sequence       uint64                      `json:"sequence"`
	PreviousDigest string                      `json:"previous_digest"`
	Generation     contract.GenerationIdentity `json:"generation"`
	Evidence       contract.CheckEvidence      `json:"evidence"`
	CreatedAt      time.Time                   `json:"created_at"`
}

// EvidenceLedger is the trusted persistence boundary outside the model
// workspace.
type EvidenceLedger interface {
	Append(contract.GenerationIdentity, contract.CheckEvidence) error
	List(contract.GenerationIdentity) ([]LedgerRecord, error)
}

// FileLedger stores canonical JSON Lines outside the model-writable tree.
type FileLedger struct {
	path string
	mu   sync.Mutex
}

// NewFileLedger rejects a ledger path inside the candidate workspace.
func NewFileLedger(pathValue, workspaceRoot string) (*FileLedger, error) {
	absolutePath, err := filepath.Abs(pathValue)
	if err != nil {
		return nil, errors.New("resolve evidence ledger path")
	}
	absoluteRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, errors.New("resolve model workspace path")
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil {
		return nil, errors.New("compare evidence ledger path")
	}
	if relative == "." ||
		relative != ".." &&
			!filepath.IsAbs(relative) &&
			!startsWithParent(relative) {
		return nil, errors.New("evidence ledger must be outside workspace")
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return nil, errors.New("create evidence ledger directory")
	}
	return &FileLedger{path: absolutePath}, nil
}

// Append adds one record after validating the complete existing chain.
func (ledger *FileLedger) Append(
	generation contract.GenerationIdentity,
	evidence contract.CheckEvidence,
) error {
	if ledger == nil {
		return errors.New("evidence ledger is unavailable")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()

	records, bodies, err := ledger.readAll()
	if err != nil {
		return err
	}
	previousDigest := ""
	if len(bodies) > 0 {
		previousDigest = bytesDigest(bodies[len(bodies)-1])
	}
	record := LedgerRecord{
		Sequence:       uint64(len(records) + 1),
		PreviousDigest: previousDigest,
		Generation:     generation,
		Evidence:       evidence,
		CreatedAt:      time.Now().UTC(),
	}
	if err := validateLedgerRecord(record); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return errors.New("encode evidence ledger record")
	}
	file, err := os.OpenFile(
		ledger.path,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return errors.New("open evidence ledger")
	}
	defer file.Close()
	if _, err := file.Write(append(body, '\n')); err != nil {
		return errors.New("append evidence ledger")
	}
	return file.Sync()
}

// List validates the full chain and returns records for one generation.
func (ledger *FileLedger) List(
	generation contract.GenerationIdentity,
) ([]LedgerRecord, error) {
	if ledger == nil {
		return nil, errors.New("evidence ledger is unavailable")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()

	records, _, err := ledger.readAll()
	if err != nil {
		return nil, err
	}
	target := contract.MustGenerationDigest(generation)
	result := make([]LedgerRecord, 0, len(records))
	for _, record := range records {
		if contract.MustGenerationDigest(record.Generation) == target {
			result = append(result, record)
		}
	}
	return result, nil
}

func (ledger *FileLedger) readAll() (
	[]LedgerRecord,
	[][]byte,
	error,
) {
	file, err := os.Open(ledger.path)
	if errors.Is(err, os.ErrNotExist) {
		return []LedgerRecord{}, [][]byte{}, nil
	}
	if err != nil {
		return nil, nil, errors.New("open evidence ledger")
	}
	defer file.Close()

	records := make([]LedgerRecord, 0)
	bodies := make([][]byte, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxLedgerRecordBytes)
	for scanner.Scan() {
		body := append([]byte(nil), scanner.Bytes()...)
		var record LedgerRecord
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, nil, errors.New("decode evidence ledger")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err == nil {
			return nil, nil, errors.New("evidence ledger has trailing JSON")
		}
		if err := validateLedgerRecord(record); err != nil {
			return nil, nil, err
		}
		if record.Sequence != uint64(len(records)+1) {
			return nil, nil, errors.New("evidence ledger sequence is discontinuous")
		}
		expectedPrevious := ""
		if len(bodies) > 0 {
			expectedPrevious = bytesDigest(bodies[len(bodies)-1])
		}
		if record.PreviousDigest != expectedPrevious {
			return nil, nil, errors.New("evidence ledger chain is discontinuous")
		}
		records = append(records, record)
		bodies = append(bodies, body)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, errors.New("read evidence ledger")
	}
	return records, bodies, nil
}

func validateLedgerRecord(record LedgerRecord) error {
	if record.Sequence == 0 {
		return errors.New("invalid evidence ledger sequence")
	}
	if record.Sequence == 1 && record.PreviousDigest != "" ||
		record.Sequence > 1 && len(record.PreviousDigest) != 71 {
		return errors.New("invalid evidence ledger predecessor")
	}
	if err := contract.ValidateGenerationIdentity(record.Generation); err != nil {
		return err
	}
	evidence := contract.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    record.Generation,
		Complete:      true,
		Checks:        []contract.CheckEvidence{record.Evidence},
		CreatedAt:     record.CreatedAt,
	}
	if err := contract.ValidateReviewEvidence(evidence); err != nil {
		return err
	}
	return nil
}

func startsWithParent(value string) bool {
	return value == ".." ||
		len(value) > 3 &&
			value[:3] == ".."+string(filepath.Separator)
}
