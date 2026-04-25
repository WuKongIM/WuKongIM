I'm using the writing-plans skill to create the implementation plan.

# Lock GroupKey Into ISR API Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the public ISR contract's numeric `GroupID` exposure with a validated `GroupKey` while locking in the first set of regression tests that assert the new behavior.

**Architecture:** Build from the existing `pkg/replication/isr` API surface by introducing the new `GroupKey` alias, moving validation and helper usage to string keys, and updating docs/tests so every public touchpoint reflects the new contract documented in `docs/superpowers/specs/2026-04-03-channel-keyed-isr-design.md`.

**Tech Stack:** Go, standard library testing and validation packages, existing ISR helpers.

---

### Task 1: Lock GroupKey in the public ISR contract

**Files:**
- Modify: `pkg/replication/isr/meta_test.go`
- Modify: `pkg/replication/isr/api_test.go`
- Modify: `pkg/replication/isr/types.go`
- Modify: `pkg/replication/isr/meta.go`
- Modify: `pkg/replication/isr/doc.go`
- Modify: `pkg/replication/isr/testenv_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestGroupMetaRejectsEmptyGroupKey(t *testing.T) {
    r := newTestReplica(t)
    err := r.ApplyMeta(isr.GroupMeta{
        GroupKey: "",
        Epoch:    1,
        Leader:   1,
        Replicas: []isr.NodeID{1, 2},
        ISR:      []isr.NodeID{1, 2},
        MinISR:   1,
    })
    if !errors.Is(err, isr.ErrInvalidMeta) {
        t.Fatalf("expected ErrInvalidMeta, got %v", err)
    }
}

func TestReplicaSurfaceUsesGroupKey(t *testing.T) {
    var req isr.FetchRequest
    req.GroupKey = isr.GroupKey("channel/1/YzE")

    var apply isr.ApplyFetchRequest
    apply.GroupKey = req.GroupKey
}
```

- [ ] **Step 2: Run the failing test to confirm the contract still exposes numeric GroupID**

```
go test ./pkg/replication/isr -run 'TestGroupMetaRejectsEmptyGroupKey|TestReplicaSurfaceUsesGroupKey' -v
```

Expected: FAIL because `GroupMeta` still insists on a non-zero numeric `GroupID`.

- [ ] **Step 3: Implement the minimal contract change**

1. Declare `type GroupKey string` in `pkg/replication/isr/types.go` and replace every `GroupID` field with `GroupKey` in `GroupMeta`, `ReplicaState`, `Snapshot`, `FetchRequest`, and `ApplyFetchRequest`.
2. Update `pkg/replication/isr/meta.go` to validate `GroupKey == ""` instead of `GroupID == 0` and keep the rest of the validation path untouched.
3. Refresh `pkg/replication/isr/testenv_test.go` helpers to allocate and compare the new `GroupKey` strings.
4. Adjust `pkg/replication/isr/doc.go` so the documentation matches the new field names and highlights the non-empty key requirement.
5. Ensure any helper constructors or comments within these files use the new `GroupKey` terminology and the new spec reference.

- [ ] **Step 4: Run the tests again to prove the new API passes**
```
go test ./pkg/replication/isr -run 'TestGroupMetaRejectsEmptyGroupKey|TestReplicaSurfaceUsesGroupKey' -v
```
Expected: PASS with no `ErrInvalidMeta` once `GroupKey` is enforced as a string.

- [ ] **Step 5: Commit the change**

```
git add pkg/replication/isr/types.go pkg/replication/isr/meta.go pkg/replication/isr/doc.go pkg/replication/isr/api_test.go pkg/replication/isr/meta_test.go pkg/replication/isr/testenv_test.go
git commit -m "refactor: add group key to isr api"
```
