# internalv2 Plugin HTTP Forward Fanout Decision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lock `/plugin/httpForward toNodeId=-1` as an explicit deferred compatibility behavior through a real `.wkp` e2ev2 negative smoke and matching flow documentation.

**Architecture:** Keep production logic unchanged because both `internal` and `internalv2` already return `ErrHTTPForwardFanoutDeferred`. Extend the existing three-node plugin HTTP forward e2e scenario so the initiator plugin records a third JSONL entry for the deferred fanout error, then update flow docs to state that deferred behavior is intentional.

**Tech Stack:** Go, `testing`, e2e build tag, real `cmd/wukongimv2` cluster harness, WKRPC plugin socket protocol, protobuf `pluginproto`.

---

## File Structure

- Modify `test/e2ev2/plugin/http_forward/http_forward_test.go`
  - Owns black-box assertions over sandbox JSONL records.
  - Add an `error` field to `httpForwardRecord`.
  - Wait for three records and assert the fanout-deferred error entry.
- Modify `test/e2ev2/plugin/http_forward/testdata/httpforward/main.go`
  - Owns the real `.wkp` compatibility helper.
  - Add a `toNodeId=-1` host RPC call from the initiator and record its error.
- Modify `test/e2ev2/plugin/http_forward/AGENTS.md`
  - Keep scenario rules aligned with the new negative coverage.
- Modify `internalv2/usecase/plugin/FLOW.md`
  - Replace "until fanout compatibility is implemented" with the explicit design decision.
- Modify `internalv2/infra/cluster/FLOW.md`
  - State that `toNodeId=-1` is intentionally deferred and does not enter infra fanout.

No production Go code should change in this plan.

---

### Task 1: Make the e2ev2 scenario expect the deferred fanout record

**Files:**
- Modify: `test/e2ev2/plugin/http_forward/http_forward_test.go`

- [ ] **Step 1: Write the failing e2ev2 assertion**

Change the test to wait for three records and assert a `fanout` error record:

```go
records := waitHTTPForwardRecords(t, node1.resultPath, 3, cluster)
requireHTTPForwardRecord(t, records, "local", 200, "route:httpforward:/local:local-payload")
requireHTTPForwardRecord(t, records, "remote", 200, "route:httpforward:/remote:remote-payload")
requireHTTPForwardErrorRecord(t, records, "fanout", "plugin http forward fanout deferred")
```

Extend `httpForwardRecord`:

```go
type httpForwardRecord struct {
	Mode   string `json:"mode"`
	Status int32  `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}
```

Add this helper near `requireHTTPForwardRecord`:

```go
func requireHTTPForwardErrorRecord(t *testing.T, records []httpForwardRecord, mode string, contains string) {
	t.Helper()
	for _, record := range records {
		if record.Mode != mode {
			continue
		}
		require.Contains(t, record.Error, contains)
		require.Zero(t, record.Status)
		require.Empty(t, record.Body)
		return
	}
	t.Fatalf("error record mode %q not found in %#v", mode, records)
}
```

- [ ] **Step 2: Run the e2ev2 test and verify it fails before plugin support**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/plugin/http_forward -run TestPluginHTTPForwardRoutesLocalAndRemotePluginHTTP -count=1 -timeout 2m
```

Expected: FAIL because only the existing `local` and `remote` JSONL records are written. The failure should mention `want=3` or missing `fanout`.

---

### Task 2: Make the real test plugin record the deferred fanout error

**Files:**
- Modify: `test/e2ev2/plugin/http_forward/testdata/httpforward/main.go`

- [ ] **Step 1: Extend the initiator flow**

Replace `runInitiator` with:

```go
func (c *client) runInitiator() error {
	local, err := c.forwardWithRetry("local", 0, "/local", []byte("local-payload"))
	if err != nil {
		return err
	}
	if err := c.appendRecord("local", local); err != nil {
		return err
	}
	remote, err := c.forwardWithRetry("remote", 2, "/remote", []byte("remote-payload"))
	if err != nil {
		return err
	}
	if err := c.appendRecord("remote", remote); err != nil {
		return err
	}
	fanoutErr := c.forwardExpectError(context.Background(), -1, "/fanout", []byte("fanout-payload"))
	if fanoutErr == nil {
		return errors.New("fanout httpForward unexpectedly succeeded")
	}
	return c.appendErrorRecord("fanout", fanoutErr)
}
```

- [ ] **Step 2: Add the negative helper**

Add this helper near `forwardWithRetry`:

```go
func (c *client) forwardExpectError(ctx context.Context, toNodeID int64, path string, body []byte) error {
	_, err := c.forwardHTTP(ctx, toNodeID, path, body)
	return err
}
```

- [ ] **Step 3: Add an error JSONL writer**

Add this helper after `appendRecord`:

```go
func (c *client) appendErrorRecord(mode string, err error) error {
	path := filepath.Join(c.sandbox, resultsFile)
	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(struct {
		Mode  string `json:"mode"`
		Error string `json:"error"`
	}{
		Mode:  mode,
		Error: err.Error(),
	})
}
```

- [ ] **Step 4: Run the e2ev2 test and verify it passes**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/plugin/http_forward -run TestPluginHTTPForwardRoutesLocalAndRemotePluginHTTP -count=1 -timeout 2m
```

Expected: PASS. The initiator sandbox file should contain `local`, `remote`, and `fanout` records.

---

### Task 3: Align flow docs with the explicit decision

**Files:**
- Modify: `test/e2ev2/plugin/http_forward/AGENTS.md`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Update the scenario rules**

In `test/e2ev2/plugin/http_forward/AGENTS.md`, replace the validation rule:

```markdown
- Validate both local `toNodeId=0` routing and positive-node remote routing.
```

with:

```markdown
- Validate local `toNodeId=0` routing, positive-node remote routing, and the
  explicit deferred error for `toNodeId=-1`.
```

- [ ] **Step 2: Update the usecase flow language**

In `internalv2/usecase/plugin/FLOW.md`, replace:

```text
  -> toNodeId == -1:
       return ErrHTTPForwardFanoutDeferred until fanout compatibility is implemented
```

with:

```text
  -> toNodeId == -1:
       return ErrHTTPForwardFanoutDeferred by explicit compatibility decision
```

Then replace the paragraph after the flow:

```markdown
Remote forwarding is a narrow port owned by app/infra wiring;
the plugin usecase does not call clusterv2 directly and the remote receiver
must execute only the local `/plugin/route` hook.
```

with:

```markdown
Remote forwarding is a narrow port owned by app/infra wiring;
the plugin usecase does not call clusterv2 directly and the remote receiver
must execute only the local `/plugin/route` hook. Fanout `toNodeId=-1` remains
an intentional deferred compatibility path; it must not scan the cluster
snapshot or issue partial remote RPCs.
```

- [ ] **Step 3: Update the infra flow language**

In `internalv2/infra/cluster/FLOW.md`, replace:

```markdown
fanout `toNodeId=-1` remains deferred in the plugin usecase.
```

with:

```markdown
fanout `toNodeId=-1` is intentionally deferred in the plugin usecase and never
enters the infra adapter.
```

- [ ] **Step 4: Run formatting and focused verification**

Run:

```bash
gofmt -w test/e2ev2/plugin/http_forward/http_forward_test.go test/e2ev2/plugin/http_forward/testdata/httpforward/main.go
GOWORK=off go test -tags=e2e ./test/e2ev2/plugin/http_forward -count=1 -timeout 2m
git diff --check
```

Expected: e2ev2 test PASS and `git diff --check` exits 0.

- [ ] **Step 5: Commit the implementation**

Run:

```bash
git status --short
git add test/e2ev2/plugin/http_forward/http_forward_test.go \
  test/e2ev2/plugin/http_forward/testdata/httpforward/main.go \
  test/e2ev2/plugin/http_forward/AGENTS.md \
  internalv2/usecase/plugin/FLOW.md \
  internalv2/infra/cluster/FLOW.md
git commit -m "test: cover plugin http forward fanout deferred"
```

Expected: one commit containing only the negative e2ev2 smoke and flow docs.

---

## Self-Review

- Spec coverage: covers explicit deferred behavior, no production fanout, real `.wkp` e2ev2 negative smoke, and flow documentation.
- Placeholder scan: no placeholder markers or deferred instruction text.
- Type consistency: `httpForwardRecord.Error`, `appendErrorRecord`, and `requireHTTPForwardErrorRecord` use the same JSON field and mode name.
