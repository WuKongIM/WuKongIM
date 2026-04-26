package replica

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestReplicaStructHasNoObsoleteAppendOwnerFields(t *testing.T) {
	replicaType := reflect.TypeOf(replica{})
	obsolete := []string{
		"appendMu",
		"collectorDone",
		"checkpointDone",
	}
	for _, field := range obsolete {
		if _, ok := replicaType.FieldByName(field); ok {
			t.Fatalf("replica still has obsolete state-owner field %s", field)
		}
	}
}

func TestValidateLeaderReconcileEffectFenceDoesNotMutateLeaseState(t *testing.T) {
	r := newLeaderReplica(t)

	r.mu.Lock()
	r.pendingReconcileEffectID = 11
	r.roleGeneration = 3
	r.meta.LeaderEpoch = 5
	r.meta.LeaseUntil = r.now().Add(-1)
	effect := leaderReconcileDurableEffect{
		EffectID:       11,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
	}
	err := r.validateLeaderReconcileEffectFenceLocked(effect)
	role := r.state.Role
	pending := r.pendingReconcileEffectID
	generation := r.roleGeneration
	r.mu.Unlock()

	if err != channel.ErrLeaseExpired {
		t.Fatalf("validateLeaderReconcileEffectFenceLocked() error = %v, want ErrLeaseExpired", err)
	}
	if role != channel.ReplicaRoleLeader {
		t.Fatalf("role changed outside loop = %v, want leader", role)
	}
	if pending != 11 {
		t.Fatalf("pending reconcile effect changed outside loop = %d, want 11", pending)
	}
	if generation != 3 {
		t.Fatalf("role generation changed outside loop = %d, want 3", generation)
	}
}

func TestLeaderReconcileResultFencesExpiredLeaseInsideLoop(t *testing.T) {
	r := newLeaderReplica(t)

	r.mu.Lock()
	r.pendingReconcileEffectID = 11
	r.roleGeneration = 3
	r.meta.LeaderEpoch = 5
	r.meta.LeaseUntil = r.now().Add(-1)
	event := machineLeaderReconcileResultCommand{
		EffectID:       11,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		StoredLEO:      r.state.LEO,
		HW:             r.state.HW,
		Checkpoint:     channel.Checkpoint{Epoch: r.state.Epoch, LogStartOffset: r.state.LogStartOffset, HW: r.state.HW},
		Err:            channel.ErrLeaseExpired,
	}
	r.mu.Unlock()

	result := r.applyLoopEvent(event)

	if result.Err != channel.ErrLeaseExpired {
		t.Fatalf("applyLoopEvent() error = %v, want ErrLeaseExpired", result.Err)
	}
	st := r.Status()
	if st.Role != channel.ReplicaRoleFencedLeader {
		t.Fatalf("role = %v, want fenced leader", st.Role)
	}
	if st.CommitReady {
		t.Fatal("expired lease reconcile result left CommitReady=true")
	}
	r.mu.RLock()
	pending := r.pendingReconcileEffectID
	generation := r.roleGeneration
	r.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("pending reconcile effect = %d, want 0", pending)
	}
	if generation != 4 {
		t.Fatalf("role generation = %d, want 4", generation)
	}
}

func TestNoTransitionalHistoryDurableHelpersRemain(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "history.go", nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(history.go) error = %v", err)
	}
	obsolete := map[string]struct{}{
		"appendEpochPointLocked": {},
		"truncateLogToLocked":    {},
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, found := obsolete[fn.Name.Name]; found {
			t.Fatalf("obsolete transitional durable helper still exists: %s", fn.Name.Name)
		}
	}
}

func TestAppendTimeoutLoggingDoesNotReadLoopOwnedWaiterAfterUnlock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "append.go", nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(append.go) error = %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		candidate, ok := decl.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "logAppendTimeout" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("logAppendTimeout not found")
	}

	unlocked := false
	for _, stmt := range fn.Body.List {
		if callsReceiverMethod(stmt, "r", "mu", "RUnlock") {
			unlocked = true
			continue
		}
		if !unlocked {
			continue
		}
		ast.Inspect(stmt, func(n ast.Node) bool {
			if selectsNestedField(n, "req", "waiter") {
				t.Fatalf("logAppendTimeout reads req.waiter after releasing replica lock at %s", fset.Position(n.Pos()))
			}
			return true
		})
	}
}

func callsReceiverMethod(stmt ast.Stmt, receiver, field, method string) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != method {
		return false
	}
	parent, ok := selector.X.(*ast.SelectorExpr)
	if !ok || parent.Sel.Name != field {
		return false
	}
	ident, ok := parent.X.(*ast.Ident)
	return ok && ident.Name == receiver
}

func selectsNestedField(n ast.Node, root, field string) bool {
	selector, ok := n.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	parent, ok := selector.X.(*ast.SelectorExpr)
	if !ok || parent.Sel.Name != field {
		return false
	}
	ident, ok := parent.X.(*ast.Ident)
	return ok && ident.Name == root
}
