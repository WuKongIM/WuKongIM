package opsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	ordinaryCallsPerMinute       = 60
	logCallsPerMinute            = 20
	ingressRequestsPerMinute     = 60
	concurrentCallsPerCredential = 4
	concurrentCallsPerNode       = 16
	authFailuresPerSourceMinute  = 30
	authFailuresPerNodeMinute    = 300
	maxRecentAudits              = 200
)

var (
	// ErrRateLimited reports a per-credential minute budget exhaustion.
	ErrRateLimited = errors.New("internal/runtime/opsmcp: rate limited")
	// ErrConcurrencyLimited reports a credential or node concurrency ceiling.
	ErrConcurrencyLimited = errors.New("internal/runtime/opsmcp: concurrency limited")
)

// CallMetadata is the bounded, non-secret execution selector set.
type CallMetadata struct {
	// RequestID correlates one public request without containing a credential.
	RequestID string
	// Principal is the verified non-raw credential identity.
	Principal Principal
	// Tool is one frozen low-cardinality tool name.
	Tool string
	// NodeID is the optional exact target node.
	NodeID uint64
	// SlotID is the optional exact physical Slot.
	SlotID uint32
	// ChannelType is the optional bounded channel type selector.
	ChannelType uint8
	// PprofKind is the optional supported profile kind.
	PprofKind string
	// PprofSeconds is the bounded requested CPU sampling duration.
	PprofSeconds int
}

// CallFinish records one tool result without arguments or response content.
type CallFinish struct {
	// Result is a stable low-cardinality completion class.
	Result string
	// ResponseBytes is the bounded encoded response size.
	ResponseBytes int
}

// CallController is the access-layer seam for limits and local audits.
type CallController interface {
	BeginCall(context.Context, CallMetadata) (context.Context, func(CallFinish), error)
	BeginIngress(Principal, string, uint64, uint64) (func(string), error)
	AllowAuthentication(remoteAddr string) bool
	RecordAuthFailure(remoteAddr, requestID string)
}

// CallObserver records low-cardinality MCP call lifecycle metrics.
type CallObserver interface {
	// CallStarted records one admitted tool call.
	CallStarted(tool string)
	// CallFinished records one completed admitted tool call.
	CallFinished(tool, result string, duration time.Duration)
	// CallRejected records one rate or concurrency admission rejection.
	CallRejected(tool, result string)
	// AuthenticationFailed records one failed bearer authentication.
	AuthenticationFailed()
}

// CallControlConfig configures local high-frequency state.
type CallControlConfig struct {
	// AuditPath is the optional local rotated JSON-lines file.
	AuditPath string
	// Now provides deterministic rate windows and durations.
	Now func() time.Time
	// Observer receives bounded metrics without tokens, selectors, or request IDs.
	Observer CallObserver
}

// CallControl enforces local budgets and stores bounded audit summaries.
type CallControl struct {
	now      func() time.Time
	observer CallObserver

	mu           sync.Mutex
	credential   map[string]*credentialBudget
	totalActive  int
	authWindow   time.Time
	authBySource map[string]int
	authTotal    int
	recent       []opscontract.AuditEntry

	writerMu sync.Mutex
	writer   io.WriteCloser
}

type credentialBudget struct {
	windowStart time.Time
	ordinary    int
	logs        int
	ingress     int
	active      int
}

// NewCallControl creates local MCP limits and optional rotated audit output.
func NewCallControl(config CallControlConfig) *CallControl {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	var writer io.WriteCloser
	if path := strings.TrimSpace(config.AuditPath); path != "" {
		writer = &lumberjack.Logger{
			Filename: path, MaxSize: 10, MaxBackups: 10, MaxAge: 7, Compress: false,
		}
	}
	return &CallControl{
		now: now, observer: config.Observer, writer: writer, credential: make(map[string]*credentialBudget),
		authBySource: make(map[string]int), recent: make([]opscontract.AuditEntry, 0, maxRecentAudits),
	}
}

// BeginCall admits one tool call and returns an idempotent completion recorder.
func (c *CallControl) BeginCall(ctx context.Context, metadata CallMetadata) (context.Context, func(CallFinish), error) {
	if c == nil || metadata.Principal.CredentialID == "" || metadata.Tool == "" {
		return ctx, func(CallFinish) {}, ErrUnauthorized
	}
	now := c.now().UTC()
	window := now.Truncate(time.Minute)
	c.mu.Lock()
	c.pruneCredentialBudgetsLocked(window)
	budget := c.credential[metadata.Principal.CredentialID]
	if budget == nil {
		budget = &credentialBudget{}
		c.credential[metadata.Principal.CredentialID] = budget
	}
	if !budget.windowStart.Equal(window) {
		budget.windowStart = window
		budget.ordinary = 0
		budget.logs = 0
		budget.ingress = 0
	}
	isLogs := metadata.Tool == "logs_search" || metadata.Tool == "logs_context"
	rateLimited := isLogs && budget.logs >= logCallsPerMinute || !isLogs && budget.ordinary >= ordinaryCallsPerMinute
	if rateLimited {
		c.mu.Unlock()
		c.observeRejected(metadata.Tool, "rate_limited")
		c.recordAudit(auditEntry(metadata, now, "rate_limited", 0, 0, false))
		return ctx, func(CallFinish) {}, ErrRateLimited
	}
	if budget.active >= concurrentCallsPerCredential || c.totalActive >= concurrentCallsPerNode {
		c.mu.Unlock()
		c.observeRejected(metadata.Tool, "concurrency_limited")
		c.recordAudit(auditEntry(metadata, now, "concurrency_limited", 0, 0, false))
		return ctx, func(CallFinish) {}, ErrConcurrencyLimited
	}
	if isLogs {
		budget.logs++
	} else {
		budget.ordinary++
	}
	budget.active++
	c.totalActive++
	c.mu.Unlock()
	if c.observer != nil {
		c.observer.CallStarted(metadata.Tool)
	}

	var cacheHit bool
	ctx = opscontract.WithCacheObserver(ctx, func(hit bool) { cacheHit = hit })
	var once sync.Once
	finish := func(result CallFinish) {
		once.Do(func() {
			c.mu.Lock()
			if current := c.credential[metadata.Principal.CredentialID]; current != nil && current.active > 0 {
				current.active--
			}
			if c.totalActive > 0 {
				c.totalActive--
			}
			c.mu.Unlock()
			duration := c.now().UTC().Sub(now)
			if duration < 0 {
				duration = 0
			}
			if c.observer != nil {
				c.observer.CallFinished(metadata.Tool, result.Result, duration)
			}
			c.recordAudit(auditEntry(metadata, now, result.Result, duration.Milliseconds(), result.ResponseBytes, cacheHit))
		})
	}
	return ctx, finish, nil
}

// BeginIngress admits and audits one token-authenticated request that must be
// forwarded to a remote owner. It uses a separate local ingress budget so the
// owner remains the cluster-wide tool-call limiter.
func (c *CallControl) BeginIngress(principal Principal, requestID string, ingressNodeID, ownerNodeID uint64) (func(string), error) {
	if c == nil || principal.CredentialID == "" || requestID == "" || ingressNodeID == 0 || ownerNodeID == 0 {
		return func(string) {}, ErrUnauthorized
	}
	now := c.now().UTC()
	window := now.Truncate(time.Minute)
	c.mu.Lock()
	c.pruneCredentialBudgetsLocked(window)
	budget := c.credential[principal.CredentialID]
	if budget == nil {
		budget = &credentialBudget{windowStart: window}
		c.credential[principal.CredentialID] = budget
	}
	if !budget.windowStart.Equal(window) {
		budget.windowStart = window
		budget.ordinary = 0
		budget.logs = 0
		budget.ingress = 0
	}
	entry := opscontract.AuditEntry{
		RequestID: requestID, Phase: "ingress", IngressNodeID: ingressNodeID,
		OwnerNodeID: ownerNodeID, CredentialID: principal.CredentialID, StartedAt: now,
	}
	if budget.ingress >= ingressRequestsPerMinute {
		c.mu.Unlock()
		entry.Result = "rate_limited"
		c.recordAudit(entry)
		return func(string) {}, ErrRateLimited
	}
	if budget.active >= concurrentCallsPerCredential || c.totalActive >= concurrentCallsPerNode {
		c.mu.Unlock()
		entry.Result = "concurrency_limited"
		c.recordAudit(entry)
		return func(string) {}, ErrConcurrencyLimited
	}
	budget.ingress++
	budget.active++
	c.totalActive++
	c.mu.Unlock()

	var once sync.Once
	return func(result string) {
		once.Do(func() {
			c.mu.Lock()
			if current := c.credential[principal.CredentialID]; current != nil && current.active > 0 {
				current.active--
			}
			if c.totalActive > 0 {
				c.totalActive--
			}
			c.mu.Unlock()
			entry.Result = result
			entry.DurationMS = max(0, c.now().UTC().Sub(now).Milliseconds())
			c.recordAudit(entry)
		})
	}, nil
}

// AllowAuthentication reports whether prior failures left source and node budgets.
func (c *CallControl) AllowAuthentication(remoteAddr string) bool {
	if c == nil {
		return true
	}
	source := tcpSource(remoteAddr)
	nowWindow := c.now().UTC().Truncate(time.Minute)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetAuthWindowLocked(nowWindow)
	return c.authTotal < authFailuresPerNodeMinute && c.authBySource[source] < authFailuresPerSourceMinute
}

// RecordAuthFailure records one failed public bearer authentication decision.
func (c *CallControl) RecordAuthFailure(remoteAddr, requestID string) {
	if c == nil {
		return
	}
	now := c.now().UTC()
	source := tcpSource(remoteAddr)
	c.mu.Lock()
	c.resetAuthWindowLocked(now.Truncate(time.Minute))
	c.authTotal++
	c.authBySource[source]++
	c.mu.Unlock()
	if c.observer != nil {
		c.observer.AuthenticationFailed()
	}
	c.recordAudit(opscontract.AuditEntry{
		RequestID: requestID, StartedAt: now, Result: "auth_failed",
	})
}

func (c *CallControl) observeRejected(tool, result string) {
	if c != nil && c.observer != nil {
		c.observer.CallRejected(tool, result)
	}
}

// RecentAudits returns newest-first detached local audit summaries.
func (c *CallControl) RecentAudits(ctx context.Context, limit int) ([]opscontract.AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || limit < 1 || limit > maxRecentAudits {
		return nil, errors.New("internal/runtime/opsmcp: invalid audit limit")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := min(limit, len(c.recent))
	out := make([]opscontract.AuditEntry, 0, count)
	for index := len(c.recent) - 1; index >= 0 && len(out) < count; index-- {
		out = append(out, c.recent[index])
	}
	return out, nil
}

// Close flushes the optional rotated audit writer.
func (c *CallControl) Close() error {
	if c == nil {
		return nil
	}
	c.writerMu.Lock()
	defer c.writerMu.Unlock()
	if c.writer == nil {
		return nil
	}
	err := c.writer.Close()
	c.writer = nil
	return err
}

func (c *CallControl) resetAuthWindowLocked(window time.Time) {
	if c.authWindow.Equal(window) {
		return
	}
	c.authWindow = window
	c.authTotal = 0
	c.authBySource = make(map[string]int)
}

func (c *CallControl) pruneCredentialBudgetsLocked(window time.Time) {
	for credentialID, budget := range c.credential {
		if budget == nil || (budget.active == 0 && budget.windowStart.Before(window)) {
			delete(c.credential, credentialID)
		}
	}
}

func (c *CallControl) recordAudit(entry opscontract.AuditEntry) {
	if c == nil {
		return
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	payload = append(payload, '\n')
	c.mu.Lock()
	if len(c.recent) == maxRecentAudits {
		copy(c.recent, c.recent[1:])
		c.recent[len(c.recent)-1] = entry
	} else {
		c.recent = append(c.recent, entry)
	}
	c.mu.Unlock()
	c.writerMu.Lock()
	if c.writer != nil {
		_, _ = c.writer.Write(payload)
	}
	c.writerMu.Unlock()
}

func auditEntry(metadata CallMetadata, startedAt time.Time, result string, durationMS int64, responseBytes int, cacheHit bool) opscontract.AuditEntry {
	pprofSeconds := metadata.PprofSeconds
	if metadata.PprofKind == "cpu" && pprofSeconds == 0 {
		pprofSeconds = 10
	}
	return opscontract.AuditEntry{
		RequestID: metadata.RequestID, Phase: "owner", OwnerNodeID: metadata.Principal.OwnerNodeID,
		CredentialID: metadata.Principal.CredentialID, Tool: metadata.Tool,
		Target: opscontract.AuditTarget{
			NodeID: metadata.NodeID, SlotID: metadata.SlotID, ChannelType: metadata.ChannelType,
		},
		StartedAt: startedAt, DurationMS: durationMS, Result: result,
		ResponseBytes: int64(responseBytes), CacheHit: cacheHit,
		PprofKind: metadata.PprofKind, PprofSeconds: pprofSeconds,
	}
}

func tcpSource(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	if value := strings.TrimSpace(remoteAddr); value != "" {
		return value
	}
	return "unknown"
}

var _ CallController = (*CallControl)(nil)
