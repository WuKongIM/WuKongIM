package management

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

const (
	opsMCPIdempotencyTTL = 5 * time.Minute
	opsMCPOldTokenAge    = 90 * 24 * time.Hour
	opsMCPProfileFence   = 30 * time.Second
)

var (
	// ErrOpsMCPUnavailable reports missing or failed MCP control dependencies.
	ErrOpsMCPUnavailable = errors.New("operations MCP unavailable")
	// ErrOpsMCPInvalidRequest reports malformed management input.
	ErrOpsMCPInvalidRequest = errors.New("invalid operations MCP request")
	// ErrOpsMCPConflict reports a revision or desired-state conflict.
	ErrOpsMCPConflict = errors.New("operations MCP conflict")
	// ErrOpsMCPTokenNotFound reports an unknown credential identifier.
	ErrOpsMCPTokenNotFound = errors.New("operations MCP token not found")
)

// OpsMCPStatus is the manager-facing desired-state snapshot.
type OpsMCPStatus struct {
	// ClusterID is the durable cluster identity.
	ClusterID string
	// Revision is the global Controller state revision.
	Revision uint64
	// Enabled is the desired MCP serving state.
	Enabled bool
	// ObservedStatus is stopped, ready, or unavailable from locally visible control state.
	ObservedStatus string
	// OwnerNodeID is the single MCP execution owner.
	OwnerNodeID uint64
	// OwnerCandidates lists nodes eligible to become the execution owner.
	OwnerCandidates []OpsMCPOwnerCandidate
	// Credentials lists non-secret token metadata.
	Credentials []OpsMCPCredentialStatus
	// Warnings contains bounded operator guidance.
	Warnings []string
}

// OpsMCPOwnerCandidate is a safe node selector exposed under cluster.mcp:r.
type OpsMCPOwnerCandidate struct {
	// NodeID is the stable cluster node identity.
	NodeID uint64
	// Status is the bounded Controller health status.
	Status string
}

// OpsMCPCredentialStatus is non-secret token metadata shown by Manager.
type OpsMCPCredentialStatus struct {
	// ID is the credential identifier.
	ID string
	// CreatedAtUnixMillis records token creation.
	CreatedAtUnixMillis int64
	// Old reports whether the token exceeded the built-in rotation reminder age.
	Old bool
}

// OpsMCPTokenCreateRequest creates one opaque token.
type OpsMCPTokenCreateRequest struct {
	// ExpectedRevision is the Controller compare-and-set fence.
	ExpectedRevision uint64
	// IdempotencyKey deduplicates retries of the one-time token response.
	IdempotencyKey string
}

// OpsMCPTokenCreateResponse returns the raw token exactly once.
type OpsMCPTokenCreateResponse struct {
	// CredentialID is the non-secret token identifier.
	CredentialID string
	// Token is the one-time opaque bearer token.
	Token string
	// CreatedAtUnixMillis records token creation.
	CreatedAtUnixMillis int64
	// Revision is the expected Controller revision after the successful write.
	Revision uint64
}

// OpsMCPTokenRevokeRequest revokes one credential.
type OpsMCPTokenRevokeRequest struct {
	// ExpectedRevision is the Controller compare-and-set fence.
	ExpectedRevision uint64
	// IdempotencyKey deduplicates mutation retries.
	IdempotencyKey string
	// CredentialID is the non-secret token identifier to remove.
	CredentialID string
}

// OpsMCPOwnerUpdateRequest selects the single execution owner.
type OpsMCPOwnerUpdateRequest struct {
	// ExpectedRevision is the Controller compare-and-set fence.
	ExpectedRevision uint64
	// IdempotencyKey deduplicates mutation retries.
	IdempotencyKey string
	// OwnerNodeID selects one active cluster node.
	OwnerNodeID uint64
}

// OpsMCPStateMutationRequest starts or stops MCP.
type OpsMCPStateMutationRequest struct {
	// ExpectedRevision is the Controller compare-and-set fence.
	ExpectedRevision uint64
	// IdempotencyKey deduplicates mutation retries.
	IdempotencyKey string
}

// OpsMCPAuditEntry is one bounded non-secret MCP audit summary.
type OpsMCPAuditEntry struct {
	// RequestID correlates one public request.
	RequestID string
	// RecorderNodeID identifies the node that retained this summary.
	RecorderNodeID uint64
	// Phase is ingress or owner.
	Phase string
	// IngressNodeID identifies the accepting Manager node.
	IngressNodeID uint64
	// OwnerNodeID identifies the configured execution owner.
	OwnerNodeID uint64
	// CredentialID identifies the non-secret credential record.
	CredentialID string
	// Tool is one frozen low-cardinality tool name.
	Tool string
	// NodeID is the optional exact target node.
	NodeID uint64
	// SlotID is the optional exact physical Slot.
	SlotID uint32
	// ChannelType is the optional bounded channel type.
	ChannelType uint8
	// Result is the stable execution result class.
	Result string
	// StartedAt records local admission time.
	StartedAt time.Time
	// DurationMS is the bounded execution duration.
	DurationMS int64
	// ResponseBytes is the encoded response size.
	ResponseBytes int64
	// CacheHit reports reuse of a short-lived observation.
	CacheHit bool
	// PprofKind is the optional supported profile kind.
	PprofKind string
	// PprofSeconds is the bounded requested CPU sampling duration.
	PprofSeconds int
}

type opsMCPIdempotencyEntry struct {
	operation   string
	fingerprint string
	expiresAt   time.Time
	token       *OpsMCPTokenCreateResponse
}

type opsMCPMutationState struct {
	mu      sync.Mutex
	entries map[string]opsMCPIdempotencyEntry
}

func newOpsMCPMutationState() *opsMCPMutationState {
	return &opsMCPMutationState{entries: make(map[string]opsMCPIdempotencyEntry)}
}

// OpsMCPStatus returns the latest locally visible desired state without token digests.
func (a *App) OpsMCPStatus(ctx context.Context) (OpsMCPStatus, error) {
	snapshot, err := a.opsMCPSnapshot(ctx)
	if err != nil {
		return OpsMCPStatus{}, err
	}
	now := a.now().UTC()
	status := OpsMCPStatus{
		ClusterID: snapshot.ClusterID, Revision: snapshot.Revision,
		ObservedStatus: "stopped", Warnings: []string{},
		OwnerCandidates: make([]OpsMCPOwnerCandidate, 0, len(snapshot.Nodes)),
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID != 0 && node.JoinState == control.NodeJoinStateActive && node.Status == control.NodeAlive {
			status.OwnerCandidates = append(status.OwnerCandidates, OpsMCPOwnerCandidate{
				NodeID: node.NodeID, Status: string(node.Status),
			})
		}
	}
	if snapshot.OpsMCP == nil {
		status.Credentials = []OpsMCPCredentialStatus{}
		return status, nil
	}
	status.Enabled = snapshot.OpsMCP.Enabled
	status.OwnerNodeID = snapshot.OpsMCP.OwnerNodeID
	if status.Enabled {
		status.ObservedStatus = "unavailable"
		if activeOpsMCPNode(snapshot.Nodes, status.OwnerNodeID) {
			status.ObservedStatus = "ready"
		}
	}
	status.Credentials = make([]OpsMCPCredentialStatus, 0, len(snapshot.OpsMCP.Credentials))
	for _, credential := range snapshot.OpsMCP.Credentials {
		createdAt := time.UnixMilli(credential.CreatedAtUnixMillis)
		old := credential.CreatedAtUnixMillis > 0 && now.Sub(createdAt) >= opsMCPOldTokenAge
		status.Credentials = append(status.Credentials, OpsMCPCredentialStatus{
			ID: credential.ID, CreatedAtUnixMillis: credential.CreatedAtUnixMillis, Old: old,
		})
		if old {
			status.Warnings = append(status.Warnings, "token_rotation_recommended")
		}
	}
	return status, nil
}

// CreateOpsMCPToken creates one 256-bit opaque token and persists only its digest.
func (a *App) CreateOpsMCPToken(ctx context.Context, req OpsMCPTokenCreateRequest) (OpsMCPTokenCreateResponse, error) {
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if err := validateOpsMCPMutationInput(req.ExpectedRevision, req.IdempotencyKey); err != nil {
		return OpsMCPTokenCreateResponse{}, err
	}
	state := a.mutationState()
	state.mu.Lock()
	defer state.mu.Unlock()
	now := a.now().UTC()
	fingerprint := fmt.Sprintf("%d", req.ExpectedRevision)
	if entry, found, err := state.lookup(now, req.IdempotencyKey, "create_token", fingerprint); err != nil {
		return OpsMCPTokenCreateResponse{}, err
	} else if found && entry.token != nil {
		return *entry.token, nil
	}
	snapshot, err := a.opsMCPSnapshot(ctx)
	if err != nil {
		return OpsMCPTokenCreateResponse{}, err
	}
	if err := requireOpsMCPRevision(snapshot, req.ExpectedRevision); err != nil {
		return OpsMCPTokenCreateResponse{}, err
	}
	replacement := controllerOpsMCPState(snapshot.OpsMCP)
	if len(replacement.Credentials) >= controller.MaxOpsMCPCredentials {
		return OpsMCPTokenCreateResponse{}, fmt.Errorf("%w: at most two active tokens are allowed", ErrOpsMCPConflict)
	}
	credentialID, rawToken, digest, err := generateOpsMCPToken()
	if err != nil {
		return OpsMCPTokenCreateResponse{}, fmt.Errorf("%w: token generation failed", ErrOpsMCPUnavailable)
	}
	replacement.Credentials = append(replacement.Credentials, controller.OpsMCPCredential{
		ID: credentialID, DigestSHA256: digest, CreatedAtUnixMillis: now.UnixMilli(),
	})
	if err := a.replaceOpsMCPState(ctx, req.ExpectedRevision, replacement); err != nil {
		return OpsMCPTokenCreateResponse{}, err
	}
	response := OpsMCPTokenCreateResponse{
		CredentialID: credentialID, Token: rawToken, CreatedAtUnixMillis: now.UnixMilli(), Revision: req.ExpectedRevision + 1,
	}
	state.store(now, req.IdempotencyKey, "create_token", fingerprint, &response)
	return response, nil
}

// RevokeOpsMCPToken removes one credential from desired state.
func (a *App) RevokeOpsMCPToken(ctx context.Context, req OpsMCPTokenRevokeRequest) error {
	req.CredentialID = strings.TrimSpace(req.CredentialID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if err := validateOpsMCPMutationInput(req.ExpectedRevision, req.IdempotencyKey); err != nil || !validOpsMCPCredentialID(req.CredentialID) {
		return ErrOpsMCPInvalidRequest
	}
	return a.runOpsMCPMutation(ctx, req.IdempotencyKey, "revoke_token", fmt.Sprintf("%d:%s", req.ExpectedRevision, req.CredentialID), func() error {
		snapshot, err := a.opsMCPSnapshot(ctx)
		if err != nil {
			return err
		}
		if err := requireOpsMCPRevision(snapshot, req.ExpectedRevision); err != nil {
			return err
		}
		replacement := controllerOpsMCPState(snapshot.OpsMCP)
		index := -1
		for i := range replacement.Credentials {
			if replacement.Credentials[i].ID == req.CredentialID {
				index = i
				break
			}
		}
		if index < 0 {
			return ErrOpsMCPTokenNotFound
		}
		if replacement.Enabled && len(replacement.Credentials) == 1 {
			return fmt.Errorf("%w: stop MCP before revoking its last token", ErrOpsMCPConflict)
		}
		replacement.Credentials = append(replacement.Credentials[:index], replacement.Credentials[index+1:]...)
		return a.replaceOpsMCPState(ctx, req.ExpectedRevision, replacement)
	})
}

// SetOpsMCPOwner selects a new owner only while MCP is stopped.
func (a *App) SetOpsMCPOwner(ctx context.Context, req OpsMCPOwnerUpdateRequest) error {
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if err := validateOpsMCPMutationInput(req.ExpectedRevision, req.IdempotencyKey); err != nil || req.OwnerNodeID == 0 {
		return ErrOpsMCPInvalidRequest
	}
	return a.runOpsMCPMutation(ctx, req.IdempotencyKey, "set_owner", fmt.Sprintf("%d:%d", req.ExpectedRevision, req.OwnerNodeID), func() error {
		snapshot, err := a.opsMCPSnapshot(ctx)
		if err != nil {
			return err
		}
		if err := requireOpsMCPRevision(snapshot, req.ExpectedRevision); err != nil {
			return err
		}
		replacement := controllerOpsMCPState(snapshot.OpsMCP)
		if replacement.Enabled && replacement.OwnerNodeID != req.OwnerNodeID {
			return fmt.Errorf("%w: stop MCP before changing owner", ErrOpsMCPConflict)
		}
		if !activeOpsMCPNode(snapshot.Nodes, req.OwnerNodeID) {
			return fmt.Errorf("%w: owner must be an active cluster node", ErrOpsMCPConflict)
		}
		if replacement.OwnerNodeID == req.OwnerNodeID {
			return nil
		}
		replacement.OwnerNodeID = req.OwnerNodeID
		return a.replaceOpsMCPState(ctx, req.ExpectedRevision, replacement)
	})
}

// StartOpsMCP enables MCP after owner and credential prerequisites are present.
func (a *App) StartOpsMCP(ctx context.Context, req OpsMCPStateMutationRequest) error {
	return a.setOpsMCPEnabled(ctx, req, true)
}

// StopOpsMCP disables MCP while preserving owner and credentials.
func (a *App) StopOpsMCP(ctx context.Context, req OpsMCPStateMutationRequest) error {
	return a.setOpsMCPEnabled(ctx, req, false)
}

// OpsMCPAudits returns a bounded newest-first local audit page.
func (a *App) OpsMCPAudits(ctx context.Context, limit int) ([]OpsMCPAuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		return nil, ErrOpsMCPInvalidRequest
	}
	if a == nil || a.operationsManagementDeps.opsMCPAudit == nil {
		return []OpsMCPAuditEntry{}, nil
	}
	entries, err := a.operationsManagementDeps.opsMCPAudit.RecentAudits(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: audit read", ErrOpsMCPUnavailable)
	}
	out := make([]OpsMCPAuditEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, OpsMCPAuditEntry{
			RequestID: entry.RequestID, RecorderNodeID: entry.RecorderNodeID, Phase: entry.Phase,
			IngressNodeID: entry.IngressNodeID, OwnerNodeID: entry.OwnerNodeID,
			CredentialID: entry.CredentialID, Tool: entry.Tool,
			NodeID: entry.Target.NodeID, SlotID: entry.Target.SlotID, ChannelType: entry.Target.ChannelType,
			Result: entry.Result, StartedAt: entry.StartedAt, DurationMS: entry.DurationMS,
			ResponseBytes: entry.ResponseBytes, CacheHit: entry.CacheHit,
			PprofKind: entry.PprofKind, PprofSeconds: entry.PprofSeconds,
		})
	}
	return out, nil
}

func (a *App) setOpsMCPEnabled(ctx context.Context, req OpsMCPStateMutationRequest, enabled bool) error {
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if err := validateOpsMCPMutationInput(req.ExpectedRevision, req.IdempotencyKey); err != nil {
		return err
	}
	operation := "stop"
	if enabled {
		operation = "start"
	}
	return a.runOpsMCPMutation(ctx, req.IdempotencyKey, operation, fmt.Sprintf("%d", req.ExpectedRevision), func() error {
		snapshot, err := a.opsMCPSnapshot(ctx)
		if err != nil {
			return err
		}
		if err := requireOpsMCPRevision(snapshot, req.ExpectedRevision); err != nil {
			return err
		}
		replacement := controllerOpsMCPState(snapshot.OpsMCP)
		if replacement.Enabled == enabled {
			return nil
		}
		if enabled && (replacement.OwnerNodeID == 0 || len(replacement.Credentials) == 0 || !activeOpsMCPNode(snapshot.Nodes, replacement.OwnerNodeID)) {
			return fmt.Errorf("%w: owner and token are required before start", ErrOpsMCPConflict)
		}
		replacement.Enabled = enabled
		if !enabled {
			fence := a.now().UTC().Add(opsMCPProfileFence).UnixMilli()
			if replacement.ProfileFenceUntilUnixMillis < fence {
				replacement.ProfileFenceUntilUnixMillis = fence
			}
		}
		return a.replaceOpsMCPState(ctx, req.ExpectedRevision, replacement)
	})
}

func (a *App) runOpsMCPMutation(ctx context.Context, key, operation, fingerprint string, mutate func() error) error {
	state := a.mutationState()
	state.mu.Lock()
	defer state.mu.Unlock()
	now := a.now().UTC()
	if _, found, err := state.lookup(now, key, operation, fingerprint); err != nil {
		return err
	} else if found {
		return nil
	}
	if err := mutate(); err != nil {
		return err
	}
	state.store(now, key, operation, fingerprint, nil)
	return nil
}

func (a *App) mutationState() *opsMCPMutationState {
	if a.opsMCPMutations == nil {
		a.opsMCPMutations = newOpsMCPMutationState()
	}
	return a.opsMCPMutations
}

func (s *opsMCPMutationState) lookup(now time.Time, key, operation, fingerprint string) (opsMCPIdempotencyEntry, bool, error) {
	for candidate, entry := range s.entries {
		if !entry.expiresAt.After(now) {
			delete(s.entries, candidate)
		}
	}
	entry, found := s.entries[key]
	if !found {
		return opsMCPIdempotencyEntry{}, false, nil
	}
	if entry.operation != operation || entry.fingerprint != fingerprint {
		return opsMCPIdempotencyEntry{}, false, fmt.Errorf("%w: idempotency key was used for another mutation", ErrOpsMCPConflict)
	}
	return entry, true, nil
}

func (s *opsMCPMutationState) store(now time.Time, key, operation, fingerprint string, token *OpsMCPTokenCreateResponse) {
	var cloned *OpsMCPTokenCreateResponse
	if token != nil {
		value := *token
		cloned = &value
	}
	s.entries[key] = opsMCPIdempotencyEntry{
		operation: operation, fingerprint: fingerprint, expiresAt: now.Add(opsMCPIdempotencyTTL), token: cloned,
	}
}

func (a *App) opsMCPSnapshot(ctx context.Context) (control.Snapshot, error) {
	if a == nil || a.nodeManagementDeps.cluster == nil {
		return control.Snapshot{}, ErrOpsMCPUnavailable
	}
	snapshot, err := a.nodeManagementDeps.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return control.Snapshot{}, fmt.Errorf("%w: control snapshot", ErrOpsMCPUnavailable)
	}
	return snapshot, nil
}

func (a *App) replaceOpsMCPState(ctx context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	if a == nil || a.operationsManagementDeps.opsMCP == nil {
		return ErrOpsMCPUnavailable
	}
	if err := a.operationsManagementDeps.opsMCP.ReplaceOpsMCPState(ctx, expectedRevision, replacement); err != nil {
		if controller.IsExpectedRevisionMismatch(err) || errors.Is(err, controller.ErrExpectedRevisionMismatch) {
			return fmt.Errorf("%w: state revision changed", ErrOpsMCPConflict)
		}
		return fmt.Errorf("%w: control write", ErrOpsMCPUnavailable)
	}
	return nil
}

func requireOpsMCPRevision(snapshot control.Snapshot, expected uint64) error {
	if snapshot.Revision != expected {
		return fmt.Errorf("%w: expected revision %d, observed %d", ErrOpsMCPConflict, expected, snapshot.Revision)
	}
	return nil
}

func controllerOpsMCPState(state *control.OpsMCPState) controller.OpsMCPState {
	if state == nil {
		return controller.OpsMCPState{Credentials: []controller.OpsMCPCredential{}}
	}
	out := controller.OpsMCPState{
		Enabled: state.Enabled, OwnerNodeID: state.OwnerNodeID,
		ProfileFenceUntilUnixMillis: state.ProfileFenceUntilUnixMillis,
		Credentials:                 make([]controller.OpsMCPCredential, 0, len(state.Credentials)),
	}
	for _, credential := range state.Credentials {
		out.Credentials = append(out.Credentials, controller.OpsMCPCredential{
			ID: credential.ID, DigestSHA256: credential.DigestSHA256, CreatedAtUnixMillis: credential.CreatedAtUnixMillis,
		})
	}
	return out
}

func activeOpsMCPNode(nodes []control.Node, nodeID uint64) bool {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node.JoinState == control.NodeJoinStateActive && node.Status == control.NodeAlive
		}
	}
	return false
}

func validateOpsMCPMutationInput(expectedRevision uint64, idempotencyKey string) error {
	key := strings.TrimSpace(idempotencyKey)
	if expectedRevision == 0 || key == "" || len(key) > 128 {
		return ErrOpsMCPInvalidRequest
	}
	return nil
}

func validOpsMCPCredentialID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func generateOpsMCPToken() (credentialID, token, digest string, err error) {
	idBytes := make([]byte, 8)
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", "", err
	}
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", "", err
	}
	credentialID = hex.EncodeToString(idBytes)
	token = "wko_" + credentialID + "_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	sum := sha256.Sum256([]byte(token))
	return credentialID, token, hex.EncodeToString(sum[:]), nil
}
