package rpctrace_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestOperationDeclarationIsServerOnlyAndFrozen(t *testing.T) {
	var absent *rpctrace.Trace
	if absent.RecordOperation(11) {
		t.Fatal("nil trace accepted operation")
	}
	r, _ := rpctrace.NewRecorder(2)
	client := r.Start(context.Background(), rpctrace.SideClient, 14)
	if client.RecordOperation(11) || client.Finish(rpctrace.ResultOK).Operation != 0 {
		t.Fatal("client invented server declaration")
	}
	server := r.Start(context.Background(), rpctrace.SideServer, 14)
	if server.RecordOperation(0) || !server.RecordOperation(11) || server.RecordOperation(7) {
		t.Fatal("operation is not first nonzero declaration")
	}
	s := server.Finish(rpctrace.ResultCanceled)
	if s.Operation != 11 || server.RecordOperation(9) || server.Finish(rpctrace.ResultOK) != nil {
		t.Fatal("finished operation changed")
	}
	clone := s.Clone()
	clone.Operation = 7
	if s.Operation != 11 {
		t.Fatal("clone changed original scalar")
	}
	data, err := json.Marshal(s)
	if err != nil || !strings.Contains(string(data), `"operation":11`) {
		t.Fatalf("operation JSON missing: %s/%v", data, err)
	}
	unknown, err := json.Marshal(rpctrace.Sample{})
	if err != nil || strings.Contains(string(unknown), `"operation"`) {
		t.Fatal("unknown operation is not omitted")
	}
}
