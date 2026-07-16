package cloudview

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limits bounds the public observation surface without decoding WuKongIM
// application frames inside the proxy.
type Limits struct {
	// HTTPRequestsPerSecondPerIP is the sustained HTTP request rate for one source address.
	HTTPRequestsPerSecondPerIP float64 `json:"http_requests_per_second_per_ip"`
	// HTTPBurstPerIP is the immediate HTTP token capacity for one source address.
	HTTPBurstPerIP int `json:"http_burst_per_ip"`
	// HTTPRequestsPerSecondGlobal is the sustained HTTP request rate across all clients.
	HTTPRequestsPerSecondGlobal float64 `json:"http_requests_per_second_global"`
	// HTTPBurstGlobal is the immediate global HTTP token capacity.
	HTTPBurstGlobal int `json:"http_burst_global"`
	// WebSocketConnectionsPerIP is the concurrent WebSocket limit for one source address.
	WebSocketConnectionsPerIP int `json:"websocket_connections_per_ip"`
	// WebSocketConnectionsGlobal is the concurrent WebSocket limit across all clients.
	WebSocketConnectionsGlobal int `json:"websocket_connections_global"`
}

func defaultLimits() Limits {
	return Limits{
		HTTPRequestsPerSecondPerIP:  30,
		HTTPBurstPerIP:              60,
		HTTPRequestsPerSecondGlobal: 200,
		HTTPBurstGlobal:             400,
		WebSocketConnectionsPerIP:   20,
		WebSocketConnectionsGlobal:  64,
	}
}

func normalizeLimits(configured Limits) (Limits, error) {
	defaults := defaultLimits()
	if configured.HTTPRequestsPerSecondPerIP == 0 {
		configured.HTTPRequestsPerSecondPerIP = defaults.HTTPRequestsPerSecondPerIP
	}
	if configured.HTTPBurstPerIP == 0 {
		configured.HTTPBurstPerIP = defaults.HTTPBurstPerIP
	}
	if configured.HTTPRequestsPerSecondGlobal == 0 {
		configured.HTTPRequestsPerSecondGlobal = defaults.HTTPRequestsPerSecondGlobal
	}
	if configured.HTTPBurstGlobal == 0 {
		configured.HTTPBurstGlobal = defaults.HTTPBurstGlobal
	}
	if configured.WebSocketConnectionsPerIP == 0 {
		configured.WebSocketConnectionsPerIP = defaults.WebSocketConnectionsPerIP
	}
	if configured.WebSocketConnectionsGlobal == 0 {
		configured.WebSocketConnectionsGlobal = defaults.WebSocketConnectionsGlobal
	}
	if configured.HTTPRequestsPerSecondPerIP <= 0 || configured.HTTPBurstPerIP <= 0 ||
		configured.HTTPRequestsPerSecondGlobal <= 0 || configured.HTTPBurstGlobal <= 0 ||
		configured.WebSocketConnectionsPerIP <= 0 || configured.WebSocketConnectionsGlobal <= 0 {
		return Limits{}, errInvalidOptions
	}
	return configured, nil
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (b *tokenBucket) allow(now time.Time, rate float64, burst int) bool {
	if b.last.IsZero() {
		b.tokens = float64(burst)
		b.last = now
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(float64(burst), b.tokens+elapsed*rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type sourceLimitState struct {
	httpTokens tokenBucket
	webSockets int
	lastSeen   time.Time
}

type requestLimiter struct {
	mu               sync.Mutex
	limits           Limits
	globalHTTPTokens tokenBucket
	globalWebSockets int
	sources          map[string]*sourceLimitState
	lastPrune        time.Time
}

func newRequestLimiter(limits Limits) *requestLimiter {
	return &requestLimiter{limits: limits, sources: make(map[string]*sourceLimitState)}
}

func (l *requestLimiter) allowHTTP(source string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	state := l.sourceLocked(source, now)
	if !l.globalHTTPTokens.allow(now, l.limits.HTTPRequestsPerSecondGlobal, l.limits.HTTPBurstGlobal) {
		return false
	}
	if !state.httpTokens.allow(now, l.limits.HTTPRequestsPerSecondPerIP, l.limits.HTTPBurstPerIP) {
		// Restore the global token because the per-source gate rejected this request.
		l.globalHTTPTokens.tokens = math.Min(float64(l.limits.HTTPBurstGlobal), l.globalHTTPTokens.tokens+1)
		return false
	}
	return true
}

func (l *requestLimiter) acquireWebSocket(source string, now time.Time) (func(), bool) {
	l.mu.Lock()
	l.pruneLocked(now)
	state := l.sourceLocked(source, now)
	if state.webSockets >= l.limits.WebSocketConnectionsPerIP || l.globalWebSockets >= l.limits.WebSocketConnectionsGlobal {
		l.mu.Unlock()
		return nil, false
	}
	state.webSockets++
	l.globalWebSockets++
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		state := l.sources[source]
		if state != nil && state.webSockets > 0 {
			state.webSockets--
			state.lastSeen = time.Now()
		}
		if l.globalWebSockets > 0 {
			l.globalWebSockets--
		}
	}, true
}

func (l *requestLimiter) sourceLocked(source string, now time.Time) *sourceLimitState {
	state := l.sources[source]
	if state == nil {
		state = &sourceLimitState{}
		l.sources[source] = state
	}
	state.lastSeen = now
	return state
}

func (l *requestLimiter) pruneLocked(now time.Time) {
	if !l.lastPrune.IsZero() && now.Sub(l.lastPrune) < time.Minute {
		return
	}
	for source, state := range l.sources {
		if state.webSockets == 0 && now.Sub(state.lastSeen) > 10*time.Minute {
			delete(l.sources, source)
		}
	}
	l.lastPrune = now
}

func requestSource(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(request.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if address := strings.TrimSpace(request.RemoteAddr); address != "" {
		return address
	}
	return "unknown"
}
