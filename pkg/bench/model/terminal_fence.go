package model

import "fmt"

// TerminalFenceVersion is the closed target handshake schema understood by
// the native single-node cluster diagnostic.
const TerminalFenceVersion = "wukongim/bench-terminal-fence/v1"

// TerminalFencePrepareRequest asks one exact target process generation to
// close and drain its product write path before issuing a session capability.
type TerminalFencePrepareRequest struct {
	RunID            string `json:"run_id"`
	AssignmentID     string `json:"assignment_id"`
	ExpectedSessions int    `json:"expected_sessions"`
}

// TerminalFenceGrant is returned only after the target product path has
// drained. Capability is an opaque secret and must never enter diagnostics.
type TerminalFenceGrant struct {
	Version          string `json:"version"`
	RunID            string `json:"run_id"`
	AssignmentID     string `json:"assignment_id"`
	ExpectedSessions int    `json:"expected_sessions"`
	Epoch            uint64 `json:"epoch"`
	Capability       string `json:"capability"`
}

// String keeps the run-scoped capability out of ordinary diagnostics.
func (g TerminalFenceGrant) String() string {
	return fmt.Sprintf("terminal-fence-grant{version:%s run_id:%s assignment_id:%s expected_sessions:%d epoch:%d capability:[redacted]}",
		g.Version, g.RunID, g.AssignmentID, g.ExpectedSessions, g.Epoch)
}

// GoString keeps the capability out of %#v diagnostics as well.
func (g TerminalFenceGrant) GoString() string { return g.String() }
