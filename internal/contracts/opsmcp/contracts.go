// Package opsmcp contains bounded internal RPC contracts for operations MCP.
package opsmcp

import (
	"context"
	"time"
)

type cacheObserverContextKey struct{}

// WithCacheObserver attaches a per-call cache-hit observer without coupling
// runtime admission to the observation usecase.
func WithCacheObserver(ctx context.Context, observer func(bool)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, cacheObserverContextKey{}, observer)
}

// ReportCacheHit reports one cache decision when an observer is attached.
func ReportCacheHit(ctx context.Context, hit bool) {
	if observer, ok := ctx.Value(cacheObserverContextKey{}).(func(bool)); ok {
		observer(hit)
	}
}

const (
	// RPCVersion is the only internal operations MCP wire version.
	RPCVersion uint8 = 1
	// MaxForwardRequestBytes is the maximum MCP JSON request payload.
	MaxForwardRequestBytes = 64 << 10
	// MaxForwardResponseBytes is the maximum MCP JSON response payload.
	MaxForwardResponseBytes = 1 << 20
	// MaxProfileBytes is the maximum in-memory raw profile accepted for parsing.
	MaxProfileBytes = 32 << 20
	// MaxAuditEntries is the Manager-visible aggregate audit page bound.
	MaxAuditEntries = 200
)

// ForwardRequest carries an authenticated MCP request from an ingress Manager
// node to the configured execution owner. It never contains the raw token.
type ForwardRequest struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// RequestID correlates ingress and owner execution.
	RequestID string `json:"request_id"`
	// IngressNodeID identifies the accepting Manager node.
	IngressNodeID uint64 `json:"ingress_node_id"`
	// CredentialID is the non-secret credential identifier.
	CredentialID string `json:"credential_id"`
	// DigestSHA256 proves possession without forwarding the raw token.
	DigestSHA256 string `json:"digest_sha256"`
	// ExpectedRevision fences owner execution to the ingress state snapshot.
	ExpectedRevision uint64 `json:"expected_revision"`
	// ContentType preserves the bounded MCP request media type.
	ContentType string `json:"content_type"`
	// Accept preserves the bounded MCP response preference.
	Accept string `json:"accept"`
	// ProtocolVersion carries the optional negotiated MCP version header.
	ProtocolVersion string `json:"protocol_version,omitempty"`
	// Payload is the bounded JSON request body.
	Payload []byte `json:"payload"`
}

// ForwardResponse returns the owner's complete bounded HTTP response.
type ForwardResponse struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// StatusCode is the complete owner HTTP status.
	StatusCode int `json:"status_code"`
	// ContentType is the owner response media type.
	ContentType string `json:"content_type"`
	// Payload is the bounded owner response body.
	Payload []byte `json:"payload"`
}

// ProfileRequest asks one target node for an in-memory runtime profile. Only
// the configured owner may issue this contract.
type ProfileRequest struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// OwnerNodeID identifies the configured execution owner.
	OwnerNodeID uint64 `json:"owner_node_id"`
	// ExpectedRevision fences target capture to the owner's state snapshot.
	ExpectedRevision uint64 `json:"expected_revision"`
	// NodeID is the exact target node.
	NodeID uint64 `json:"node_id"`
	// Kind is cpu, heap, or goroutine.
	Kind string `json:"kind"`
	// Seconds is the bounded CPU sampling duration.
	Seconds int `json:"seconds"`
	// LeaseID is the owner-generated one-time proof authorizing this capture.
	LeaseID string `json:"lease_id"`
}

// ProfileResponse carries an ephemeral profile only across the trusted
// internal RPC. The owner parses and discards it before returning MCP output.
type ProfileResponse struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// Payload is the ephemeral bounded raw profile.
	Payload []byte `json:"payload"`
}

// ProfileLeaseRequest asks the configured owner to consume one exact,
// short-lived profile authorization before the target starts capture.
type ProfileLeaseRequest struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// OwnerNodeID identifies the configured execution owner.
	OwnerNodeID uint64 `json:"owner_node_id"`
	// ExpectedRevision fences authorization to one desired-state generation.
	ExpectedRevision uint64 `json:"expected_revision"`
	// TargetNodeID identifies the node that must consume the lease.
	TargetNodeID uint64 `json:"target_node_id"`
	// LeaseID is the random one-time owner-held proof.
	LeaseID string `json:"lease_id"`
}

// ProfileLeaseResponse confirms that the owner consumed the exact lease.
type ProfileLeaseResponse struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// Allowed is true only after successful one-time consumption.
	Allowed bool `json:"allowed"`
}

// AuditRequest asks one node for recent local non-secret summaries.
type AuditRequest struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// Limit bounds the newest-first local page.
	Limit int `json:"limit"`
}

// AuditResponse returns detached local audit summaries.
type AuditResponse struct {
	// Version is the internal contract version.
	Version uint8 `json:"version"`
	// Entries are newest-first on the source node.
	Entries []AuditEntry `json:"entries"`
}

// AuditTarget contains only bounded, non-secret target selectors.
type AuditTarget struct {
	// NodeID is the optional exact target node.
	NodeID uint64 `json:"node_id,omitempty"`
	// SlotID is the optional exact physical Slot.
	SlotID uint32 `json:"slot_id,omitempty"`
	// ChannelType is the optional bounded channel type.
	ChannelType uint8 `json:"channel_type,omitempty"`
}

// AuditEntry is one local non-secret MCP authentication or execution summary.
type AuditEntry struct {
	// RequestID correlates one public request.
	RequestID string `json:"request_id"`
	// RecorderNodeID identifies the node that retained this summary.
	RecorderNodeID uint64 `json:"recorder_node_id,omitempty"`
	// Phase is ingress for forwarding decisions or owner for tool execution.
	Phase string `json:"phase"`
	// IngressNodeID is the Manager node that accepted the public request.
	IngressNodeID uint64 `json:"ingress_node_id,omitempty"`
	// OwnerNodeID is the configured execution owner for this request.
	OwnerNodeID uint64 `json:"owner_node_id,omitempty"`
	// CredentialID is the non-secret verified credential identity.
	CredentialID string `json:"credential_id,omitempty"`
	// Tool is one frozen low-cardinality tool name.
	Tool string `json:"tool,omitempty"`
	// Target contains only bounded selectors.
	Target AuditTarget `json:"target"`
	// StartedAt records local admission time.
	StartedAt time.Time `json:"started_at"`
	// DurationMS is the bounded execution duration.
	DurationMS int64 `json:"duration_ms"`
	// Result is the stable completion class.
	Result string `json:"result"`
	// ResponseBytes is the encoded response size.
	ResponseBytes int64 `json:"response_bytes"`
	// CacheHit reports reuse of a short-lived observation.
	CacheHit bool `json:"cache_hit"`
	// PprofKind is the optional supported profile kind.
	PprofKind string `json:"pprof_kind,omitempty"`
	// PprofSeconds is the bounded requested CPU sampling duration.
	PprofSeconds int `json:"pprof_seconds,omitempty"`
}
