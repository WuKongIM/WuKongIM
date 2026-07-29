package backup

import (
	"context"
	"io"
	"time"
)

// PutObject describes one bounded repository write.
type PutObject struct {
	Key           string
	Body          io.Reader
	ExpectedBytes uint64
	IfAbsent      bool
}

// ArchiveObject describes one repository object without exposing payload.
type ArchiveObject struct {
	Key      string    `json:"key"`
	Bytes    uint64    `json:"bytes"`
	Modified time.Time `json:"modified"`
}

// ArchiveStore is the single-repository boundary used by scheduled full
// backup. Implementations must keep keys repository-relative and ordered.
type ArchiveStore interface {
	Put(context.Context, PutObject) error
	Open(context.Context, string) (io.ReadCloser, ArchiveObject, error)
	List(context.Context, string) ([]ArchiveObject, error)
	Delete(context.Context, string) error
	DeletePrefix(context.Context, string) error
}

// ValidateRepositoryKey rejects absolute, traversal, and non-canonical keys.
func ValidateRepositoryKey(key string) error {
	return validateRepositoryKey(key)
}
