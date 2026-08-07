package chatlifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const RunStartReceiptSchemaV1 = "wukongim.chat_lifecycle.run_start/v1"

// RunStartReceipt proves the exact post-sync, post-first-grant workload clock
// boundary without retaining raw run or assignment identities.
type RunStartReceipt struct {
	Schema         string    `json:"schema"`
	Stage          Stage     `json:"stage"`
	StartedAt      time.Time `json:"started_at"`
	ExpectedEndAt  time.Time `json:"expected_end_at"`
	RunHash        string    `json:"run_hash"`
	AssignmentHash string    `json:"assignment_hash"`
	Generation     uint64    `json:"generation"`
}

func writeRunStartReceipt(path string, receipt RunStartReceipt) error {
	if receipt.Schema != RunStartReceiptSchemaV1 ||
		(receipt.Stage != StageFormal && receipt.Stage != StageRehearsal && receipt.Stage != StageShakeout) ||
		receipt.StartedAt.IsZero() || !receipt.ExpectedEndAt.After(receipt.StartedAt) ||
		!validReportHash(receipt.RunHash) || !validReportHash(receipt.AssignmentHash) || receipt.Generation == 0 {
		return ErrReportInvalid
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".run-start.tmp-*")
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
