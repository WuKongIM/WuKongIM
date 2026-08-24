// Package benchterminal owns the entry-agnostic terminal delivery cut for one
// benchmark product-process generation.
package benchterminal

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	// Version is the only target-to-worker terminal preparation schema.
	Version = "wukongim/bench-terminal-fence/v1"

	maxIdentityBytes       = 128
	maxExpectedSessions    = 1_000_000
	defaultMaxSessions     = 2500
	maxDrainTimeout        = 90 * time.Second
	defaultDrainTimeout    = maxDrainTimeout
	epochRandomBytes       = 8
	capabilityRandomBytes  = 32
	capabilityEncodedBytes = 43 // base64.RawURLEncoding.EncodedLen(32)
	maxEpochAttempts       = 3
)

var (
	// ErrInvalidPrepareRequest reports a missing or out-of-bound terminal identity.
	ErrInvalidPrepareRequest = errors.New("internal/usecase/benchterminal: invalid prepare request")
	// ErrPreparationConflict reports an attempt to reuse one terminal controller
	// with a different run identity.
	ErrPreparationConflict = errors.New("internal/usecase/benchterminal: preparation identity conflict")
	// ErrPreparationFailed reports a terminal epoch whose background preparation
	// or session-marker admission failed. The low-cardinality reason is in Status.
	ErrPreparationFailed = errors.New("internal/usecase/benchterminal: preparation failed")
	// ErrTerminalNotReady reports an EVENT admission before its exact terminal
	// preparation completes.
	ErrTerminalNotReady = errors.New("internal/usecase/benchterminal: terminal is not ready")
	// ErrGrantRejected reports a bad epoch or capability without revealing which.
	ErrGrantRejected = errors.New("internal/usecase/benchterminal: grant rejected")
	// ErrInvalidSessionFence reports an incomplete owner-local session marker.
	ErrInvalidSessionFence = errors.New("internal/usecase/benchterminal: invalid session fence")
	// ErrSessionLimit reports an unexpected session after the exact local count
	// has already been admitted for this terminal epoch.
	ErrSessionLimit = errors.New("internal/usecase/benchterminal: expected session count reached")
	// ErrProtocolViolation reports a duplicate or otherwise invalid session
	// marker that permanently invalidated the published terminal epoch.
	ErrProtocolViolation = errors.New("internal/usecase/benchterminal: terminal protocol violation")
	errZeroEpoch         = errors.New("internal/usecase/benchterminal: random epoch was zero")
)

// GatewayDrainer closes gateway SEND admission and waits for accepted work.
type GatewayDrainer interface {
	DrainSends(context.Context) error
}

// ChannelAppendStopper closes channel-append admission and drains accepted work.
type ChannelAppendStopper interface {
	Stop(context.Context) error
}

// DeliveryQuiescer closes delivery-plan admission and waits for pending RECVACK.
type DeliveryQuiescer interface {
	Quiesce(context.Context) error
}

// SessionFence is the frame-independent marker that an owner-local session
// must enqueue atomically with closing its ordinary outbound admission.
// Nonce is copied from the authenticated terminal EVENT and is returned only
// in that session's matching acknowledgement.
type SessionFence struct {
	SessionID uint64
	Epoch     uint64
	Nonce     [16]byte
}

// String deliberately redacts the request nonce from ordinary formatting.
func (f SessionFence) String() string {
	return fmt.Sprintf("bench-terminal-session-fence{session_id:%d epoch:%d nonce:[redacted]}", f.SessionID, f.Epoch)
}

// GoString deliberately redacts the request nonce from %#v formatting.
func (f SessionFence) GoString() string { return f.String() }

// SessionSealer owns the session write-lock seam. Implementations must either
// atomically seal ordinary outbound writes and enqueue marker, or return an
// error; they must not claim a remote acknowledgement.
type SessionSealer interface {
	SealAndEnqueue(context.Context, SessionFence) error
}

// Options supplies the terminal-pipeline adapters. Reader exists only to
// source opaque epoch material; nil selects crypto/rand.Reader. DrainTimeout
// bounds the complete detached prepare pipeline; values above 90 seconds are
// clamped so a malformed adapter cannot retain the generation forever.
type Options struct {
	Gateway       GatewayDrainer
	ChannelAppend ChannelAppendStopper
	Delivery      DeliveryQuiescer
	Reader        io.Reader
	MaxSessions   int
	DrainTimeout  time.Duration
	// Goroutines owns the detached bounded prepare pipeline.
	Goroutines *goruntimeregistry.Registry
}

// PrepareRequest names one exact benchmark assignment and its expected local
// session count. Values are immutable once preparation starts.
type PrepareRequest struct {
	RunID            string
	AssignmentID     string
	ExpectedSessions int
}

// Grant carries opaque, fixed-size material required by a later owner-local
// terminal event. It is never copied into Status.
type Grant struct {
	Epoch      uint64
	Capability string
}

// Proof is the non-secret presentation extracted from one validated wire
// request. The capability itself never crosses the access/usecase boundary.
type Proof struct {
	Epoch            uint64
	CapabilitySHA256 [sha256.Size]byte
}

// String deliberately redacts the capability digest from diagnostics.
func (p Proof) String() string {
	return fmt.Sprintf("bench-terminal-proof{epoch:%d capability_sha256:[redacted]}", p.Epoch)
}

// GoString deliberately redacts the digest from %#v formatting.
func (p Proof) GoString() string { return p.String() }

// String deliberately redacts the capability from ordinary formatting.
func (g Grant) String() string {
	return fmt.Sprintf("bench-terminal-grant{epoch:%d capability:[redacted]}", g.Epoch)
}

// GoString deliberately redacts the capability from %#v formatting.
func (g Grant) GoString() string { return g.String() }

// Stage is a closed, low-cardinality preparation or server-seal state.
type Stage string

const (
	StageIdle                  Stage = "idle"
	StageDrainingGateway       Stage = "draining_gateway"
	StageStoppingChannelAppend Stage = "stopping_channelappend"
	StageQuiescingDelivery     Stage = "quiescing_delivery"
	StageAwaitingSessions      Stage = "awaiting_sessions"
	StageSessionsSealed        Stage = "sessions_sealed"
	StageFailed                Stage = "failed"
)

// FailureCode is a closed diagnostic class without raw adapter errors.
type FailureCode string

const (
	FailureNone              FailureCode = "none"
	FailureGatewayDrain      FailureCode = "gateway_drain"
	FailureChannelAppendStop FailureCode = "channelappend_stop"
	FailureDeliveryQuiesce   FailureCode = "delivery_quiesce"
	FailureRandom            FailureCode = "random"
	FailureSessionSeal       FailureCode = "session_seal"
	FailureProtocolViolation FailureCode = "protocol_violation"
)

// Status exposes only bounded epoch progress. It intentionally never contains
// run identity, assignment identity, capability material, or session IDs.
type Status struct {
	Stage            Stage
	Epoch            uint64
	ExpectedSessions int
	SealedSessions   int
	Failure          FailureCode
}

// SealResult describes whether this caller newly admitted the exact session
// marker. It never reports a remote client acknowledgement.
type SealResult struct {
	Enqueued       bool
	Complete       bool
	SealedSessions int
}

type sessionSealState uint8

const (
	sessionSealing sessionSealState = iota + 1
	sessionSealed
)

// Controller serializes one exact benchmark terminal epoch behind a small,
// entry-agnostic interface. It is deliberately one-shot because every QPS
// tier uses an independent product-process generation.
type Controller struct {
	gateway       GatewayDrainer
	channelAppend ChannelAppendStopper
	delivery      DeliveryQuiescer
	reader        io.Reader
	maxSessions   int
	drainTimeout  time.Duration
	goroutines    *goruntimeregistry.Registry

	mu             sync.Mutex
	stage          Stage
	failure        FailureCode
	request        PrepareRequest
	grant          Grant
	done           chan struct{}
	sessions       map[uint64]sessionSealState
	sealedSessions int
}

// New creates one one-shot terminal controller. Missing pipeline ports become
// a permanent preparation failure rather than a partial terminal claim.
func New(options Options) *Controller {
	reader := options.Reader
	if reader == nil {
		reader = cryptorand.Reader
	}
	maxSessions := options.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}
	if maxSessions > maxExpectedSessions {
		maxSessions = maxExpectedSessions
	}
	drainTimeout := options.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = defaultDrainTimeout
	}
	if drainTimeout > maxDrainTimeout {
		drainTimeout = maxDrainTimeout
	}
	return &Controller{
		gateway:       options.Gateway,
		channelAppend: options.ChannelAppend,
		delivery:      options.Delivery,
		reader:        reader,
		maxSessions:   maxSessions,
		drainTimeout:  drainTimeout,
		goroutines:    options.Goroutines,
		stage:         StageIdle,
		failure:       FailureNone,
		sessions:      make(map[uint64]sessionSealState),
	}
}

// Prepare starts or joins the exact terminal preparation. Its context bounds
// only this caller's wait: after admission, the strict pipeline keeps draining
// within its own bounded background context and can be joined later by the
// same identity.
func (c *Controller) Prepare(ctx context.Context, request PrepareRequest) (Grant, error) {
	if c == nil || !validPrepareRequest(request, 0) {
		return Grant{}, ErrInvalidPrepareRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, err
	}
	c.mu.Lock()
	switch c.stage {
	case StageIdle:
		if !validPrepareRequest(request, c.maxSessions) {
			c.mu.Unlock()
			return Grant{}, ErrInvalidPrepareRequest
		}
		c.request = request
		c.stage = StageDrainingGateway
		c.done = make(chan struct{})
		done := c.done
		c.mu.Unlock()
		goruntimeregistry.SafeGo(c.goroutines, goruntimeregistry.TaskAppBenchTerminalPrepare, func() {
			c.prepare(done)
		})
		return c.waitPrepared(ctx, done)
	case StageFailed:
		if !sameIdentity(c.request, request) {
			c.mu.Unlock()
			return Grant{}, ErrPreparationConflict
		}
		c.mu.Unlock()
		return Grant{}, ErrPreparationFailed
	default:
		if !sameIdentity(c.request, request) {
			c.mu.Unlock()
			return Grant{}, ErrPreparationConflict
		}
		done, grant := c.done, c.grant
		ready := c.stage == StageAwaitingSessions || c.stage == StageSessionsSealed
		c.mu.Unlock()
		if ready {
			return grant, nil
		}
		return c.waitPrepared(ctx, done)
	}
}

func (c *Controller) waitPrepared(ctx context.Context, done <-chan struct{}) (Grant, error) {
	select {
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stage == StageAwaitingSessions || c.stage == StageSessionsSealed {
			return c.grant, nil
		}
		return Grant{}, ErrPreparationFailed
	case <-ctx.Done():
		return Grant{}, ctx.Err()
	}
}

func (c *Controller) prepare(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), c.drainTimeout)
	defer cancel()
	if c.gateway == nil || c.gateway.DrainSends(ctx) != nil || ctx.Err() != nil {
		c.fail(done, FailureGatewayDrain)
		return
	}
	c.transition(StageStoppingChannelAppend)
	if c.channelAppend == nil || c.channelAppend.Stop(ctx) != nil || ctx.Err() != nil {
		c.fail(done, FailureChannelAppendStop)
		return
	}
	c.transition(StageQuiescingDelivery)
	if c.delivery == nil || c.delivery.Quiesce(ctx) != nil || ctx.Err() != nil {
		c.fail(done, FailureDeliveryQuiesce)
		return
	}
	grant, err := c.newGrant()
	if err != nil {
		c.fail(done, FailureRandom)
		return
	}
	c.mu.Lock()
	if c.done == done {
		c.grant = grant
		c.stage = StageAwaitingSessions
		close(done)
	}
	c.mu.Unlock()
}

func (c *Controller) transition(stage Stage) {
	c.mu.Lock()
	if c.stage != StageFailed {
		c.stage = stage
	}
	c.mu.Unlock()
}

func (c *Controller) fail(done chan struct{}, failure FailureCode) {
	c.mu.Lock()
	if c.done == done {
		c.grant = Grant{}
		c.failure = failure
		c.stage = StageFailed
		close(done)
	}
	c.mu.Unlock()
}

func (c *Controller) newGrant() (Grant, error) {
	var epochBytes [epochRandomBytes]byte
	for attempt := 0; attempt < maxEpochAttempts; attempt++ {
		if _, err := io.ReadFull(c.reader, epochBytes[:]); err != nil {
			return Grant{}, err
		}
		epoch := binary.BigEndian.Uint64(epochBytes[:])
		if epoch == 0 {
			continue
		}
		capability := make([]byte, capabilityRandomBytes)
		if _, err := io.ReadFull(c.reader, capability); err != nil {
			return Grant{}, err
		}
		return Grant{Epoch: epoch, Capability: base64.RawURLEncoding.EncodeToString(capability)}, nil
	}
	return Grant{}, errZeroEpoch
}

// ValidateGrant checks a presented capability in constant time once the exact
// terminal epoch is ready. It has no side effects and never returns a secret.
func (c *Controller) ValidateGrant(presented Grant) error {
	if c == nil {
		return ErrTerminalNotReady
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage == StageFailed {
		return ErrPreparationFailed
	}
	if c.stage != StageAwaitingSessions && c.stage != StageSessionsSealed {
		return ErrTerminalNotReady
	}
	if !grantsEqual(presented, c.grant) {
		return ErrGrantRejected
	}
	return nil
}

// SealAndEnqueue verifies the presented grant and transfers one exact,
// owner-local session marker to the per-call SessionSealer. The entry adapter
// owns the current session lookup and must reject a fence whose SessionID is
// not its current session before invoking its write-lock implementation.
// A caller cancellation before admission stops that caller; once admitted, the
// session operation uses the controller's bounded background deadline because
// an ambiguous write cannot safely be retried. Any seal error permanently
// fails the terminal epoch.
func (c *Controller) SealAndEnqueue(ctx context.Context, presented Proof, fence SessionFence, sealer SessionSealer) (SealResult, error) {
	if c == nil {
		return SealResult{}, ErrInvalidSessionFence
	}
	if !validSessionFence(fence) {
		c.FailProtocolViolation()
		return SealResult{}, ErrInvalidSessionFence
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		c.FailProtocolViolation()
		return SealResult{}, err
	}
	c.mu.Lock()
	if c.stage == StageFailed {
		c.mu.Unlock()
		return SealResult{}, ErrPreparationFailed
	}
	if c.stage != StageAwaitingSessions && c.stage != StageSessionsSealed {
		c.mu.Unlock()
		return SealResult{}, ErrTerminalNotReady
	}
	grantMatch := proofMatchesGrant(presented, c.grant)
	epochMatch := constantTimeUint64Equal(fence.Epoch, c.grant.Epoch)
	if !(grantMatch && epochMatch) {
		c.failProtocolViolationLocked()
		c.mu.Unlock()
		return SealResult{}, ErrGrantRejected
	}
	if _, exists := c.sessions[fence.SessionID]; exists {
		c.failProtocolViolationLocked()
		c.mu.Unlock()
		return SealResult{}, ErrProtocolViolation
	}
	if len(c.sessions) >= c.request.ExpectedSessions {
		c.failProtocolViolationLocked()
		c.mu.Unlock()
		return SealResult{}, ErrSessionLimit
	}
	c.sessions[fence.SessionID] = sessionSealing
	timeout := c.drainTimeout
	c.mu.Unlock()

	if sealer == nil {
		return c.failSessionSeal(fence.SessionID)
	}
	sealCtx, cancel := context.WithTimeout(context.Background(), timeout)
	err := sealer.SealAndEnqueue(sealCtx, fence)
	cancel()
	if err != nil {
		return c.failSessionSeal(fence.SessionID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage == StageFailed || c.sessions[fence.SessionID] != sessionSealing {
		return SealResult{}, ErrPreparationFailed
	}
	c.sessions[fence.SessionID] = sessionSealed
	c.sealedSessions++
	if c.sealedSessions == c.request.ExpectedSessions {
		c.stage = StageSessionsSealed
	}
	return c.sealResultLocked(true), nil
}

// FailProtocolViolation permanently invalidates a published terminal epoch.
// It is intentionally reason-free at the boundary so frame, session, and
// identity values cannot leak into status, logs, or metric labels.
func (c *Controller) FailProtocolViolation() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.failProtocolViolationLocked()
	c.mu.Unlock()
}

func (c *Controller) failProtocolViolationLocked() {
	if c.stage != StageAwaitingSessions && c.stage != StageSessionsSealed {
		return
	}
	c.grant.Capability = ""
	c.failure = FailureProtocolViolation
	c.stage = StageFailed
}

func (c *Controller) failSessionSeal(sessionID uint64) (SealResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessions[sessionID] == sessionSealing && c.stage != StageFailed {
		// Retain only the non-secret epoch for low-cardinality operator status.
		c.grant.Capability = ""
		c.failure = FailureSessionSeal
		c.stage = StageFailed
	}
	return SealResult{}, ErrPreparationFailed
}

func (c *Controller) sealResultLocked(enqueued bool) SealResult {
	return SealResult{
		Enqueued:       enqueued,
		Complete:       c.stage == StageSessionsSealed,
		SealedSessions: c.sealedSessions,
	}
}

// Status returns a bounded snapshot and never returns grant capability or
// identity values.
func (c *Controller) Status() Status {
	if c == nil {
		return Status{Stage: StageFailed, Failure: FailureGatewayDrain}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Stage:            c.stage,
		Epoch:            c.grant.Epoch,
		ExpectedSessions: c.request.ExpectedSessions,
		SealedSessions:   c.sealedSessions,
		Failure:          c.failure,
	}
}

func validPrepareRequest(request PrepareRequest, maxSessions int) bool {
	if request.RunID == "" || request.AssignmentID == "" || request.ExpectedSessions <= 0 || request.ExpectedSessions > maxExpectedSessions {
		return false
	}
	if request.RunID != strings.TrimSpace(request.RunID) || request.AssignmentID != strings.TrimSpace(request.AssignmentID) {
		return false
	}
	if len(request.RunID) > maxIdentityBytes || len(request.AssignmentID) > maxIdentityBytes {
		return false
	}
	return maxSessions == 0 || request.ExpectedSessions <= maxSessions
}

func validSessionFence(fence SessionFence) bool {
	if fence.SessionID == 0 || fence.Epoch == 0 {
		return false
	}
	var zero [16]byte
	return subtle.ConstantTimeCompare(fence.Nonce[:], zero[:]) != 1
}

func grantsEqual(left, right Grant) bool {
	epochMatch := constantTimeUint64Equal(left.Epoch, right.Epoch)
	capabilityMatch := constantTimeCapabilityEqual(left.Capability, right.Capability)
	return boolToInt(epochMatch)&boolToInt(capabilityMatch) == 1
}

// ProofForGrant is a construction helper for trusted adapters and tests that
// already own a grant. Product handlers should use the digest copied from the
// validated wire request instead of retaining the capability.
func ProofForGrant(grant Grant) Proof {
	return Proof{Epoch: grant.Epoch, CapabilitySHA256: sha256.Sum256([]byte(grant.Capability))}
}

func proofMatchesGrant(proof Proof, grant Grant) bool {
	want := sha256.Sum256([]byte(grant.Capability))
	return constantTimeUint64Equal(proof.Epoch, grant.Epoch) &&
		subtle.ConstantTimeCompare(proof.CapabilitySHA256[:], want[:]) == 1
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func constantTimeUint64Equal(left, right uint64) bool {
	var leftBytes, rightBytes [8]byte
	binary.BigEndian.PutUint64(leftBytes[:], left)
	binary.BigEndian.PutUint64(rightBytes[:], right)
	return subtle.ConstantTimeCompare(leftBytes[:], rightBytes[:]) == 1
}

func constantTimeCapabilityEqual(left, right string) bool {
	var leftBytes, rightBytes [capabilityEncodedBytes]byte
	copy(leftBytes[:], left)
	copy(rightBytes[:], right)
	lengthMatch := subtle.ConstantTimeEq(int32(len(left)), capabilityEncodedBytes)
	expectedLengthMatch := subtle.ConstantTimeEq(int32(len(right)), capabilityEncodedBytes)
	contentMatch := subtle.ConstantTimeCompare(leftBytes[:], rightBytes[:])
	return lengthMatch&expectedLengthMatch&contentMatch == 1
}

func sameIdentity(left, right PrepareRequest) bool {
	return left.RunID == right.RunID && left.AssignmentID == right.AssignmentID && left.ExpectedSessions == right.ExpectedSessions
}
