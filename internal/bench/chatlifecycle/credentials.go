package chatlifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	benchTokenEnvironment      = "WK_BENCH_API_TOKEN"
	benchTokenFileEnvironment  = "WK_CHAT_LIFECYCLE_BENCH_TOKEN_FILE"
	workerTokenEnvironment     = "WK_BENCH_WORKER_TOKEN"
	workerTokenFileEnvironment = "WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE"
)

// Credentials keeps control-plane secrets outside Config, assignments, and
// persisted reports. Every generic rendering is deliberately redacted.
type Credentials struct {
	benchToken  string
	workerToken string
}

// LoadCredentials resolves each required token from either its direct
// environment variable or one owner-only file, never both.
func LoadCredentials() (Credentials, error) {
	benchToken, err := loadCredential(benchTokenEnvironment, benchTokenFileEnvironment)
	if err != nil {
		return Credentials{}, err
	}
	workerToken, err := loadCredential(workerTokenEnvironment, workerTokenFileEnvironment)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{benchToken: benchToken, workerToken: workerToken}, nil
}

func loadCredential(valueEnvironment, fileEnvironment string) (string, error) {
	value := strings.TrimSpace(os.Getenv(valueEnvironment))
	path := strings.TrimSpace(os.Getenv(fileEnvironment))
	if value != "" && path != "" {
		return "", fmt.Errorf("%s and %s are mutually exclusive", valueEnvironment, fileEnvironment)
	}
	if value != "" {
		return value, nil
	}
	if path == "" {
		return "", fmt.Errorf("%s or %s is required", valueEnvironment, fileEnvironment)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s must reference an owner-only regular file", fileEnvironment)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s credential file", fileEnvironment)
	}
	value = strings.TrimSpace(string(encoded))
	if value == "" {
		return "", fmt.Errorf("%s credential file is empty", fileEnvironment)
	}
	return value, nil
}

// BenchToken returns the protected service observation token to production adapters.
func (c Credentials) BenchToken() string { return c.benchToken }

// WorkerToken returns the shared private worker-control token to production adapters.
func (c Credentials) WorkerToken() string { return c.workerToken }

func (Credentials) String() string   { return "[REDACTED]" }
func (Credentials) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents accidental credential disclosure through diagnostics.
func (Credentials) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }
