package management

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrApplicationLogReaderUnavailable reports that ordinary application log inspection is not wired.
var ErrApplicationLogReaderUnavailable = errors.New("internal/usecase/management: application log reader unavailable")

// ApplicationLogReader exposes selected-node ordinary application log sources and entries.
type ApplicationLogReader interface {
	// ApplicationLogSources returns the ordinary application log sources available on one node.
	ApplicationLogSources(context.Context, ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error)
	// ApplicationLogEntries returns one page from a selected ordinary application log source.
	ApplicationLogEntries(context.Context, ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error)
}

// ApplicationLogSourcesRequest selects one node's ordinary application log sources.
type ApplicationLogSourcesRequest struct {
	// NodeID is the node whose ordinary application log sources should be listed.
	NodeID uint64
}

// ApplicationLogSourcesResponse contains ordinary application log sources for one node.
type ApplicationLogSourcesResponse struct {
	// NodeID is the node whose ordinary application log sources were listed.
	NodeID uint64
	// Sources contains available and unavailable ordinary application log sources.
	Sources []ApplicationLogSource
}

// ApplicationLogSource describes one ordinary application log source.
type ApplicationLogSource struct {
	// Name is the stable manager-facing source identifier.
	Name string
	// File is the display file name or reader-owned file label.
	File string
	// Available reports whether the source can currently be read.
	Available bool
	// SizeBytes is the current source size in bytes when known.
	SizeBytes int64
	// ModifiedAt is the current source modification timestamp when known.
	ModifiedAt time.Time
}

// ApplicationLogEntriesRequest selects one page from an ordinary application log source.
type ApplicationLogEntriesRequest struct {
	// NodeID is the node whose ordinary application log source should be read.
	NodeID uint64
	// Source is the reader-owned ordinary application log source identifier.
	Source string
	// Limit is the maximum number of entries to return. Zero lets the reader use its default.
	Limit int
	// Cursor is the reader-owned pagination cursor.
	Cursor string
	// Keyword filters entries by message or raw text when supported by the reader.
	Keyword string
	// Levels filters entries by normalized log levels when supported by the reader.
	Levels []string
}

// ApplicationLogEntriesResponse contains one ordinary application log page.
type ApplicationLogEntriesResponse struct {
	// NodeID is the node whose ordinary application log source was read.
	NodeID uint64
	// Source is the reader-owned ordinary application log source identifier.
	Source string
	// Cursor is the cursor for the next page. Empty means no further cursor is available.
	Cursor string
	// Rotated reports whether the source rotated while or before this page was read.
	Rotated bool
	// Items contains ordinary application log entries in reader-defined order.
	Items []ApplicationLogEntry
}

// ApplicationLogEntry is a manager-facing ordinary application log entry.
type ApplicationLogEntry struct {
	// Seq is a reader-owned monotonically comparable entry sequence.
	Seq uint64
	// Offset is the byte offset of the entry in the source when known.
	Offset uint64
	// Time is the parsed log timestamp when available.
	Time time.Time
	// Level is the normalized log level.
	Level string
	// Module is the parsed logger or module name.
	Module string
	// Caller is the parsed caller location.
	Caller string
	// Message is the parsed human-readable log message.
	Message string
	// Fields contains structured log fields that are safe for manager display.
	Fields map[string]any
	// Raw is the original entry text or a reader-owned raw representation.
	Raw string
	// Truncated reports whether Raw or Fields were shortened for transport.
	Truncated bool
}

// ApplicationLogSources returns ordinary application log sources for one selected node.
func (a *App) ApplicationLogSources(ctx context.Context, req ApplicationLogSourcesRequest) (ApplicationLogSourcesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplicationLogSourcesResponse{}, err
	}
	if req.NodeID == 0 {
		return ApplicationLogSourcesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.applicationLogs == nil {
		return ApplicationLogSourcesResponse{}, ErrApplicationLogReaderUnavailable
	}
	return a.applicationLogs.ApplicationLogSources(ctx, req)
}

// ApplicationLogEntries returns one page from an ordinary application log source.
func (a *App) ApplicationLogEntries(ctx context.Context, req ApplicationLogEntriesRequest) (ApplicationLogEntriesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplicationLogEntriesResponse{}, err
	}
	if req.NodeID == 0 || req.Limit < 0 {
		return ApplicationLogEntriesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.applicationLogs == nil {
		return ApplicationLogEntriesResponse{}, ErrApplicationLogReaderUnavailable
	}
	return a.applicationLogs.ApplicationLogEntries(ctx, req)
}
