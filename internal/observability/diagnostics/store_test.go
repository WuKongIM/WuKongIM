package diagnostics

import (
	"testing"
	"time"
)

func TestEventContainsMessageSeq(t *testing.T) {
	event := Event{MessageSeq: 7, RangeStart: 10, RangeEnd: 12}
	if !event.ContainsMessageSeq(7) {
		t.Fatal("expected direct message seq match")
	}
	if !event.ContainsMessageSeq(11) {
		t.Fatal("expected range message seq match")
	}
	if event.ContainsMessageSeq(9) {
		t.Fatal("did not expect message seq outside direct and range match")
	}
}

func TestNormalizeEventSetsDefaultsAndTruncatesError(t *testing.T) {
	event := normalizeEvent(Event{Stage: Stage("message.send_durable"), Error: string(make([]byte, 300))}, time.Unix(10, 0), 256)
	if event.Result != ResultOK {
		t.Fatalf("expected default result %q, got %q", ResultOK, event.Result)
	}
	if !event.At.Equal(time.Unix(10, 0)) {
		t.Fatalf("expected default timestamp, got %s", event.At)
	}
	if len(event.Error) != 256 {
		t.Fatalf("expected truncated error length 256, got %d", len(event.Error))
	}
}

func TestRedactEventRemovesSensitiveFieldsFromResponses(t *testing.T) {
	event := redactEvent(Event{FromUID: "u1", Error: "boom"})
	if event.FromUID != "" {
		t.Fatalf("expected FromUID to be redacted, got %q", event.FromUID)
	}
	if event.Error != "boom" {
		t.Fatalf("expected Error to be preserved, got %q", event.Error)
	}
}
