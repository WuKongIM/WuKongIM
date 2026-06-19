package tracectx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

type contextKey struct{}

// Context carries diagnostics trace metadata across gateway and runtime boundaries.
type Context struct {
	// TraceID is the lowercase 16-byte hexadecimal diagnostics trace identifier.
	TraceID string
	// Sampled records whether diagnostics events should be emitted for this trace.
	Sampled bool
	// Attempt records the current retry or delivery attempt associated with the trace.
	Attempt int
}

// WithContext stores diagnostics trace metadata on ctx.
func WithContext(ctx context.Context, trace Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, trace)
}

// FromContext returns diagnostics trace metadata stored on ctx.
func FromContext(ctx context.Context) (Context, bool) {
	if ctx == nil {
		return Context{}, false
	}
	trace, ok := ctx.Value(contextKey{}).(Context)
	if !ok || trace.TraceID == "" {
		return Context{}, false
	}
	return trace, true
}

// Ensure returns a context with valid diagnostics trace metadata.
func Ensure(ctx context.Context, _ func() time.Time) (context.Context, Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace, ok := FromContext(ctx); ok {
		if traceID, valid := ValidateHeaderTraceID(trace.TraceID); valid {
			trace.TraceID = traceID
			return WithContext(ctx, trace), trace
		}
	}

	trace := Context{TraceID: newTraceID(), Sampled: true}
	return WithContext(ctx, trace), trace
}

// ValidateHeaderTraceID validates and normalizes a 16-byte hexadecimal trace id header.
func ValidateHeaderTraceID(value string) (string, bool) {
	if len(value) != 32 {
		return "", false
	}
	for _, ch := range value {
		if !isHex(ch) {
			return "", false
		}
	}
	return lowerHex(value), true
}

func newTraceID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return strings.Repeat("0", 31) + "1"
	}
	return hex.EncodeToString(bytes[:])
}

func isHex(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func lowerHex(value string) string {
	buf := []byte(value)
	for i, ch := range buf {
		if ch >= 'A' && ch <= 'F' {
			buf[i] = ch + ('a' - 'A')
		}
	}
	return string(buf)
}
