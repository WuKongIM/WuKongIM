package tracectx

import (
	"context"
	"strings"
	"testing"
)

func TestValidateHeaderTraceIDAccepts32HexChars(t *testing.T) {
	traceID, ok := ValidateHeaderTraceID("ABCDEF0123456789abcdef0123456789")
	if !ok {
		t.Fatal("expected trace id to be valid")
	}
	if traceID != "abcdef0123456789abcdef0123456789" {
		t.Fatalf("expected lowercase normalized trace id, got %q", traceID)
	}
}

func TestValidateHeaderTraceIDRejectsInvalidValues(t *testing.T) {
	tests := []string{
		"",
		"abcdef0123456789abcdef012345678",
		"abcdef0123456789abcdef01234567890",
		"abcdef0123456789abcdef01234567zz",
		"abcdef0123456789abcdef01234567  ",
	}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			if traceID, ok := ValidateHeaderTraceID(value); ok || traceID != "" {
				t.Fatalf("expected invalid trace id, got traceID=%q ok=%v", traceID, ok)
			}
		})
	}
}

func TestEnsureCreatesAndPreservesTraceContext(t *testing.T) {
	createdCtx, created := Ensure(context.Background(), nil)
	if created.TraceID == "" {
		t.Fatal("expected generated trace id")
	}
	if len(created.TraceID) != 32 {
		t.Fatalf("expected 32 hex chars, got %q", created.TraceID)
	}
	if created.TraceID != strings.ToLower(created.TraceID) {
		t.Fatalf("expected lowercase trace id, got %q", created.TraceID)
	}
	if _, ok := ValidateHeaderTraceID(created.TraceID); !ok {
		t.Fatalf("expected generated trace id to validate, got %q", created.TraceID)
	}
	if !created.Sampled {
		t.Fatal("expected generated context to be sampled")
	}
	if created.Attempt != 0 {
		t.Fatalf("expected default attempt 0, got %d", created.Attempt)
	}
	if fromCtx, ok := FromContext(createdCtx); !ok || fromCtx != created {
		t.Fatalf("expected generated context to be stored, got context=%+v ok=%v", fromCtx, ok)
	}

	existing := Context{TraceID: "abcdef0123456789abcdef0123456789", Sampled: false, Attempt: 3}
	preservedCtx, preserved := Ensure(WithContext(context.Background(), existing), nil)
	if preserved != existing {
		t.Fatalf("expected existing context to be preserved, got %+v", preserved)
	}
	if fromCtx, ok := FromContext(preservedCtx); !ok || fromCtx != existing {
		t.Fatalf("expected existing context to remain stored, got context=%+v ok=%v", fromCtx, ok)
	}

	invalidExisting := Context{TraceID: "not-valid", Sampled: false, Attempt: 9}
	_, replaced := Ensure(WithContext(context.Background(), invalidExisting), nil)
	if replaced.TraceID == invalidExisting.TraceID {
		t.Fatal("expected invalid context to be replaced")
	}
	if !replaced.Sampled {
		t.Fatal("expected replaced context to be sampled")
	}
}

func TestFromContextRejectsEmptyTraceID(t *testing.T) {
	ctx := WithContext(context.Background(), Context{})
	trace, ok := FromContext(ctx)
	if ok {
		t.Fatalf("expected empty trace id to be absent, got %+v", trace)
	}
}
