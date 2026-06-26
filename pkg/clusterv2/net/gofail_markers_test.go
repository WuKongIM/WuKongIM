package clusternet

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestTransportGofailMarkersInstrumentEachTypedClientMethod(t *testing.T) {
	source, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatalf("ReadFile(transport.go): %v", err)
	}
	text := string(source)
	required := []struct {
		method string
		marker string
		call   string
	}{
		{
			method: "CallShard",
			marker: "// gofail: var wkClusterNetCallShardFault string",
			call:   "// if err := gofailClusterNetServiceFault(wkClusterNetCallShardFault, serviceID); err != nil { return nil, err }",
		},
		{
			method: "CallShardOwned",
			marker: "// gofail: var wkClusterNetCallShardOwnedFault string",
			call:   "// if err := gofailClusterNetServiceFault(wkClusterNetCallShardOwnedFault, serviceID); err != nil { return nil, err }",
		},
		{
			method: "Send",
			marker: "// gofail: var wkClusterNetSendFault string",
			call:   "// if err := gofailClusterNetServiceFault(wkClusterNetSendFault, serviceID); err != nil { return err }",
		},
		{
			method: "SendOwned",
			marker: "// gofail: var wkClusterNetSendOwnedFault string",
			call:   "// if err := gofailClusterNetServiceFault(wkClusterNetSendOwnedFault, serviceID); err != nil { return err }",
		},
	}
	for _, req := range required {
		body := transportMethodSource(t, source, req.method)
		if !strings.Contains(body, req.marker) {
			t.Fatalf("%s missing gofail marker %q", req.method, req.marker)
		}
		if !strings.Contains(body, req.call) {
			t.Fatalf("%s missing gofail call %q", req.method, req.call)
		}
		if got := strings.Count(text, req.marker); got != 1 {
			t.Fatalf("marker %q appears %d times, want once", req.marker, got)
		}
	}
}

func TestGofailClusterNetServiceFaultMatchesAlias(t *testing.T) {
	err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCNodeLifecycle)
	if err == nil || !strings.Contains(err.Error(), "seed join unavailable") {
		t.Fatalf("matched error = %v, want seed join unavailable", err)
	}
	if err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCControlWrite); err != nil {
		t.Fatalf("non-matching service error = %v, want nil", err)
	}
	if err := gofailClusterNetServiceFault("control_write:controller write unavailable", RPCControlWrite); err == nil || !strings.Contains(err.Error(), "controller write unavailable") {
		t.Fatalf("control_write error = %v, want controller write unavailable", err)
	}
	if err := gofailClusterNetServiceFault("manager_db_inspect:down", RPCManagerDBInspect); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("manager_db_inspect error = %v, want down", err)
	}
	if err := gofailClusterNetServiceFault("all:boom", RPCControlWrite); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("all match error = %v, want boom", err)
	}
	if err := gofailClusterNetServiceFault("all: ", RPCControlWrite); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("blank message error = %v, want injected", err)
	}
	if err := gofailClusterNetServiceFault("unknown_service:boom", 255); err != nil {
		t.Fatalf("unknown service named alias error = %v, want nil", err)
	}
	if err := gofailClusterNetServiceFault(":boom", 255); err != nil {
		t.Fatalf("unknown service blank alias error = %v, want nil", err)
	}
	if err := gofailClusterNetServiceFault("all:unknown", 255); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown service all error = %v, want unknown", err)
	}
	if err := gofailClusterNetServiceFault("malformed", RPCControlWrite); !errors.Is(err, errGofailClusterNetFault) {
		t.Fatalf("malformed error = %v, want errGofailClusterNetFault", err)
	}
}

func transportMethodSource(t *testing.T, source []byte, method string) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "transport.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile(transport.go): %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != method || fn.Recv == nil || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Offset
		end := fset.Position(fn.End()).Offset
		return string(source[start:end])
	}
	t.Fatalf("method %s not found", method)
	return ""
}
