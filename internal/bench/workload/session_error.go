package workload

import (
	"errors"
	"sort"
	"strings"
)

var safeSessionOperations = map[string]struct{}{
	"person sendack lock": {},
	"person send":         {},
	"person sendack":      {},
	"person recv":         {},
	"person recvack":      {},
	"group sendack lock":  {},
	"group send":          {},
	"group sendack":       {},
	"group recv":          {},
	"group recvack":       {},
}

// SessionError identifies a workload error that is tied to a specific online session.
type SessionError struct {
	// UID is the connected user whose session produced the error.
	UID string
	// Operation describes the session operation that failed.
	Operation string
	// Err is the underlying workload or transport error.
	Err error
}

func (e *SessionError) Error() string {
	if e == nil {
		return ""
	}
	op := strings.TrimSpace(e.Operation)
	if _, ok := safeSessionOperations[op]; !ok {
		return "session operation failed"
	}
	return op + ": session operation failed"
}

// Unwrap returns the underlying workload or transport error.
func (e *SessionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SessionErrorUIDs returns unique UIDs carried by SessionError values in err.
func SessionErrorUIDs(err error) []string {
	if err == nil {
		return nil
	}
	seen := make(map[string]struct{})
	collectSessionErrorUIDs(err, seen)
	uids := make([]string, 0, len(seen))
	for uid := range seen {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids
}

func collectSessionErrorUIDs(err error, seen map[string]struct{}) {
	if err == nil {
		return
	}
	var sessionErr *SessionError
	if errors.As(err, &sessionErr) && sessionErr != nil {
		uid := strings.TrimSpace(sessionErr.UID)
		if uid != "" {
			seen[uid] = struct{}{}
		}
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range unwrapped.Unwrap() {
			collectSessionErrorUIDs(child, seen)
		}
	case interface{ Unwrap() error }:
		collectSessionErrorUIDs(unwrapped.Unwrap(), seen)
	}
}

func sessionOperationError(uid, operation string, err error) error {
	if err == nil {
		return nil
	}
	return &SessionError{UID: uid, Operation: operation, Err: err}
}
