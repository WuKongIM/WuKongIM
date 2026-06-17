# DB Inspect Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only `/system/db` Web DB inspect console backed by `wukongimv2` manager APIs and `pkg/db/inspect`.

**Architecture:** The browser calls manager HTTP routes only. `internalv2/access/manager` parses HTTP and permission checks, `internalv2/usecase/management` owns node-local versus remote orchestration, `internalv2/app` derives storage paths and adapts `pkg/db/inspect`, and `internalv2/access/node` carries remote node requests over clusterv2 RPC.

**Tech Stack:** Go, Gin, clusterv2 node RPC, `pkg/db/inspect`, React, TypeScript, React Router, React Intl, Vitest, existing Web manager shell.

---

## Scope Check

The spec covers one feature with several dependent layers: backend usecase,
HTTP adapter, node RPC, app wiring, Web API client, Web page, and docs. These
layers must land in order because each layer consumes the previous contract.
The plan keeps each task testable on its own and does not introduce write,
repair, compaction, or cluster-wide merged query behavior.

## Pre-Execution Notes

- Before editing a package, read its `FLOW.md` when present.
- Do not add new `WK_` config keys.
- Keep all DB inspection read-only.
- Do not import `pkg/db/internal/*` outside `pkg/db`.
- Do not accept filesystem paths from HTTP or Web requests.
- Use "single-node cluster" in docs or UI copy for deployment shape; use
  "local node" only for node-local storage behavior.

## File Structure

Backend files:

- Create `internalv2/usecase/management/db_inspect.go`: DB inspect DTOs,
  errors, ports, local-or-remote routing, and wrappers for `show tables` and
  `describe`.
- Create `internalv2/usecase/management/db_inspect_test.go`: usecase routing,
  validation, and unavailable tests.
- Create `internalv2/app/db_inspect.go`: app-level local reader that derives
  `slotmeta` and `messages` paths and calls `pkg/db/inspect`.
- Modify `internalv2/app/wiring.go`: attach local and remote DB inspect readers
  to manager management and register the RPC handler.
- Modify `internalv2/app/app_test.go`: app wiring tests.
- Modify `internalv2/access/manager/server.go`: extend the `Management`
  interface and route registration.
- Create `internalv2/access/manager/db_inspect.go`: HTTP DTOs, parsing, error
  mapping, and handlers.
- Create `internalv2/access/manager/db_inspect_test.go`: HTTP success,
  validation, unavailable, and permission tests.
- Modify `pkg/clusterv2/net/ids.go`: add `RPCManagerDBInspect`.
- Modify `internalv2/access/node/presence_rpc.go`: extend adapter options with
  a manager DB inspect reader.
- Create `internalv2/access/node/manager_db_inspect_codec.go`: deterministic
  versioned RPC envelope with JSON payload for dynamic rows.
- Create `internalv2/access/node/manager_db_inspect_rpc.go`: server and client
  RPC logic.
- Create `internalv2/access/node/manager_db_inspect_rpc_test.go`: codec and
  status mapping tests.
- Create `internalv2/infra/cluster/management_db_inspect.go`: cluster-routed
  remote DB inspect adapter.
- Create `internalv2/infra/cluster/management_db_inspect_test.go`: adapter
  routing tests.

Frontend files:

- Modify `web/src/lib/manager-api.types.ts`: DB inspect types.
- Modify `web/src/lib/manager-api.ts`: URL builders and fetch helpers.
- Modify `web/src/lib/manager-api.test.ts`: client path/body tests.
- Modify `web/src/lib/navigation.ts`: System navigation item and legacy
  redirect.
- Modify `web/src/lib/navigation.test.ts`: navigation metadata tests.
- Modify `web/src/app/router.tsx`: `/system/db` route and `/db-inspect`
  redirect.
- Modify `web/src/app/router.test.tsx`: route coverage.
- Create `web/src/pages/db-inspect/page.tsx`: DB inspect UI.
- Create `web/src/pages/db-inspect/page.test.tsx`: page behavior tests.
- Modify `web/src/i18n/messages/en.ts`: English strings.
- Modify `web/src/i18n/messages/zh-CN.ts`: Chinese strings.

Docs:

- Modify `internalv2/usecase/management/FLOW.md`.
- Modify `internalv2/access/manager/FLOW.md`.
- Modify `internalv2/access/node/FLOW.md`.
- Modify `internalv2/app/FLOW.md`.
- Modify `web/README.md`.

## Task 1: Management Usecase Contract And Routing

**Files:**
- Create: `internalv2/usecase/management/db_inspect.go`
- Create: `internalv2/usecase/management/db_inspect_test.go`
- Modify: `internalv2/usecase/management/nodes.go`

- [ ] **Step 1: Read package flow**

Run:

```bash
sed -n '1,220p' internalv2/usecase/management/FLOW.md
sed -n '1,140p' internalv2/usecase/management/nodes.go
```

Expected: confirm management owns manager read models and `Options` lives in
`nodes.go`.

- [ ] **Step 2: Write failing usecase tests**

Create `internalv2/usecase/management/db_inspect_test.go`:

```go
package management

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDBInspectUsesLocalReaderForEmptyOrLocalNode(t *testing.T) {
	local := &fakeDBInspectReader{
		resp: DBInspectQueryResponse{
			NodeID:      1,
			GeneratedAt: time.Unix(10, 0),
			Rows:        []DBInspectRow{{"table": "meta.user"}},
			Stats:       DBInspectStats{ReturnedRows: 1},
		},
	}
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect: local,
		Now:       func() time.Time { return time.Unix(10, 0) },
	})

	got, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "show tables"})
	if err != nil {
		t.Fatalf("QueryDBInspect(local empty node) error = %v", err)
	}
	if got.NodeID != 1 || len(got.Rows) != 1 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("local empty node response = %#v, want local node result", got)
	}
	if len(local.calls) != 1 || local.calls[0].NodeID != 1 || local.calls[0].Query != "show tables" {
		t.Fatalf("local calls = %#v, want one normalized local call", local.calls)
	}

	got, err = app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 1, Query: "describe meta.user"})
	if err != nil {
		t.Fatalf("QueryDBInspect(local node) error = %v", err)
	}
	if got.NodeID != 1 {
		t.Fatalf("local node response node = %d, want 1", got.NodeID)
	}
	if len(local.calls) != 2 || local.calls[1].Query != "describe meta.user" {
		t.Fatalf("local calls after explicit local = %#v", local.calls)
	}
}

func TestDBInspectRoutesRemoteNode(t *testing.T) {
	local := &fakeDBInspectReader{}
	remote := &fakeRemoteDBInspectReader{
		resp: DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(20, 0),
			Rows:        []DBInspectRow{{"node": float64(2)}},
			Stats:       DBInspectStats{ReturnedRows: 1},
		},
	}
	app := New(Options{
		Cluster:         fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect:       local,
		RemoteDBInspect: remote,
	})

	got, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("QueryDBInspect(remote) error = %v", err)
	}
	if got.NodeID != 2 || len(remote.calls) != 1 || remote.calls[0].NodeID != 2 {
		t.Fatalf("remote response/calls = %#v %#v, want node 2", got, remote.calls)
	}
	if len(local.calls) != 0 {
		t.Fatalf("local calls = %#v, want none for remote query", local.calls)
	}
}

func TestDBInspectRejectsInvalidRequests(t *testing.T) {
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1}},
		DBInspect: &fakeDBInspectReader{},
	})

	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "   "}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("empty query err = %v, want invalid argument", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "../meta", "user"); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("bad domain err = %v, want invalid argument", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "meta", "user/x"); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("bad table err = %v, want invalid argument", err)
	}
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 9, Query: "show tables"}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown node err = %v, want invalid argument", err)
	}
}

func TestDBInspectUnavailableErrors(t *testing.T) {
	app := New(Options{Cluster: fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}}})
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "show tables"}); !errors.Is(err, ErrDBInspectUnavailable) {
		t.Fatalf("missing local reader err = %v, want ErrDBInspectUnavailable", err)
	}

	app = New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect: &fakeDBInspectReader{},
	})
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 2, Query: "show tables"}); !errors.Is(err, ErrDBInspectUnavailable) {
		t.Fatalf("missing remote reader err = %v, want ErrDBInspectUnavailable", err)
	}
}

func TestDBInspectConvenienceQueries(t *testing.T) {
	local := &fakeDBInspectReader{
		resp: DBInspectQueryResponse{NodeID: 1, Rows: []DBInspectRow{{"column": "uid"}}, Stats: DBInspectStats{ReturnedRows: 1}},
	}
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1}},
		DBInspect: local,
	})

	if _, err := app.ListDBInspectTables(context.Background(), 0); err != nil {
		t.Fatalf("ListDBInspectTables() error = %v", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "meta", "user"); err != nil {
		t.Fatalf("DescribeDBInspectTable() error = %v", err)
	}
	want := []string{"show tables", "describe meta.user"}
	var got []string
	for _, call := range local.calls {
		got = append(got, call.Query)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queries = %#v, want %#v", got, want)
	}
}

type fakeDBInspectReader struct {
	resp  DBInspectQueryResponse
	err   error
	calls []DBInspectQueryRequest
}

func (f *fakeDBInspectReader) QueryDBInspect(_ context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeRemoteDBInspectReader struct {
	resp  DBInspectQueryResponse
	err   error
	calls []DBInspectQueryRequest
}

func (f *fakeRemoteDBInspectReader) NodeDBInspectQuery(_ context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeDBInspectCluster struct {
	nodeID uint64
	nodes  []uint64
	err    error
}

func (f fakeDBInspectCluster) NodeID() uint64 { return f.nodeID }

func (f fakeDBInspectCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	if f.err != nil {
		return control.Snapshot{}, f.err
	}
	nodes := make([]control.Node, 0, len(f.nodes))
	for _, id := range f.nodes {
		nodes = append(nodes, control.Node{NodeID: id})
	}
	return control.Snapshot{Nodes: nodes}, nil
}
```

- [ ] **Step 3: Run the failing usecase tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestDBInspect'
```

Expected: compile fails because `DBInspectQueryRequest`,
`DBInspectQueryResponse`, `DBInspectRow`, `DBInspectStats`,
`ErrDBInspectUnavailable`, and DB inspect methods are not defined.

- [ ] **Step 4: Add usecase types and routing**

Create `internalv2/usecase/management/db_inspect.go`:

```go
package management

import (
	"context"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrDBInspectUnavailable reports that read-only DB inspection is not wired.
var ErrDBInspectUnavailable = errors.New("management: db inspect unavailable")

// DBInspectReader executes read-only inspect queries against one node-local DB.
type DBInspectReader interface {
	// QueryDBInspect runs one bounded read-only inspect query.
	QueryDBInspect(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}

// RemoteDBInspectReader executes read-only inspect queries on peer nodes.
type RemoteDBInspectReader interface {
	// NodeDBInspectQuery runs one bounded read-only inspect query on req.NodeID.
	NodeDBInspectQuery(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}

// DBInspectRow is one JSON-friendly inspect result row.
type DBInspectRow map[string]any

// DBInspectQueryRequest configures one manager DB inspect query.
type DBInspectQueryRequest struct {
	// NodeID selects the node-local DB to inspect. Zero means the local node.
	NodeID uint64
	// Query is the read-only inspect SQL statement.
	Query string
}

// DBInspectQueryResponse is one manager DB inspect result.
type DBInspectQueryResponse struct {
	// NodeID is the node that executed the query.
	NodeID uint64
	// GeneratedAt records when the result was produced.
	GeneratedAt time.Time
	// Rows contains dynamic inspect result rows.
	Rows []DBInspectRow
	// Stats summarizes scan work and pagination state.
	Stats DBInspectStats
}

// DBInspectStats summarizes inspect query execution.
type DBInspectStats struct {
	// ScanMode names the scan strategy used by pkg/db/inspect.
	ScanMode string
	// ScannedHashSlots lists metadata hash slots touched by the scan.
	ScannedHashSlots []uint16
	// ScannedRows counts decoded rows considered by the scan.
	ScannedRows int
	// ReturnedRows counts rows returned to the caller.
	ReturnedRows int
	// HasMore reports whether another page is available.
	HasMore bool
	// NextCursor resumes the next page when HasMore is true.
	NextCursor string
}

// ListDBInspectTables returns inspectable DB tables for one node.
func (a *App) ListDBInspectTables(ctx context.Context, nodeID uint64) (DBInspectQueryResponse, error) {
	return a.QueryDBInspect(ctx, DBInspectQueryRequest{NodeID: nodeID, Query: "show tables"})
}

// DescribeDBInspectTable returns inspectable column metadata for one table.
func (a *App) DescribeDBInspectTable(ctx context.Context, nodeID uint64, domain, table string) (DBInspectQueryResponse, error) {
	domain = strings.TrimSpace(domain)
	table = strings.TrimSpace(table)
	if !validDBInspectIdentifier(domain) || !validDBInspectIdentifier(table) {
		return DBInspectQueryResponse{}, metadb.ErrInvalidArgument
	}
	return a.QueryDBInspect(ctx, DBInspectQueryRequest{NodeID: nodeID, Query: "describe " + domain + "." + table})
}

// QueryDBInspect runs one read-only DB inspect query on the selected node.
func (a *App) QueryDBInspect(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return DBInspectQueryResponse{}, metadb.ErrInvalidArgument
	}
	nodeID, local, err := a.resolveDBInspectNode(ctx, req.NodeID)
	if err != nil {
		return DBInspectQueryResponse{}, err
	}
	next := DBInspectQueryRequest{NodeID: nodeID, Query: query}
	if local {
		if a == nil || a.dbInspect == nil {
			return DBInspectQueryResponse{}, ErrDBInspectUnavailable
		}
		return a.dbInspect.QueryDBInspect(ctx, next)
	}
	if a == nil || a.remoteDBInspect == nil {
		return DBInspectQueryResponse{}, ErrDBInspectUnavailable
	}
	return a.remoteDBInspect.NodeDBInspectQuery(ctx, next)
}

func (a *App) resolveDBInspectNode(ctx context.Context, nodeID uint64) (uint64, bool, error) {
	if a == nil || a.cluster == nil {
		if nodeID == 0 {
			return 0, true, nil
		}
		return 0, false, metadb.ErrInvalidArgument
	}
	localID := a.cluster.NodeID()
	if nodeID == 0 || nodeID == localID {
		return localID, true, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return nodeID, false, nil
		}
	}
	return 0, false, metadb.ErrInvalidArgument
}

func validDBInspectIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}
```

Modify `internalv2/usecase/management/nodes.go`:

```go
type Options struct {
	Cluster ControlSnapshotReader
	ChannelRuntimeMeta ChannelRuntimeMetaReader
	ChannelBusinessReader ChannelBusinessReader
	RemoteBusinessChannels RemoteBusinessChannelReader
	Users UserReader
	UserOperator UserOperator
	UserPresence UserPresenceDirectory
	UserActions UserRouteActionDispatcher
	SystemUsers SystemUserOperator
	Conversations ConversationSyncer
	Messages MessageReader
	MessageRetention MessageRetentionOperator
	Connections ConnectionReader
	RemoteConnections RemoteConnectionReader
	Logs LogReader
	// DBInspect runs read-only node-local DB inspect queries.
	DBInspect DBInspectReader
	// RemoteDBInspect runs read-only DB inspect queries on peer nodes.
	RemoteDBInspect RemoteDBInspectReader
	Now func() time.Time
}
```

Add fields to `App`:

```go
dbInspect       DBInspectReader
remoteDBInspect RemoteDBInspectReader
```

Initialize them in `New`:

```go
dbInspect:       opts.DBInspect,
remoteDBInspect: opts.RemoteDBInspect,
```

Keep the existing fields and comments around these additions intact.

- [ ] **Step 5: Run usecase tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestDBInspect'
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

Run:

```bash
git add internalv2/usecase/management/db_inspect.go internalv2/usecase/management/db_inspect_test.go internalv2/usecase/management/nodes.go
git commit -m "feat(management): add db inspect usecase"
```

Expected: commit succeeds with only the three listed files staged.

## Task 2: App Local Inspect Reader

**Files:**
- Create: `internalv2/app/db_inspect.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Read app flow and DB inspect package**

Run:

```bash
sed -n '1,180p' internalv2/app/FLOW.md
sed -n '1,220p' pkg/db/inspect/types.go
sed -n '1,120p' pkg/db/inspect/store.go
```

Expected: confirm app is the composition root and `pkg/db/inspect.OpenStore`
accepts explicit `MetaPath`, `MessagePath`, and `HashSlotCount`.

- [ ] **Step 2: Write failing app reader tests**

Append to `internalv2/app/app_test.go`:

```go
func TestDBInspectReaderQueriesDerivedNodeStorage(t *testing.T) {
	dataDir := t.TempDir()
	metaPath := filepath.Join(dataDir, "slotmeta")
	store, err := metadb.Open(metaPath)
	if err != nil {
		t.Fatalf("open meta store: %v", err)
	}
	if err := store.ForHashSlot(3).CreateUser(context.Background(), metadb.User{UID: "u1", Token: "t1"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close meta store: %v", err)
	}

	reader := newDBInspectReader(dbInspectReaderOptions{
		NodeID:        1,
		DataDir:       dataDir,
		HashSlotCount: 16,
		Now:           func() time.Time { return time.Unix(100, 0) },
	})

	resp, err := reader.QueryDBInspect(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 1,
		Query:  "select uid, token from meta.user where uid='u1' limit 10",
	})
	if err != nil {
		t.Fatalf("QueryDBInspect() error = %v", err)
	}
	if resp.NodeID != 1 || !resp.GeneratedAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("response metadata = node:%d at:%s, want node 1 at 100", resp.NodeID, resp.GeneratedAt)
	}
	if len(resp.Rows) != 1 || resp.Rows[0]["uid"] != "u1" || resp.Rows[0]["token"] != "t1" {
		t.Fatalf("rows = %#v, want seeded user", resp.Rows)
	}
	if resp.Stats.ScanMode != "point-partition" || resp.Stats.ReturnedRows != 1 {
		t.Fatalf("stats = %#v, want point partition one row", resp.Stats)
	}
}

func TestDBInspectReaderRequiresDataDir(t *testing.T) {
	reader := newDBInspectReader(dbInspectReaderOptions{NodeID: 1, HashSlotCount: 16})
	_, err := reader.QueryDBInspect(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 1,
		Query:  "show tables",
	})
	if !errors.Is(err, managementusecase.ErrDBInspectUnavailable) {
		t.Fatalf("QueryDBInspect() err = %v, want ErrDBInspectUnavailable", err)
	}
}
```

Add imports if missing:

```go
import (
	"errors"
	"path/filepath"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestDBInspectReader'
```

Expected: compile fails because `newDBInspectReader` and
`dbInspectReaderOptions` do not exist.

- [ ] **Step 4: Implement the local reader**

Create `internalv2/app/db_inspect.go`:

```go
package app

import (
	"context"
	"path/filepath"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

type dbInspectReaderOptions struct {
	NodeID        uint64
	DataDir       string
	HashSlotCount uint16
	Now           func() time.Time
}

type dbInspectReader struct {
	nodeID        uint64
	dataDir       string
	hashSlotCount uint16
	now           func() time.Time
}

func newDBInspectReader(opts dbInspectReaderOptions) *dbInspectReader {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &dbInspectReader{
		nodeID:        opts.NodeID,
		dataDir:       opts.DataDir,
		hashSlotCount: opts.HashSlotCount,
		now:           now,
	}
}

func (r *dbInspectReader) QueryDBInspect(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if r == nil || r.dataDir == "" {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	store, err := inspect.OpenStore(inspect.Options{
		MetaPath:       filepath.Join(r.dataDir, "slotmeta"),
		MessagePath:    filepath.Join(r.dataDir, "messages"),
		HashSlotCount: r.hashSlotCount,
	})
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	defer store.Close()

	result, err := store.Query(ctx, req.Query)
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	rows := make([]managementusecase.DBInspectRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		next := make(managementusecase.DBInspectRow, len(row))
		for key, value := range row {
			next[key] = value
		}
		rows = append(rows, next)
	}
	nodeID := req.NodeID
	if nodeID == 0 {
		nodeID = r.nodeID
	}
	return managementusecase.DBInspectQueryResponse{
		NodeID:      nodeID,
		GeneratedAt: r.now(),
		Rows:        rows,
		Stats: managementusecase.DBInspectStats{
			ScanMode:         result.Stats.ScanMode,
			ScannedHashSlots: append([]uint16(nil), result.Stats.ScannedHashSlots...),
			ScannedRows:      result.Stats.ScannedRows,
			ReturnedRows:     result.Stats.ReturnedRows,
			HasMore:          result.Stats.HasMore,
			NextCursor:       result.Stats.NextCursor,
		},
	}, nil
}
```

- [ ] **Step 5: Run app reader tests**

Run:

```bash
go test ./internalv2/app -run 'TestDBInspectReader'
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

Run:

```bash
git add internalv2/app/db_inspect.go internalv2/app/app_test.go
git commit -m "feat(app): add local db inspect reader"
```

Expected: commit succeeds.

## Task 3: Manager HTTP Routes

**Files:**
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/db_inspect.go`
- Create: `internalv2/access/manager/db_inspect_test.go`

- [ ] **Step 1: Read manager flow and existing route style**

Run:

```bash
sed -n '1,150p' internalv2/access/manager/FLOW.md
sed -n '213,280p' internalv2/access/manager/server.go
sed -n '1,180p' internalv2/access/manager/messages.go
```

Expected: confirm routes are grouped by permission resource and errors use
`jsonError`.

- [ ] **Step 2: Write failing HTTP tests**

Create `internalv2/access/manager/db_inspect_test.go`:

```go
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestDBInspectRoutesRequirePermission(t *testing.T) {
	allowed := New(Options{
		ListenAddr: "127.0.0.1:0",
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "secret",
			JWTIssuer: "manager-test",
			JWTExpire: time.Hour,
			Users: []UserConfig{{
				Username: "admin",
				Password: "pw",
				Permissions: []PermissionConfig{{Resource: "cluster.db", Actions: []string{"r"}}},
			}},
		},
		Management: &fakeDBInspectManagement{resp: managementusecase.DBInspectQueryResponse{NodeID: 1}},
	})
	token := loginForManagerTest(t, allowed, "admin", "pw")
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	allowed.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	denied := New(Options{
		ListenAddr: "127.0.0.1:0",
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "secret",
			JWTIssuer: "manager-test",
			JWTExpire: time.Hour,
			Users: []UserConfig{{
				Username: "admin",
				Password: "pw",
				Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
			}},
		},
		Management: &fakeDBInspectManagement{},
	})
	token = loginForManagerTest(t, denied, "admin", "pw")
	req = httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	denied.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want 403", rec.Code)
	}
}

func TestDBInspectQuerySuccess(t *testing.T) {
	mgmt := &fakeDBInspectManagement{
		resp: managementusecase.DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(100, 0).UTC(),
			Rows: []managementusecase.DBInspectRow{
				{"table": "meta.user"},
			},
			Stats: managementusecase.DBInspectStats{ScanMode: "local-bounded", ReturnedRows: 1},
		},
	}
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: mgmt})
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"node_id":2,"query":"show tables"}`))
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var body dbInspectQueryResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.NodeID != 2 || body.Rows[0]["table"] != "meta.user" || body.Stats.ScanMode != "local-bounded" {
		t.Fatalf("body = %#v, want DB inspect response", body)
	}
	if len(mgmt.queryCalls) != 1 || mgmt.queryCalls[0].NodeID != 2 || mgmt.queryCalls[0].Query != "show tables" {
		t.Fatalf("query calls = %#v, want forwarded request", mgmt.queryCalls)
	}
}

func TestDBInspectTablesAndDescribe(t *testing.T) {
	mgmt := &fakeDBInspectManagement{
		resp: managementusecase.DBInspectQueryResponse{NodeID: 1, Rows: []managementusecase.DBInspectRow{{"column": "uid"}}},
	}
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: mgmt})

	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables?node_id=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tables status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(mgmt.tableCalls) != 1 || mgmt.tableCalls[0] != 1 {
		t.Fatalf("table calls = %#v, want node 1", mgmt.tableCalls)
	}

	rec = httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables/meta/user?node_id=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("describe status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(mgmt.describeCalls) != 1 || mgmt.describeCalls[0] != "1:meta.user" {
		t.Fatalf("describe calls = %#v, want node 1 meta.user", mgmt.describeCalls)
	}
}

func TestDBInspectHTTPValidationAndUnavailable(t *testing.T) {
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: &fakeDBInspectManagement{err: managementusecase.ErrDBInspectUnavailable}})

	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":""}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, want 503", rec.Code)
	}
}

type fakeDBInspectManagement struct {
	fakeManagement
	resp          managementusecase.DBInspectQueryResponse
	err           error
	queryCalls    []managementusecase.DBInspectQueryRequest
	tableCalls    []uint64
	describeCalls []string
}

func (f *fakeDBInspectManagement) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	f.queryCalls = append(f.queryCalls, req)
	return f.resp, f.err
}

func (f *fakeDBInspectManagement) ListDBInspectTables(_ context.Context, nodeID uint64) (managementusecase.DBInspectQueryResponse, error) {
	f.tableCalls = append(f.tableCalls, nodeID)
	return f.resp, f.err
}

func (f *fakeDBInspectManagement) DescribeDBInspectTable(_ context.Context, nodeID uint64, domain, table string) (managementusecase.DBInspectQueryResponse, error) {
	f.describeCalls = append(f.describeCalls, fmt.Sprintf("%d:%s.%s", nodeID, domain, table))
	return f.resp, f.err
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/access/manager -run 'TestDBInspect'
```

Expected: compile fails because HTTP handlers and management interface methods
are missing.

- [ ] **Step 4: Extend manager server contract and routes**

In `internalv2/access/manager/server.go`, add methods to the `Management`
interface:

```go
ListDBInspectTables(ctx context.Context, nodeID uint64) (managementusecase.DBInspectQueryResponse, error)
DescribeDBInspectTable(ctx context.Context, nodeID uint64, domain, table string) (managementusecase.DBInspectQueryResponse, error)
QueryDBInspect(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error)
```

In `registerRoutes`, add:

```go
dbInspect := s.engine.Group("/manager")
if s.auth.enabled() {
	dbInspect.Use(s.requirePermission("cluster.db", "r"))
}
dbInspect.GET("/db/inspect/tables", s.handleDBInspectTables)
dbInspect.GET("/db/inspect/tables/:domain/:table", s.handleDBInspectTable)
dbInspect.POST("/db/inspect/query", s.handleDBInspectQuery)
```

- [ ] **Step 5: Implement HTTP DTOs and handlers**

Create `internalv2/access/manager/db_inspect.go`:

```go
package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

type dbInspectQueryRequestDTO struct {
	NodeID uint64 `json:"node_id"`
	Query  string `json:"query"`
}

type dbInspectQueryResponseDTO struct {
	NodeID      uint64                   `json:"node_id"`
	GeneratedAt time.Time                `json:"generated_at"`
	Rows        []map[string]any         `json:"rows"`
	Stats       dbInspectStatsResponseDTO `json:"stats"`
}

type dbInspectStatsResponseDTO struct {
	ScanMode         string   `json:"scan_mode"`
	ScannedHashSlots []uint16 `json:"scanned_hash_slots"`
	ScannedRows      int      `json:"scanned_rows"`
	ReturnedRows     int      `json:"returned_rows"`
	HasMore          bool     `json:"has_more"`
	NextCursor       string   `json:"next_cursor"`
}

func (s *Server) handleDBInspectTables(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseDBInspectNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid node_id")
		return
	}
	resp, err := s.management.ListDBInspectTables(c.Request.Context(), nodeID)
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func (s *Server) handleDBInspectTable(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseDBInspectNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid node_id")
		return
	}
	resp, err := s.management.DescribeDBInspectTable(c.Request.Context(), nodeID, c.Param("domain"), c.Param("table"))
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func (s *Server) handleDBInspectQuery(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body dbInspectQueryRequestDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid db inspect query")
		return
	}
	if strings.TrimSpace(body.Query) == "" {
		jsonError(c, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	resp, err := s.management.QueryDBInspect(c.Request.Context(), managementusecase.DBInspectQueryRequest{
		NodeID: body.NodeID,
		Query:  body.Query,
	})
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func parseDBInspectNodeID(raw string) (uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func dbInspectQueryDTO(resp managementusecase.DBInspectQueryResponse) dbInspectQueryResponseDTO {
	rows := make([]map[string]any, 0, len(resp.Rows))
	for _, row := range resp.Rows {
		next := make(map[string]any, len(row))
		for key, value := range row {
			next[key] = value
		}
		rows = append(rows, next)
	}
	return dbInspectQueryResponseDTO{
		NodeID:      resp.NodeID,
		GeneratedAt: resp.GeneratedAt,
		Rows:        rows,
		Stats: dbInspectStatsResponseDTO{
			ScanMode:         resp.Stats.ScanMode,
			ScannedHashSlots: append([]uint16(nil), resp.Stats.ScannedHashSlots...),
			ScannedRows:      resp.Stats.ScannedRows,
			ReturnedRows:     resp.Stats.ReturnedRows,
			HasMore:          resp.Stats.HasMore,
			NextCursor:       resp.Stats.NextCursor,
		},
	}
}

func writeDBInspectError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, dbinspect.ErrCursorMismatch):
		jsonError(c, http.StatusBadRequest, "invalid_cursor", "invalid cursor")
	case errors.Is(err, dbinspect.ErrInvalidQuery), errors.Is(err, dbinspect.ErrUnsupportedQuery), errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid db inspect query")
	case errors.Is(err, managementusecase.ErrDBInspectUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "db inspect unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", "db inspect query failed")
	}
}
```

- [ ] **Step 6: Run manager HTTP tests**

Run:

```bash
go test ./internalv2/access/manager -run 'TestDBInspect'
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Run:

```bash
git add internalv2/access/manager/server.go internalv2/access/manager/db_inspect.go internalv2/access/manager/db_inspect_test.go
git commit -m "feat(manager): add db inspect routes"
```

Expected: commit succeeds.

## Task 4: Node RPC For Remote DB Inspect

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `internalv2/access/node/presence_rpc.go`
- Create: `internalv2/access/node/manager_db_inspect_codec.go`
- Create: `internalv2/access/node/manager_db_inspect_rpc.go`
- Create: `internalv2/access/node/manager_db_inspect_rpc_test.go`
- Create: `internalv2/infra/cluster/management_db_inspect.go`
- Create: `internalv2/infra/cluster/management_db_inspect_test.go`

- [ ] **Step 1: Read node RPC flow**

Run:

```bash
sed -n '1,220p' internalv2/access/node/FLOW.md
sed -n '118,160p' internalv2/access/node/presence_rpc.go
sed -n '1,130p' internalv2/access/node/manager_channels_rpc.go
```

Expected: confirm `Adapter` and `Client` live in `presence_rpc.go`, while
manager RPCs use separate files.

- [ ] **Step 2: Write failing RPC tests**

Create `internalv2/access/node/manager_db_inspect_rpc_test.go`:

```go
package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerDBInspectCodecRoundTrip(t *testing.T) {
	req := managerDBInspectRPCRequest{NodeID: 2, Query: "show tables"}
	body, err := encodeManagerDBInspectRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	gotReq, err := decodeManagerDBInspectRequest(body)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Fatalf("decoded request = %#v, want %#v", gotReq, req)
	}

	resp := managerDBInspectRPCResponse{
		Status: "ok",
		Page: managementusecase.DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(100, 0).UTC(),
			Rows: []managementusecase.DBInspectRow{
				{"table": "meta.user", "payload": []byte("abc")},
			},
			Stats: managementusecase.DBInspectStats{ScanMode: "message-catalog", ReturnedRows: 1},
		},
	}
	body, err = encodeManagerDBInspectResponse(resp)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	gotResp, err := decodeManagerDBInspectResponse(body)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if gotResp.Status != "ok" || gotResp.Page.NodeID != 2 || gotResp.Page.Rows[0]["table"] != "meta.user" {
		t.Fatalf("decoded response = %#v, want encoded response", gotResp)
	}
}

func TestManagerDBInspectRPCServerAndClient(t *testing.T) {
	reader := &fakeManagerDBInspectReader{
		resp: managementusecase.DBInspectQueryResponse{
			NodeID: 2,
			Rows:   []managementusecase.DBInspectRow{{"table": "meta.user"}},
			Stats:  managementusecase.DBInspectStats{ReturnedRows: 1},
		},
	}
	adapter := NewAdapter(AdapterOptions{ManagerDBInspect: reader})
	reqBody, err := encodeManagerDBInspectRequest(managerDBInspectRPCRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerDBInspectRPC(context.Background(), reqBody)
	if err != nil {
		t.Fatalf("HandleManagerDBInspectRPC() error = %v", err)
	}
	resp, err := decodeManagerDBInspectResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || resp.Page.Rows[0]["table"] != "meta.user" {
		t.Fatalf("server response = %#v, want ok table row", resp)
	}

	clientNode := &fakeManagerDBInspectRPCNode{response: resp}
	got, err := NewClient(clientNode).NodeDBInspectQuery(context.Background(), managementusecase.DBInspectQueryRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("NodeDBInspectQuery() error = %v", err)
	}
	if got.NodeID != 2 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("client response = %#v, want decoded page", got)
	}
	if clientNode.serviceID != ManagerDBInspectRPCServiceID || clientNode.nodeID != 2 {
		t.Fatalf("client call service/node = %d/%d, want db inspect service node 2", clientNode.serviceID, clientNode.nodeID)
	}
}

func TestManagerDBInspectRPCStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   error
	}{
		{name: "invalid request", status: rpcStatusInvalidRequest, want: managementusecase.ErrDBInspectUnavailable},
		{name: "invalid cursor", status: rpcStatusInvalidCursor, want: managementusecase.ErrDBInspectUnavailable},
		{name: "unavailable", status: rpcStatusUnavailable, want: managementusecase.ErrDBInspectUnavailable},
		{name: "rejected", status: rpcStatusRejected, want: managementusecase.ErrDBInspectUnavailable},
		{name: "canceled", status: rpcStatusContextCanceled, want: context.Canceled},
		{name: "deadline", status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := managerDBInspectRPCErrorForStatus(tc.status)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

type fakeManagerDBInspectReader struct {
	resp managementusecase.DBInspectQueryResponse
	err  error
}

func (f *fakeManagerDBInspectReader) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if f.err != nil {
		return managementusecase.DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeManagerDBInspectRPCNode struct {
	response  managerDBInspectRPCResponse
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeManagerDBInspectRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return encodeManagerDBInspectResponse(f.response)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/access/node -run 'TestManagerDBInspect'
```

Expected: compile fails because DB inspect RPC codec, service ID, adapter
option, and client method are missing.

- [ ] **Step 4: Add RPC service ID**

Modify `pkg/clusterv2/net/ids.go` by appending after
`RPCManagerChannels`:

```go
// RPCManagerDBInspect serves internalv2 node-local manager DB inspect reads.
RPCManagerDBInspect
```

- [ ] **Step 5: Extend Adapter options**

In `internalv2/access/node/presence_rpc.go`, add to `AdapterOptions`:

```go
// ManagerDBInspect reads node-local DB inspect pages for manager pages.
ManagerDBInspect ManagerDBInspectReader
```

Add to `Adapter`:

```go
// managerDBInspect reads node-local DB inspect results for manager pages.
managerDBInspect ManagerDBInspectReader
```

Initialize in `NewAdapter`:

```go
managerDBInspect: opts.ManagerDBInspect,
```

Add the interface near other manager interfaces:

```go
type ManagerDBInspectReader interface {
	QueryDBInspect(context.Context, managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error)
}
```
`internalv2/access/node/presence_rpc.go` already imports
`managementusecase`, so this interface can reuse that existing import.

- [ ] **Step 6: Implement codec**

Create `internalv2/access/node/manager_db_inspect_codec.go`:

```go
package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerDBInspectRequestMagic  = [...]byte{'W', 'K', 'V', 'I', 1}
	managerDBInspectResponseMagic = [...]byte{'W', 'K', 'V', 'i', 1}
)

type managerDBInspectRPCRequest struct {
	NodeID uint64 `json:"node_id"`
	Query  string `json:"query"`
}

type managerDBInspectRPCResponse struct {
	Status string                                      `json:"status"`
	Page   managementusecase.DBInspectQueryResponse `json:"page"`
}

func encodeManagerDBInspectRequest(req managerDBInspectRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerDBInspectRequestMagic)+len(payload))
	dst = append(dst, managerDBInspectRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerDBInspectRequest(body []byte) (managerDBInspectRPCRequest, error) {
	if !hasMagic(body, managerDBInspectRequestMagic[:]) {
		return managerDBInspectRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager db inspect request codec")
	}
	var req managerDBInspectRPCRequest
	if err := json.Unmarshal(body[len(managerDBInspectRequestMagic):], &req); err != nil {
		return managerDBInspectRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerDBInspectResponse(resp managerDBInspectRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerDBInspectResponseMagic)+len(payload))
	dst = append(dst, managerDBInspectResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerDBInspectResponse(body []byte) (managerDBInspectRPCResponse, error) {
	if !hasMagic(body, managerDBInspectResponseMagic[:]) {
		return managerDBInspectRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager db inspect response codec")
	}
	var resp managerDBInspectRPCResponse
	if err := json.Unmarshal(body[len(managerDBInspectResponseMagic):], &resp); err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	return resp, nil
}
```

- [ ] **Step 7: Implement RPC server and client**

Create `internalv2/access/node/manager_db_inspect_rpc.go`:

```go
package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const ManagerDBInspectRPCServiceID uint8 = clusternet.RPCManagerDBInspect

const (
	rpcStatusInvalidRequest = "invalid_request"
	rpcStatusInvalidCursor  = "invalid_cursor"
	rpcStatusUnavailable    = "unavailable"
)

func (a *Adapter) HandleManagerDBInspectRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerDBInspectRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager db inspect rpc decode failed",
			wklog.Event("internalv2.access.node.manager_db_inspect_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerDBInspect == nil {
		return encodeManagerDBInspectResponse(managerDBInspectRPCResponse{Status: rpcStatusUnavailable})
	}
	page, err := a.managerDBInspect.QueryDBInspect(ctx, managementusecase.DBInspectQueryRequest{
		NodeID: req.NodeID,
		Query:  req.Query,
	})
	status := managerDBInspectRPCStatusForError(err)
	if err != nil && status == rpcStatusRejected {
		a.rpcLogger().Warn("manager db inspect rpc rejected",
			wklog.Event("internalv2.access.node.manager_db_inspect_rejected"),
			wklog.Uint64("nodeID", req.NodeID),
			wklog.Error(err),
		)
	}
	return encodeManagerDBInspectResponse(managerDBInspectRPCResponse{Status: status, Page: page})
}

func (c *Client) NodeDBInspectQuery(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	resp, err := c.callManagerDBInspect(ctx, req.NodeID, managerDBInspectRPCRequest{
		NodeID: req.NodeID,
		Query:  req.Query,
	})
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	if err := managerDBInspectRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	return resp.Page, nil
}

func (c *Client) callManagerDBInspect(ctx context.Context, nodeID uint64, req managerDBInspectRPCRequest) (managerDBInspectRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerDBInspectRPCResponse{}, fmt.Errorf("internalv2/access/node: manager db inspect rpc client not configured")
	}
	body, err := encodeManagerDBInspectRequest(req)
	if err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerDBInspectRPCServiceID, body)
	if err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	return decodeManagerDBInspectResponse(respBody)
}

func managerDBInspectRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, dbinspect.ErrCursorMismatch):
		return rpcStatusInvalidCursor
	case errors.Is(err, dbinspect.ErrInvalidQuery), errors.Is(err, dbinspect.ErrUnsupportedQuery), errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidRequest
	case errors.Is(err, managementusecase.ErrDBInspectUnavailable):
		return rpcStatusUnavailable
	default:
		return rpcStatusRejected
	}
}

func managerDBInspectRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidRequest, rpcStatusInvalidCursor, rpcStatusUnavailable, rpcStatusRejected:
		return managementusecase.ErrDBInspectUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager db inspect rpc status %q", status)
	}
}
```

- [ ] **Step 8: Run node RPC tests**

Run:

```bash
go test ./internalv2/access/node -run 'TestManagerDBInspect'
```

Expected: PASS.

- [ ] **Step 9: Write failing infra adapter test**

Create `internalv2/infra/cluster/management_db_inspect_test.go`:

```go
package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementDBInspectReaderRoutesToNodeRPC(t *testing.T) {
	adapter := accessnode.New(accessnode.Options{
		ManagerDBInspect: fakeDBInspectLocalReader{
			resp: managementusecase.DBInspectQueryResponse{
				NodeID: 2,
				Rows: []managementusecase.DBInspectRow{
					{"table": "meta.user"},
				},
				Stats: managementusecase.DBInspectStats{ReturnedRows: 1},
			},
		},
	})
	node := &fakeManagementDBInspectNode{handler: adapter.HandleManagerDBInspectRPC}
	reader := NewManagementDBInspectReader(node)

	got, err := reader.NodeDBInspectQuery(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 2,
		Query:  "show tables",
	})
	if err != nil {
		t.Fatalf("NodeDBInspectQuery() error = %v", err)
	}
	if got.NodeID != 2 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("response = %#v, want table row from target node", got)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerDBInspectRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerDBInspectRPCServiceID)
	}
}

type fakeDBInspectLocalReader struct {
	resp managementusecase.DBInspectQueryResponse
}

func (f fakeDBInspectLocalReader) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeManagementDBInspectNode struct {
	handler         func(context.Context, []byte) ([]byte, error)
	calledNodeID   uint64
	calledServiceID uint8
}

func (f *fakeManagementDBInspectNode) NodeID() uint64 {
	return 1
}

func (f *fakeManagementDBInspectNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}
```

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestManagementDBInspectReader'
```

Expected: compile fails because `ManagementDBInspectReader` is missing.

- [ ] **Step 10: Implement infra adapter**

Create `internalv2/infra/cluster/management_db_inspect.go`:

```go
package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementDBInspectNode exposes clusterv2 node RPC for manager DB inspect reads.
type ManagementDBInspectNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementDBInspectReader routes manager DB inspect queries to selected nodes.
type ManagementDBInspectReader struct {
	node   ManagementDBInspectNode
	remote *accessnode.Client
}

// NewManagementDBInspectReader creates a cluster-routed manager DB inspect reader.
func NewManagementDBInspectReader(node ManagementDBInspectNode) *ManagementDBInspectReader {
	return &ManagementDBInspectReader{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// NodeDBInspectQuery runs a read-only DB inspect query on one node.
func (r *ManagementDBInspectReader) NodeDBInspectQuery(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if r == nil || r.remote == nil {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	return r.remote.NodeDBInspectQuery(ctx, req)
}
```

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestManagementDBInspectReader'
```

Expected: PASS.

- [ ] **Step 11: Commit Task 4**

Run:

```bash
git add pkg/clusterv2/net/ids.go internalv2/access/node/presence_rpc.go internalv2/access/node/manager_db_inspect_codec.go internalv2/access/node/manager_db_inspect_rpc.go internalv2/access/node/manager_db_inspect_rpc_test.go internalv2/infra/cluster/management_db_inspect.go internalv2/infra/cluster/management_db_inspect_test.go
git commit -m "feat(node): add manager db inspect rpc"
```

Expected: commit succeeds.

## Task 5: App Wiring

**Files:**
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Read current manager wiring**

Run:

```bash
sed -n '320,370p' internalv2/app/wiring.go
sed -n '649,710p' internalv2/app/wiring.go
```

Expected: find manager connection, log, and channel RPC registration plus
`newManagerManagement`.

- [ ] **Step 2: Write failing wiring tests**

Append to `internalv2/app/app_test.go`:

```go
func TestAppWiresManagerDBInspectRoute(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		NodeID:  1,
		DataDir: dir,
		Cluster: clusterv2.Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:0",
			DataDir:    dir,
			Slots:      clusterv2.SlotConfig{HashSlotCount: 16},
		},
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{Resource: "cluster.db", Actions: []string{"r"}}},
			}},
		},
	}
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := newTestApp(t, cfg, WithCluster(cluster))
	if err != nil {
		t.Fatalf("newTestApp() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginRec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`))
	req.Header.Set("Authorization", "Bearer "+login.AccessToken)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("db inspect status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func TestAppRegistersManagerDBInspectRPC(t *testing.T) {
	dir := t.TempDir()
	cluster := &fakeManagerCluster{nodeID: 1}
	cfg := Config{
		NodeID:  1,
		DataDir: dir,
		Cluster: clusterv2.Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:0",
			DataDir:    dir,
			Slots:      clusterv2.SlotConfig{HashSlotCount: 16},
		},
		Manager: ManagerConfig{ListenAddr: ":0"},
	}
	if _, err := newTestApp(t, cfg, WithCluster(cluster)); err != nil {
		t.Fatalf("newTestApp() error = %v", err)
	}
	if _, ok := cluster.handlers[accessnode.ManagerDBInspectRPCServiceID]; !ok {
		t.Fatal("manager db inspect rpc handler not registered")
	}
}
```

Use existing imports and fake cluster helpers in `app_test.go`. If `strings`,
`httptest`, `json`, `accessmanager`, or `accessnode` already exist in the file,
do not duplicate imports.

- [ ] **Step 3: Run app wiring tests to verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestAppWiresManagerDBInspectRoute|TestAppRegistersManagerDBInspectRPC'
```

Expected: tests fail because wiring does not attach DB inspect yet.

- [ ] **Step 4: Wire local and remote DB inspect**

In `internalv2/app/wiring.go`, add a helper:

```go
func (a *App) newDBInspectReader() *dbInspectReader {
	if a == nil || strings.TrimSpace(a.cfg.DataDir) == "" {
		return nil
	}
	hashSlotCount := a.cfg.Cluster.Slots.HashSlotCount
	if hashSlotCount == 0 {
		hashSlotCount = 16
	}
	return newDBInspectReader(dbInspectReaderOptions{
		NodeID:        a.cfg.NodeID,
		DataDir:       a.cfg.DataDir,
		HashSlotCount: hashSlotCount,
	})
}
```

Add a new helper immediately after `wireManagerChannelRPC` in
`internalv2/app/wiring.go`:

```go
func (a *App) wireManagerDBInspectRPC() {
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasRegistrar {
		return
	}
	reader := a.newDBInspectReader()
	adapter := accessnode.New(accessnode.Options{ManagerDBInspect: reader, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerDBInspectRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerDBInspectRPC))
}
```

In `internalv2/app/app.go`, add the new call immediately after
`app.wireManagerChannelRPC()`:

```go
app.wireManagerDBInspectRPC()
```

In `newManagerManagement`, add:

```go
opts.DBInspect = a.newDBInspectReader()
if rpcNode, ok := a.cluster.(clusterinfra.ManagementDBInspectNode); ok {
	opts.RemoteDBInspect = clusterinfra.NewManagementDBInspectReader(rpcNode)
}
```

- [ ] **Step 5: Run app wiring tests**

Run:

```bash
go test ./internalv2/app -run 'TestAppWiresManagerDBInspectRoute|TestAppRegistersManagerDBInspectRPC'
```

Expected: PASS.

- [ ] **Step 6: Commit Task 5**

Run:

```bash
git add internalv2/app/wiring.go internalv2/app/app_test.go
git commit -m "feat(app): wire manager db inspect"
```

Expected: commit succeeds.

## Task 6: Web API Client, Navigation, And Router

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/lib/navigation.test.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`

- [ ] **Step 1: Read Web API and routing patterns**

Run:

```bash
sed -n '340,430p' web/src/lib/manager-api.ts
sed -n '1,180p' web/src/lib/navigation.ts
sed -n '1,120p' web/src/app/router.tsx
```

Expected: confirm API functions use `jsonManagerFetch`, navigation uses
message IDs, and routes are nested under `AppShell`.

- [ ] **Step 2: Write failing Web API tests**

Add to `web/src/lib/manager-api.test.ts`:

```ts
test("builds DB inspect table and query requests", async () => {
  const fetchMock = vi.spyOn(globalThis, "fetch")
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
  await getDBInspectTables({ nodeId: 2 })
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/manager/db/inspect/tables?node_id=2",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )

  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
  await getDBInspectTable("meta", "user", { nodeId: 2 })
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/manager/db/inspect/tables/meta/user?node_id=2",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )

  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ rows: [], stats: {} }), { status: 200 }))
  await queryDBInspect({ node_id: 2, query: "show tables" })
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/manager/db/inspect/query",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ node_id: 2, query: "show tables" }),
    }),
  )
})
```

Ensure imports include:

```ts
import { getDBInspectTable, getDBInspectTables, queryDBInspect } from "@/lib/manager-api"
```

- [ ] **Step 3: Write failing navigation/router tests**

Add to `web/src/lib/navigation.test.ts`:

```ts
test("includes DB inspect in system navigation", () => {
  const item = pageMetadata.get("/system/db")
  expect(item?.titleMessageId).toBe("nav.dbInspect.title")
  expect(legacyRouteRedirects["/db-inspect"]).toBe("/system/db")
})
```

Add to `web/src/app/router.test.tsx`:

```tsx
test("redirects legacy DB inspect route to system DB page", () => {
  const route = routes[1].children?.find((item) => item.path === "db-inspect")
  expect(route).toBeTruthy()
})
```

- [ ] **Step 4: Run Web tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx
```

Expected: compile or assertion failures for missing DB inspect API functions
and route metadata.

- [ ] **Step 5: Add Web API types**

Append to `web/src/lib/manager-api.types.ts`:

```ts
export type ManagerDBInspectRow = Record<string, unknown>

export type ManagerDBInspectStats = {
  scan_mode: string
  scanned_hash_slots: number[]
  scanned_rows: number
  returned_rows: number
  has_more: boolean
  next_cursor: string
}

export type ManagerDBInspectQueryResponse = {
  node_id: number
  generated_at: string
  rows: ManagerDBInspectRow[]
  stats: ManagerDBInspectStats
}

export type ManagerDBInspectTablesResponse = ManagerDBInspectQueryResponse

export type ManagerDBInspectDescribeResponse = ManagerDBInspectQueryResponse

export type ManagerDBInspectQueryInput = {
  node_id?: number
  query: string
}
```

- [ ] **Step 6: Add Web API functions**

In `web/src/lib/manager-api.ts`, import the new types and add:

```ts
function buildDBInspectNodeSearch(params?: { nodeId?: number }) {
  const search = new URLSearchParams()
  if (typeof params?.nodeId === "number") {
    search.set("node_id", String(params.nodeId))
  }
  const query = search.toString()
  return query ? `?${query}` : ""
}

export function getDBInspectTables(params?: { nodeId?: number }) {
  return jsonManagerFetch<ManagerDBInspectTablesResponse>(
    `/manager/db/inspect/tables${buildDBInspectNodeSearch(params)}`,
  )
}

export function getDBInspectTable(domain: string, table: string, params?: { nodeId?: number }) {
  return jsonManagerFetch<ManagerDBInspectDescribeResponse>(
    `/manager/db/inspect/tables/${encodeURIComponent(domain)}/${encodeURIComponent(table)}${buildDBInspectNodeSearch(params)}`,
  )
}

export function queryDBInspect(input: ManagerDBInspectQueryInput) {
  return jsonManagerFetch<ManagerDBInspectQueryResponse>("/manager/db/inspect/query", {
    method: "POST",
    body: JSON.stringify(input),
  })
}
```

- [ ] **Step 7: Add navigation and router**

In `web/src/lib/navigation.ts`, import `Database` is already present. Add to the
System section before Webhooks:

```ts
{
  href: "/system/db",
  titleMessageId: "nav.dbInspect.title",
  descriptionMessageId: "nav.dbInspect.description",
  pathLabelMessageId: "nav.path.system.dbInspect",
  icon: Database,
  aliases: ["/db-inspect"],
},
```

Add legacy redirect:

```ts
"/db-inspect": "/system/db",
```

In `web/src/app/router.tsx`, import:

```tsx
import { DBInspectPage } from "@/pages/db-inspect/page"
```

Add System child route:

```tsx
{ path: "system/db", element: <DBInspectPage /> },
```

Add legacy redirect:

```tsx
{ path: "db-inspect", element: <Navigate replace to="/system/db" /> },
```

- [ ] **Step 8: Add temporary page shell for route compile**

Create `web/src/pages/db-inspect/page.tsx`:

```tsx
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

export function DBInspectPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "dbInspect.title" })}
        description={intl.formatMessage({ id: "dbInspect.description" })}
      />
    </PageContainer>
  )
}
```

Task 7 will replace this with the full UI.

- [ ] **Step 9: Add minimal i18n keys for compile**

Add to `web/src/i18n/messages/en.ts`:

```ts
"nav.path.system.dbInspect": "SYSTEM / DB INSPECT",
"nav.dbInspect.title": "DB Inspect",
"nav.dbInspect.description": "Read-only node-local DB inspection.",
"dbInspect.title": "DB Inspect",
"dbInspect.description": "Run bounded read-only inspect queries against one node-local DB.",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```ts
"nav.path.system.dbInspect": "SYSTEM / DB INSPECT",
"nav.dbInspect.title": "DB 检查",
"nav.dbInspect.description": "只读查看节点本地 DB。",
"dbInspect.title": "DB 检查",
"dbInspect.description": "对一个节点本地 DB 执行有界只读检查查询。",
```

- [ ] **Step 10: Run Web routing/API tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx
```

Expected: PASS.

- [ ] **Step 11: Commit Task 6**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/app/router.tsx web/src/app/router.test.tsx web/src/pages/db-inspect/page.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): add db inspect route and client"
```

Expected: commit succeeds.

## Task 7: Web DB Inspect Page

**Files:**
- Modify: `web/src/pages/db-inspect/page.tsx`
- Create: `web/src/pages/db-inspect/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/db-inspect/page.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { DBInspectPage } from "@/pages/db-inspect/page"

const getNodesMock = vi.fn()
const getDBInspectTablesMock = vi.fn()
const getDBInspectTableMock = vi.fn()
const queryDBInspectMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getDBInspectTables: (...args: unknown[]) => getDBInspectTablesMock(...args),
    getDBInspectTable: (...args: unknown[]) => getDBInspectTableMock(...args),
    queryDBInspect: (...args: unknown[]) => queryDBInspectMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset()
  getDBInspectTablesMock.mockReset()
  getDBInspectTableMock.mockReset()
  queryDBInspectMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-06-17T12:00:00Z",
    permissions: [{ resource: "cluster.db", actions: ["r"] }],
  })
})

function renderPage() {
  return render(
    <I18nProvider>
      <DBInspectPage />
    </I18nProvider>,
  )
}

function nodesResponse() {
  return {
    generated_at: "2026-06-17T10:00:00Z",
    controller_leader_id: 1,
    items: [
      {
        node_id: 1,
        name: "node-1",
        addr: "127.0.0.1:7001",
        status: "online",
        last_heartbeat_at: "2026-06-17T10:00:00Z",
        is_local: true,
        capacity_weight: 100,
        membership: { role: "voter", join_state: "joined", schedulable: true },
        health: { status: "online", suspect: false, dead: false, draining: false },
        controller: { role: "leader", voter: true },
        slots: { leader_count: 1, replica_count: 1 },
        runtime: { online_users: null, online_connections: null },
        actions: { can_drain: false, can_resume: false, can_remove: false },
      },
    ],
  }
}

test("loads table list and runs a query", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "meta", name: "user", table: "meta.user" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1", token: "t1" }],
    stats: { scan_mode: "point-partition", scanned_hash_slots: [3], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  expect(await screen.findByText("meta.user")).toBeInTheDocument()
  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select uid, token from meta.user where uid='u1' limit 20")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  expect(queryDBInspectMock).toHaveBeenCalledWith({
    node_id: 1,
    query: "select uid, token from meta.user where uid='u1' limit 20",
  })
  expect(await screen.findByText("u1")).toBeInTheDocument()
  expect(screen.getByText("point-partition")).toBeInTheDocument()
})

test("describes a selected table", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ domain: "message", name: "message", table: "message.message" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })
  getDBInspectTableMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [{ column: "message_seq", type: "uint64" }],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.click(await screen.findByRole("button", { name: "Inspect message.message" }))
  expect(getDBInspectTableMock).toHaveBeenCalledWith("message", "message", { nodeId: 1 })
  expect(await screen.findByText("message_seq")).toBeInTheDocument()
  expect(screen.getByText("uint64")).toBeInTheDocument()
})

test("runs next page with cursor", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:01Z",
    rows: [{ uid: "u1" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [1], scanned_rows: 1, returned_rows: 1, has_more: true, next_cursor: "abc" },
  })
  queryDBInspectMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:02Z",
    rows: [{ uid: "u2" }],
    stats: { scan_mode: "local-bounded", scanned_hash_slots: [2], scanned_rows: 1, returned_rows: 1, has_more: false, next_cursor: "" },
  })

  const user = userEvent.setup()
  renderPage()

  await user.click(screen.getByRole("button", { name: "Run query" }))
  await screen.findByText("u1")
  await user.click(screen.getByRole("button", { name: "Next page" }))

  expect(queryDBInspectMock).toHaveBeenLastCalledWith({
    node_id: 1,
    query: "show tables cursor 'abc'",
  })
  expect(await screen.findByText("u2")).toBeInTheDocument()
})

test("keeps query text when the query fails", async () => {
  getNodesMock.mockResolvedValueOnce(nodesResponse())
  getDBInspectTablesMock.mockResolvedValueOnce({
    node_id: 1,
    generated_at: "2026-06-17T10:00:00Z",
    rows: [],
    stats: { scan_mode: "", scanned_hash_slots: [], scanned_rows: 0, returned_rows: 0, has_more: false, next_cursor: "" },
  })
  queryDBInspectMock.mockRejectedValueOnce(new ManagerApiError(400, "invalid_request", "invalid db inspect query"))

  const user = userEvent.setup()
  renderPage()

  await user.clear(screen.getByLabelText("Inspect query"))
  await user.type(screen.getByLabelText("Inspect query"), "select * from meta.user order by uid")
  await user.click(screen.getByRole("button", { name: "Run query" }))

  expect(await screen.findByText("invalid db inspect query")).toBeInTheDocument()
  expect(screen.getByLabelText("Inspect query")).toHaveValue("select * from meta.user order by uid")
})
```

- [ ] **Step 2: Run page tests to verify they fail**

Run:

```bash
cd web && bun run test -- src/pages/db-inspect/page.test.tsx
```

Expected: tests fail because the page shell has no real behavior.

- [ ] **Step 3: Implement the DB Inspect page**

Replace `web/src/pages/db-inspect/page.tsx` with:

```tsx
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react"
import { Database, Play, RefreshCw } from "lucide-react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  getDBInspectTable,
  getDBInspectTables,
  getNodes,
  ManagerApiError,
  queryDBInspect,
} from "@/lib/manager-api"
import type {
  ManagerDBInspectQueryResponse,
  ManagerDBInspectRow,
  ManagerNode,
} from "@/lib/manager-api.types"

type TableRow = {
  domain: string
  name: string
  table: string
}

type PageState = {
  nodes: ManagerNode[]
  tables: TableRow[]
  selectedNodeId: number
  query: string
  result: ManagerDBInspectQueryResponse | null
  describe: ManagerDBInspectQueryResponse | null
  selectedTable: string
  loading: boolean
  running: boolean
  describing: boolean
  error: Error | null
}

const defaultQuery = "show tables"

const templates = [
  "show tables",
  "describe meta.user",
  "select * from meta.user where uid='u1' limit 20",
  "select * from meta.channel where channel_id='g1' limit 20",
  "select * from message.channels limit 50",
  "select * from message.message where channel_key='g1:2' limit 20",
]

function errorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function rowColumns(rows: ManagerDBInspectRow[]) {
  const seen = new Set<string>()
  const columns: string[] = []
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (!seen.has(key)) {
        seen.add(key)
        columns.push(key)
      }
    }
  }
  return columns
}

function renderCell(value: unknown) {
  if (value === null || value === undefined || value === "") {
    return "-"
  }
  if (Array.isArray(value)) {
    return value.join(", ")
  }
  if (typeof value === "object") {
    return JSON.stringify(value)
  }
  return String(value)
}

function appendCursor(query: string, cursor: string) {
  const trimmed = query.trim().replace(/\s+cursor\s+'[^']+'\s*$/i, "")
  return `${trimmed} cursor '${cursor}'`
}

export function DBInspectPage() {
  const intl = useIntl()
  const [state, setState] = useState<PageState>({
    nodes: [],
    tables: [],
    selectedNodeId: 0,
    query: defaultQuery,
    result: null,
    describe: null,
    selectedTable: "",
    loading: true,
    running: false,
    describing: false,
    error: null,
  })

  const selectedNodeId = state.selectedNodeId || state.nodes[0]?.node_id || 0

  const loadInitial = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const nodes = await getNodes()
      const selected = nodes.items.find((node) => node.is_local)?.node_id ?? nodes.items[0]?.node_id ?? 0
      const tables = await getDBInspectTables(selected ? { nodeId: selected } : undefined)
      setState((current) => ({
        ...current,
        nodes: nodes.items,
        selectedNodeId: selected,
        tables: tableRows(tables.rows),
        result: tables,
        loading: false,
        error: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error : new Error("db inspect request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    void loadInitial()
  }, [loadInitial])

  const runQuery = async (nextQuery?: string) => {
    const query = nextQuery ?? state.query
    if (!query.trim()) {
      setState((current) => ({ ...current, error: new Error(intl.formatMessage({ id: "dbInspect.error.emptyQuery" })) }))
      return
    }
    setState((current) => ({ ...current, query, running: true, error: null }))
    try {
      const result = await queryDBInspect({ node_id: selectedNodeId || undefined, query: query.trim() })
      setState((current) => ({ ...current, result, running: false, error: null }))
    } catch (error) {
      setState((current) => ({
        ...current,
        running: false,
        error: error instanceof Error ? error : new Error("db inspect query failed"),
      }))
    }
  }

  const describeTable = async (tableName: string) => {
    const [domain, table] = tableName.split(".")
    setState((current) => ({ ...current, describing: true, selectedTable: tableName, error: null }))
    try {
      const describe = await getDBInspectTable(domain, table, selectedNodeId ? { nodeId: selectedNodeId } : undefined)
      setState((current) => ({ ...current, describe, describing: false }))
    } catch (error) {
      setState((current) => ({
        ...current,
        describing: false,
        error: error instanceof Error ? error : new Error("describe table failed"),
      }))
    }
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void runQuery()
  }

  const columns = useMemo(() => rowColumns(state.result?.rows ?? []), [state.result])
  const describeColumns = useMemo(() => rowColumns(state.describe?.rows ?? []), [state.describe])
  const tablesByDomain = useMemo(() => groupTables(state.tables), [state.tables])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "dbInspect.title" })}
        description={intl.formatMessage({ id: "dbInspect.description" })}
        actions={(
          <Button onClick={() => void loadInitial()} size="sm" variant="outline">
            <RefreshCw className="mr-2 size-4" />
            {intl.formatMessage({ id: "common.refresh" })}
          </Button>
        )}
      />

      <div className="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]">
        <SectionCard title={intl.formatMessage({ id: "dbInspect.tables.title" })}>
          {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "dbInspect.tables.title" })} /> : null}
          {!state.loading && state.error && state.tables.length === 0 ? (
            <ResourceState
              kind={errorKind(state.error)}
              title={intl.formatMessage({ id: "dbInspect.title" })}
              description={state.error.message}
              onRetry={() => void loadInitial()}
            />
          ) : null}
          {!state.loading && state.tables.length > 0 ? (
            <div className="space-y-4">
              {Object.entries(tablesByDomain).map(([domain, rows]) => (
                <div key={domain}>
                  <div className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                    {domain}
                  </div>
                  <div className="space-y-1">
                    {rows.map((row) => (
                      <button
                        aria-label={intl.formatMessage({ id: "dbInspect.table.inspect" }, { table: row.table })}
                        className="flex w-full items-center justify-between rounded-md border border-border/70 px-3 py-2 text-left text-sm hover:bg-muted/50"
                        key={row.table}
                        onClick={() => void describeTable(row.table)}
                        type="button"
                      >
                        <span className="font-mono">{row.table}</span>
                        <Database className="size-4 text-muted-foreground" />
                      </button>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          ) : null}
        </SectionCard>

        <div className="space-y-4">
          <SectionCard title={intl.formatMessage({ id: "dbInspect.query.title" })}>
            <form className="space-y-3" onSubmit={submit}>
              <div className="flex flex-wrap items-center gap-3">
                <label className="text-sm font-medium text-foreground" htmlFor="db-inspect-node">
                  {intl.formatMessage({ id: "dbInspect.node" })}
                </label>
                <select
                  className="rounded-md border border-input bg-background px-3 py-2 text-sm"
                  id="db-inspect-node"
                  onChange={(event) => {
                    setState((current) => ({ ...current, selectedNodeId: Number(event.target.value) }))
                  }}
                  value={selectedNodeId}
                >
                  {state.nodes.map((node) => (
                    <option key={node.node_id} value={node.node_id}>
                      {node.is_local
                        ? intl.formatMessage({ id: "common.localNodeValue" }, { label: `Node ${node.node_id}` })
                        : intl.formatMessage({ id: "common.nodeValue" }, { id: node.node_id })}
                    </option>
                  ))}
                </select>
              </div>
              <label className="block text-sm font-medium text-foreground" htmlFor="db-inspect-query">
                {intl.formatMessage({ id: "dbInspect.query.label" })}
              </label>
              <textarea
                className="min-h-28 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm"
                id="db-inspect-query"
                onChange={(event) => setState((current) => ({ ...current, query: event.target.value }))}
                value={state.query}
              />
              <div className="flex flex-wrap gap-2">
                {templates.map((template) => (
                  <Button
                    key={template}
                    onClick={() => setState((current) => ({ ...current, query: template }))}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {template}
                  </Button>
                ))}
              </div>
              <Button disabled={state.running} type="submit">
                <Play className="mr-2 size-4" />
                {state.running
                  ? intl.formatMessage({ id: "dbInspect.query.running" })
                  : intl.formatMessage({ id: "dbInspect.query.run" })}
              </Button>
            </form>
          </SectionCard>

          {state.error ? (
            <ResourceState
              kind={errorKind(state.error)}
              title={intl.formatMessage({ id: "dbInspect.title" })}
              description={state.error.message}
            />
          ) : null}

          {state.describe ? (
            <ResultSection
              columns={describeColumns}
              rows={state.describe.rows}
              title={intl.formatMessage({ id: "dbInspect.describe.title" }, { table: state.selectedTable })}
            />
          ) : null}

          {state.result ? (
            <SectionCard title={intl.formatMessage({ id: "dbInspect.results.title" })}>
              <StatsStrip result={state.result} />
              <ResultTable columns={columns} rows={state.result.rows} />
              {state.result.stats.has_more && state.result.stats.next_cursor ? (
                <Button
                  className="mt-4"
                  onClick={() => void runQuery(appendCursor(state.query, state.result?.stats.next_cursor ?? ""))}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "dbInspect.nextPage" })}
                </Button>
              ) : null}
            </SectionCard>
          ) : null}
        </div>
      </div>
    </PageContainer>
  )
}

function tableRows(rows: ManagerDBInspectRow[]): TableRow[] {
  return rows
    .map((row) => ({
      domain: String(row.domain ?? ""),
      name: String(row.name ?? ""),
      table: String(row.table ?? ""),
    }))
    .filter((row) => row.domain && row.name && row.table)
}

function groupTables(rows: TableRow[]) {
  return rows.reduce<Record<string, TableRow[]>>((acc, row) => {
    acc[row.domain] = acc[row.domain] ?? []
    acc[row.domain].push(row)
    return acc
  }, {})
}

function StatsStrip({ result }: { result: ManagerDBInspectQueryResponse }) {
  return (
    <div className="mb-4 flex flex-wrap gap-2 text-xs text-muted-foreground">
      <span className="rounded-full border border-border px-2 py-1">{result.stats.scan_mode || "-"}</span>
      <span className="rounded-full border border-border px-2 py-1">scanned {result.stats.scanned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">returned {result.stats.returned_rows}</span>
      <span className="rounded-full border border-border px-2 py-1">
        slots {result.stats.scanned_hash_slots.length ? result.stats.scanned_hash_slots.join(",") : "-"}
      </span>
    </div>
  )
}

function ResultSection({ title, rows, columns }: { title: string; rows: ManagerDBInspectRow[]; columns: string[] }) {
  return (
    <SectionCard title={title}>
      <ResultTable columns={columns} rows={rows} />
    </SectionCard>
  )
}

function ResultTable({ columns, rows }: { columns: string[]; rows: ManagerDBInspectRow[] }) {
  if (rows.length === 0) {
    return <ResourceState kind="empty" title="DB Inspect" />
  }
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full border-collapse">
        <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
          <tr>
            {columns.map((column) => (
              <th className="px-3 py-3" key={column}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr className="border-t border-border" key={index}>
              {columns.map((column) => (
                <td className="max-w-[320px] truncate px-3 py-3 font-mono text-xs text-foreground" key={column}>
                  {renderCell(row[column])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

- [ ] **Step 4: Add remaining i18n strings**

Add to `web/src/i18n/messages/en.ts`:

```ts
"dbInspect.tables.title": "Inspectable tables",
"dbInspect.table.inspect": "Inspect {table}",
"dbInspect.query.title": "Query",
"dbInspect.query.label": "Inspect query",
"dbInspect.query.run": "Run query",
"dbInspect.query.running": "Running...",
"dbInspect.query.empty": "Enter a read-only inspect query.",
"dbInspect.node": "Node",
"dbInspect.results.title": "Results",
"dbInspect.describe.title": "{table} columns",
"dbInspect.nextPage": "Next page",
"dbInspect.error.emptyQuery": "Enter a read-only inspect query.",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```ts
"dbInspect.tables.title": "可检查表",
"dbInspect.table.inspect": "查看 {table}",
"dbInspect.query.title": "查询",
"dbInspect.query.label": "检查查询",
"dbInspect.query.run": "执行查询",
"dbInspect.query.running": "执行中...",
"dbInspect.query.empty": "请输入只读检查查询。",
"dbInspect.node": "节点",
"dbInspect.results.title": "结果",
"dbInspect.describe.title": "{table} 列",
"dbInspect.nextPage": "下一页",
"dbInspect.error.emptyQuery": "请输入只读检查查询。",
```

- [ ] **Step 5: Run page tests**

Run:

```bash
cd web && bun run test -- src/pages/db-inspect/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Run Web typecheck/build tests for touched area**

Run:

```bash
cd web && bun run test -- src/pages/db-inspect/page.test.tsx src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 7**

Run:

```bash
git add web/src/pages/db-inspect/page.tsx web/src/pages/db-inspect/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): add db inspect page"
```

Expected: commit succeeds.

## Task 8: FLOW Docs And Web README

**Files:**
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `web/README.md`

- [ ] **Step 1: Update management flow**

In `internalv2/usecase/management/FLOW.md`, add DB inspect to the responsibility
paragraph and add:

```markdown
## DB Inspect Flow

```text
manager HTTP handler
  -> management.App.QueryDBInspect/ListDBInspectTables/DescribeDBInspectTable
  -> local DBInspectReader or RemoteDBInspectReader by node_id
  -> node-local pkg/db/inspect Store on the selected node
  -> bounded read-only rows and scan stats
```

DB inspect is node-local diagnostics. It does not merge rows across the
cluster, does not expose filesystem paths to callers, and does not mutate
message or metadata storage. Empty `node_id` means the local manager node.
```

- [ ] **Step 2: Update manager flow**

In `internalv2/access/manager/FLOW.md`, add routes:

```text
GET  /manager/db/inspect/tables (read-only inspect table list; requires cluster.db:r when Auth.On=true)
GET  /manager/db/inspect/tables/:domain/:table (read-only inspect table description; requires cluster.db:r when Auth.On=true)
POST /manager/db/inspect/query (read-only inspect SQL query; requires cluster.db:r when Auth.On=true)
```

Add a short paragraph:

```markdown
`/manager/db/inspect*` exposes bounded read-only node-local storage inspection
for the Web DB Inspect page. HTTP parsing, auth, permission checks, and manager
error envelopes stay in this package; query planning and storage reads are
delegated through `internalv2/usecase/management` and `pkg/db/inspect`.
```

- [ ] **Step 3: Update node flow**

In `internalv2/access/node/FLOW.md`, add:

```markdown
## Manager DB Inspect RPC

```text
remote manager DB inspect reader
  -> encode W K V I 1 request
  -> clusterv2 RPCManagerDBInspect
  -> Adapter.HandleManagerDBInspectRPC
  -> local Management DBInspectReader
  -> encode W K V i 1 response
```

Manager DB Inspect RPC transports one read-only inspect query to the selected
node. The receiving node always inspects its own local DB; it does not perform
a second routing hop or mutate storage.
```

- [ ] **Step 4: Update infra cluster flow**

In `internalv2/infra/cluster/FLOW.md`, add DB inspect to the responsibility
paragraph and add:

```markdown
## Management DB Inspect Flow

```text
management.RemoteDBInspectReader
  -> access/node Manager DB Inspect RPC client
  -> clusterv2 CallRPC(target node, RPCManagerDBInspect)
  -> target node access/node Manager DB Inspect RPC handler
  -> target node app DBInspectReader
```

`ManagementDBInspectReader` is the remote half for non-local `node_id`
filters. It forwards the exact read-only query to the selected node and leaves
HTTP parsing, query validation, table metadata shaping, cursor handling, and
storage path derivation outside the infra adapter.
```

- [ ] **Step 5: Update app flow**

In `internalv2/app/FLOW.md`, add DB inspect to manager wiring text:

```markdown
The manager also wires a node-local DB inspect reader derived from
`Config.DataDir`, using `slotmeta` for metadata and `messages` for channel
message storage. Remote `node_id` DB inspect requests route through the manager
DB inspect node RPC and remain node-local on the target node.
```

- [ ] **Step 6: Update Web README**

In `web/README.md`, add a row to the Page And API Matrix:

```markdown
| `/system/db` | `GET /manager/db/inspect/tables`, `GET /manager/db/inspect/tables/:domain/:table`, `POST /manager/db/inspect/query` | Implemented |
```

Add `/db-inspect` to legacy redirects:

```markdown
- System routes: `/settings/permissions`, `/settings/webhooks`, `/connections`, `/db-inspect`.
```

- [ ] **Step 7: Run docs grep checks**

Run:

```bash
rg -n "db/inspect|DB Inspect|RPCManagerDBInspect|cluster.db" internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md internalv2/access/node/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/app/FLOW.md web/README.md
```

Expected: each changed doc contains the new DB inspect terms.

- [ ] **Step 8: Commit Task 8**

Run:

```bash
git add internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md internalv2/access/node/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/app/FLOW.md web/README.md
git commit -m "docs: describe db inspect management flow"
```

Expected: commit succeeds.

## Task 9: Focused Verification

**Files:**
- No file edits.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app ./pkg/db/inspect
```

Expected: PASS.

- [ ] **Step 2: Run focused Web tests**

Run:

```bash
cd web && bun run test -- src/pages/db-inspect/page.test.tsx src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run Web build if frontend changes are complete**

Run:

```bash
cd web && bun run build
```

Expected: PASS.

- [ ] **Step 4: Inspect final git status**

Run:

```bash
git status --short
```

Expected: no uncommitted changes from this feature.

- [ ] **Step 5: Summarize verification evidence**

Record the exact commands and pass/fail output in the final implementation
handoff. If any broad verification such as `go test ./...` was skipped, state
why.
