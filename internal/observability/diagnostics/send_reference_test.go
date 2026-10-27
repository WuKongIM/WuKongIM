package diagnostics

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func TestSendRPCReferenceStoreCloneAndClosedStates(t *testing.T) {
	for _, state := range []string{"bound", "unbound", "recorder_disabled", "recorder_exhausted", "local_untraced", "remote_untraced"} {
		t.Run(state, func(t *testing.T) {
			id := ""
			if state == "bound" {
				id = strings.Repeat("a", 32) + "/8000000000000001"
			}
			if state == "unbound" {
				id = "local/" + strings.Repeat("b", 32) + "/1"
			}
			// Decode the optional field without referring to new Go symbols so
			// the old source reaches an intended missing-behavior assertion.
			body, _ := json.Marshal(map[string]any{"TraceID": "upper-root", "Stage": "message.send_durable", "ClientMsgNo": "logical-key", "FromUID": "must-redact-uid", "Result": "ok", "rpc_reference": map[string]string{"state": state, "trace_id": id}})
			var input sendtrace.Event
			if err := json.Unmarshal(body, &input); err != nil {
				t.Fatal(err)
			}
			store := NewStore(StoreOptions{Capacity: 8})
			NewSendTraceSink(store, nil).RecordSendTrace(input)
			first := store.Query(context.Background(), Query{TraceID: "upper-root"})
			if len(first.Events) != 1 {
				t.Fatal("original event was not retained")
			}
			ref := sendReferenceJSON(t, first.Events[0])
			if ref["state"] != state || ref["trace_id"] != id {
				t.Fatalf("typed reference changed: %#v", ref)
			}
			if len(ref) != 2 {
				t.Fatal("reference exposed arbitrary fields")
			}
			mutateReferenceTrace(t, &input, "caller-mutated")
			mutateReferenceTrace(t, &first.Events[0], "query-mutated")
			second := store.Query(context.Background(), Query{TraceID: "upper-root"})
			if sendReferenceJSON(t, second.Events[0])["trace_id"] != id {
				t.Fatal("reference pointer escaped Record/Query ownership")
			}
			encoded, _ := json.Marshal(second)
			if strings.Contains(string(encoded), "must-redact-uid") {
				t.Fatal("reference query leaked raw UID")
			}
		})
	}
}

func TestSendRPCReferenceRejectsMalformedWithoutDroppingEvent(t *testing.T) {
	for _, ref := range []map[string]string{
		{"state": "unexpected", "trace_id": ""},
		{"state": "bound", "trace_id": ""},
		{"state": "bound", "trace_id": "local/" + strings.Repeat("a", 32) + "/1"},
		{"state": "bound", "trace_id": strings.Repeat("A", 32) + "/1"},
		{"state": "unbound", "trace_id": strings.Repeat("a", 32) + "/1"},
		{"state": "recorder_disabled", "trace_id": strings.Repeat("a", 32) + "/1"},
		{"state": "local_untraced", "trace_id": "raw-private-identity"},
	} {
		body, _ := json.Marshal(map[string]any{"trace_id": "original-root", "stage": "message.send_durable", "result": "ok", "rpc_reference": ref})
		var event Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatal(err)
		}
		store := NewStore(StoreOptions{Capacity: 2})
		store.Record(event)
		got := store.Query(context.Background(), Query{TraceID: "original-root"})
		if len(got.Events) != 1 {
			t.Fatal("invalid observation changed original event retention")
		}
		encoded, _ := json.Marshal(got.Events[0])
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if raw := fields["rpc_reference"]; len(raw) > 0 && string(raw) != "null" {
			t.Fatal("malformed observation was retained as authoritative")
		}
	}
}

func sendReferenceJSON(t *testing.T, event Event) map[string]string {
	t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields["rpc_reference"]) == 0 {
		t.Fatal("missing typed SEND/RPC reference in diagnostics JSON")
	}
	var ref map[string]string
	if err := json.Unmarshal(fields["rpc_reference"], &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func mutateReferenceTrace(t *testing.T, event any, value string) {
	t.Helper()
	v := reflect.ValueOf(event).Elem().FieldByName("RPCReference")
	if !v.IsValid() || v.IsNil() {
		t.Fatal("missing optional reference backing")
	}
	v.Elem().FieldByName("TraceID").SetString(value)
}
