# Manager Recent Conversations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manager-protected, read-only recent conversations API and `/business/conversations` web page.

**Architecture:** Keep HTTP and React entry points thin. Route `/manager/conversations` through `internal/access/manager` into `internal/usecase/management`, where it reuses the existing `internal/usecase/conversation.App.Sync` working-set logic. Wire dependencies only from `internal/app`; single-node deployments remain single-node clusters and use the same authoritative Slot/channel-log paths.

**Tech Stack:** Go 1.23, gin, existing manager auth/permission middleware, `internal/usecase/conversation`, React 19, React Router, react-intl, Vitest, Testing Library.

---

## Source References

- Spec: `docs/superpowers/specs/2026-05-15-recent-conversations-design.md`
- Manager HTTP routes: `internal/access/manager/routes.go`
- Manager message DTO helpers: `internal/access/manager/messages.go`
- Management app wiring: `internal/usecase/management/app.go`
- Conversation sync usecase: `internal/usecase/conversation/sync.go`, `internal/usecase/conversation/types.go`
- App composition root: `internal/app/build.go`
- Web manager API client: `web/src/lib/manager-api.ts`, `web/src/lib/manager-api.types.ts`
- Web navigation/router: `web/src/lib/navigation.ts`, `web/src/app/router.tsx`
- Existing pages to follow: `web/src/pages/messages/page.tsx`, `web/src/pages/users/page.tsx`

## Implementation Safety Rules

- Before editing any package, check for `FLOW.md`; update it only when behavior described there changes. Current exploration found no `FLOW.md` under `web/` or `internal/access/manager`; `internal/usecase/management` also has no package `FLOW.md`.
- Do not touch or revert unrelated dirty files. Current dirty files include web i18n, node/slot pages, `web/dist/index.html`, and an untracked web spec. When editing a dirty file such as `web/src/i18n/messages/en.ts`, inspect the current contents first and preserve existing changes.
- Keep manager HTTP handlers as adapters. Do not call Slot or channel-log dependencies directly from `internal/access/manager`.
- Add English comments for exported Go types, exported methods, and major struct fields.
- Keep this feature read-only: no clear-unread, set-unread, delete, or retention side effects.
- Use “single-node cluster” in comments/docs/tests when describing deployment shape; this feature should not need new deployment wording.

## File Structure

### New Files

- `internal/usecase/management/conversations.go` — manager-facing request/response DTOs and aggregation around `conversation.Sync`.
- `internal/usecase/management/conversations_test.go` — management unit tests with a fake conversation syncer.
- `internal/access/manager/conversations.go` — HTTP query parsing, DTO rendering, and error mapping for `GET /manager/conversations`.
- `internal/access/manager/conversations_test.go` — focused manager HTTP tests for the new route and auth guard.
- `web/src/pages/conversations/page.tsx` — standalone Business Management page for UID-scoped recent conversations.
- `web/src/pages/conversations/page.test.tsx` — page tests for query, auto-run, rendering, link target, and error states.

### Modified Files

- `internal/usecase/management/app.go` — add `RecentConversationSyncer` interface, `Options.Conversations`, and `App.conversations` field.
- `internal/access/manager/server.go` — add `ListRecentConversations` to the `Management` interface.
- `internal/access/manager/routes.go` — register `GET /manager/conversations` under `cluster.channel:r`.
- `internal/access/manager/server_test.go` — add recent conversations fields/method to `managementStub`.
- `internal/app/build.go` — pass `app.conversationApp` into `management.New`.
- `web/src/lib/manager-api.types.ts` — add recent-conversation request/response types.
- `web/src/lib/manager-api.ts` — add path builder and `getRecentConversations`.
- `web/src/lib/manager-api.test.ts` — add API client query-string test.
- `web/src/lib/navigation.ts` and `web/src/lib/navigation.test.ts` — add Business nav item and legacy redirect.
- `web/src/app/router.tsx` — add `/business/conversations` route and `/conversations` redirect.
- `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts` — add nav/page copy.
- `web/src/pages/page-shells.test.tsx` — mock new client method and assert English/Chinese shell labels.

## Task 1: Management Usecase Aggregation

**Files:**
- Modify: `internal/usecase/management/app.go`
- Create: `internal/usecase/management/conversations.go`
- Create: `internal/usecase/management/conversations_test.go`

- [ ] **Step 1: Write failing management tests**

Create `internal/usecase/management/conversations_test.go` with these tests:

```go
package management

import (
    "context"
    "errors"
    "testing"

    conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
    "github.com/WuKongIM/WuKongIM/pkg/channel"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/stretchr/testify/require"
)

func TestListRecentConversationsMapsSyncResultAndTruncates(t *testing.T) {
    syncer := &fakeRecentConversationSyncer{
        result: conversationusecase.SyncResult{Conversations: []conversationusecase.SyncConversation{
            {
                ChannelID: "g1", ChannelType: 2, Unread: 3, Timestamp: 100,
                LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 9, Version: 1000,
                Recents: []channel.Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
            },
            {ChannelID: "g2", ChannelType: 2, Unread: 0, Timestamp: 90, LastMsgSeq: 8, ReadToMsgSeq: 8, Version: 900},
        }},
    }
    app := New(Options{Conversations: syncer})

    got, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{
        UID: " u1 ", Limit: 1, MsgCount: 1, OnlyUnread: true,
    })

    require.NoError(t, err)
    require.Equal(t, conversationusecase.SyncQuery{UID: "u1", Limit: 2, MsgCount: 1, OnlyUnread: true}, syncer.query)
    require.True(t, got.Truncated)
    require.Equal(t, "u1", got.UID)
    require.Equal(t, 1, got.Limit)
    require.Equal(t, 1, got.MsgCount)
    require.True(t, got.OnlyUnread)
    require.Equal(t, []RecentConversation{{
        UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 3, Timestamp: 100,
        LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 9, Version: 1000,
        RecentMessages: []Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
    }}, got.Items)
}

func TestListRecentConversationsRejectsInvalidRequest(t *testing.T) {
    app := New(Options{Conversations: &fakeRecentConversationSyncer{}})
    for _, req := range []RecentConversationsRequest{
        {UID: "", Limit: 1, MsgCount: 0},
        {UID: "u1", Limit: 0, MsgCount: 0},
        {UID: "u1", Limit: 1, MsgCount: -1},
    } {
        _, err := app.ListRecentConversations(context.Background(), req)
        require.ErrorIs(t, err, metadb.ErrInvalidArgument)
    }
}

func TestListRecentConversationsReturnsUnavailableWhenSyncerMissing(t *testing.T) {
    app := New(Options{})

    _, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

    require.ErrorIs(t, err, ErrRecentConversationsUnavailable)
}

func TestListRecentConversationsPropagatesSyncError(t *testing.T) {
    want := errors.New("sync failed")
    app := New(Options{Conversations: &fakeRecentConversationSyncer{err: want}})

    _, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

    require.ErrorIs(t, err, want)
}

type fakeRecentConversationSyncer struct {
    query  conversationusecase.SyncQuery
    result conversationusecase.SyncResult
    err    error
}

func (f *fakeRecentConversationSyncer) Sync(_ context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error) {
    f.query = query
    return f.result, f.err
}
```

- [ ] **Step 2: Run management tests and verify failure**

Run:

```sh
go test ./internal/usecase/management -run 'TestListRecentConversations' -count=1
```

Expected: FAIL with undefined `RecentConversationsRequest`, `ListRecentConversations`, and `ErrRecentConversationsUnavailable`.

- [ ] **Step 3: Add management app fields**

Modify `internal/usecase/management/app.go` imports to include the conversation package:

```go
conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
```

Add this interface near the other dependency interfaces:

```go
// RecentConversationSyncer exposes UID-scoped conversation sync for manager read views.
type RecentConversationSyncer interface {
    // Sync returns the bounded recent conversation working set for one UID.
    Sync(ctx context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error)
}
```

Add to `Options`:

```go
// Conversations provides UID-scoped recent conversation working sets.
Conversations RecentConversationSyncer
```

Add to `App`:

```go
conversations RecentConversationSyncer
```

Add to `New` return literal:

```go
conversations: opts.Conversations,
```

- [ ] **Step 4: Implement management conversations file**

Create `internal/usecase/management/conversations.go`:

```go
package management

import (
    "context"
    "errors"
    "strings"

    conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
    "github.com/WuKongIM/WuKongIM/pkg/channel"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// ErrRecentConversationsUnavailable reports that manager conversation reads are not wired.
var ErrRecentConversationsUnavailable = errors.New("management: recent conversations unavailable")

// RecentConversationsRequest configures one manager recent-conversation query.
type RecentConversationsRequest struct {
    // UID identifies the user whose recent conversations should be listed.
    UID string
    // Limit caps the number of returned conversations.
    Limit int
    // MsgCount caps the number of recent messages embedded per conversation.
    MsgCount int
    // OnlyUnread filters the working set to conversations with unread messages.
    OnlyUnread bool
}

// RecentConversationsResponse contains one bounded manager recent-conversation result.
type RecentConversationsResponse struct {
    // UID is the normalized queried user id.
    UID string
    // Limit is the applied conversation limit.
    Limit int
    // MsgCount is the applied recent-message preview limit.
    MsgCount int
    // OnlyUnread reports whether unread filtering was applied.
    OnlyUnread bool
    // Truncated reports whether more matching conversations were detected.
    Truncated bool
    // Items contains conversations ordered by the conversation sync usecase.
    Items []RecentConversation
}

// RecentConversation is one manager-facing recent conversation row.
type RecentConversation struct {
    // UID is the owner user for this conversation row.
    UID string
    // ChannelID is the display channel id returned by conversation sync.
    ChannelID string
    // ChannelType is the WuKong channel type.
    ChannelType uint8
    // Unread counts unread messages for UID in this conversation.
    Unread int
    // Timestamp is the latest message timestamp in Unix seconds.
    Timestamp int64
    // LastMsgSeq is the latest message sequence known to conversation sync.
    LastMsgSeq uint32
    // LastClientMsgNo is the latest client message number when present.
    LastClientMsgNo string
    // ReadToMsgSeq is UID's read cursor for this conversation.
    ReadToMsgSeq uint32
    // Version is the sync compatibility version timestamp.
    Version int64
    // RecentMessages contains newest message previews for this conversation.
    RecentMessages []Message
}

// ListRecentConversations returns one bounded UID-scoped recent conversation working set.
func (a *App) ListRecentConversations(ctx context.Context, req RecentConversationsRequest) (RecentConversationsResponse, error) {
    uid := strings.TrimSpace(req.UID)
    if uid == "" || req.Limit <= 0 || req.MsgCount < 0 {
        return RecentConversationsResponse{}, metadb.ErrInvalidArgument
    }
    if a == nil || a.conversations == nil {
        return RecentConversationsResponse{}, ErrRecentConversationsUnavailable
    }

    syncLimit := req.Limit + 1
    result, err := a.conversations.Sync(ctx, conversationusecase.SyncQuery{
        UID:        uid,
        Limit:      syncLimit,
        MsgCount:   req.MsgCount,
        OnlyUnread: req.OnlyUnread,
    })
    if err != nil {
        return RecentConversationsResponse{}, err
    }

    conversations := result.Conversations
    truncated := len(conversations) > req.Limit
    if truncated {
        conversations = conversations[:req.Limit]
    }

    resp := RecentConversationsResponse{
        UID:        uid,
        Limit:      req.Limit,
        MsgCount:   req.MsgCount,
        OnlyUnread: req.OnlyUnread,
        Truncated:  truncated,
        Items:      make([]RecentConversation, 0, len(conversations)),
    }
    for _, item := range conversations {
        resp.Items = append(resp.Items, recentConversationFromSync(uid, item))
    }
    return resp, nil
}

func recentConversationFromSync(uid string, item conversationusecase.SyncConversation) RecentConversation {
    return RecentConversation{
        UID:             uid,
        ChannelID:       item.ChannelID,
        ChannelType:     item.ChannelType,
        Unread:          item.Unread,
        Timestamp:       item.Timestamp,
        LastMsgSeq:      item.LastMsgSeq,
        LastClientMsgNo: item.LastClientMsgNo,
        ReadToMsgSeq:    item.ReadToMsgSeq,
        Version:         item.Version,
        RecentMessages:  messagesFromChannelMessages(item.Recents),
    }
}

func messagesFromChannelMessages(items []channel.Message) []Message {
    out := make([]Message, 0, len(items))
    for _, item := range items {
        out = append(out, messageFromChannelMessage(item))
    }
    return out
}

func messageFromChannelMessage(item channel.Message) Message {
    return Message{
        MessageID:   item.MessageID,
        MessageSeq:  item.MessageSeq,
        ClientMsgNo: item.ClientMsgNo,
        ChannelID:   item.ChannelID,
        ChannelType: int64(item.ChannelType),
        FromUID:     item.FromUID,
        Timestamp:   int64(item.Timestamp),
        Payload:     append([]byte(nil), item.Payload...),
    }
}
```

- [ ] **Step 5: Reuse the channel-message mapper in `ListMessages`**

Modify the loop in `internal/usecase/management/messages.go` from the inline `Message{...}` literal to:

```go
for _, item := range page.Items {
    resp.Items = append(resp.Items, messageFromChannelMessage(item))
}
```

- [ ] **Step 6: Run management tests and verify pass**

Run:

```sh
go test ./internal/usecase/management -run 'TestListRecentConversations|TestListMessages' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit management usecase**

Run:

```sh
git add internal/usecase/management/app.go internal/usecase/management/conversations.go internal/usecase/management/conversations_test.go internal/usecase/management/messages.go
git commit -m "feat: add manager recent conversation usecase"
```

## Task 2: Manager HTTP API

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server_test.go`
- Create: `internal/access/manager/conversations.go`
- Create: `internal/access/manager/conversations_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Create `internal/access/manager/conversations_test.go`:

```go
package manager

import (
    "net/http"
    "net/http/httptest"
    "testing"

    managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/stretchr/testify/require"
)

func TestManagerConversationsReturnsRecentList(t *testing.T) {
    var received managementusecase.RecentConversationsRequest
    srv := New(Options{Management: managementStub{
        recentConversationsReqSink: &received,
        recentConversations: managementusecase.RecentConversationsResponse{
            UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true, Truncated: true,
            Items: []managementusecase.RecentConversation{{
                UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 4, Timestamp: 100,
                LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 8, Version: 1000,
                RecentMessages: []managementusecase.Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
            }},
        },
    }})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=%20u1%20&limit=50&msg_count=1&only_unread=true", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, managementusecase.RecentConversationsRequest{UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true}, received)
    require.JSONEq(t, `{
        "uid":"u1","limit":50,"msg_count":1,"only_unread":true,"truncated":true,
        "items":[{"uid":"u1","channel_id":"g1","channel_type":2,"unread":4,"timestamp":100,"last_msg_seq":12,"last_client_msg_no":"c12","read_to_msg_seq":8,"version":1000,"recent_messages":[{"message_id":99,"message_seq":12,"client_msg_no":"c12","channel_id":"g1","channel_type":2,"from_uid":"u2","timestamp":100,"payload":"aGVsbG8="}]}]
    }`, rec.Body.String())
}

func TestManagerConversationsRejectsInvalidQuery(t *testing.T) {
    srv := New(Options{Management: managementStub{}})
    for _, path := range []string{
        "/manager/conversations",
        "/manager/conversations?uid=u1&limit=0",
        "/manager/conversations?uid=u1&limit=201",
        "/manager/conversations?uid=u1&msg_count=-1",
        "/manager/conversations?uid=u1&msg_count=11",
        "/manager/conversations?uid=u1&only_unread=maybe",
    } {
        rec := httptest.NewRecorder()
        req := httptest.NewRequest(http.MethodGet, path, nil)
        srv.Engine().ServeHTTP(rec, req)
        require.Equal(t, http.StatusBadRequest, rec.Code, path)
    }
}

func TestManagerConversationsMapsUsecaseErrors(t *testing.T) {
    tests := []struct {
        name string
        err  error
        code int
        body string
    }{
        {name: "invalid", err: metadb.ErrInvalidArgument, code: http.StatusBadRequest, body: `{"error":"bad_request","message":"invalid conversation query"}`},
        {name: "unavailable", err: managementusecase.ErrRecentConversationsUnavailable, code: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"recent conversations unavailable"}`},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            srv := New(Options{Management: managementStub{recentConversationsErr: tt.err}})
            rec := httptest.NewRecorder()
            req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
            srv.Engine().ServeHTTP(rec, req)
            require.Equal(t, tt.code, rec.Code)
            require.JSONEq(t, tt.body, rec.Body.String())
        })
    }
}

func TestManagerConversationsRequiresChannelReadPermission(t *testing.T) {
    srv := New(Options{
        Auth: AuthConfig{
            On: true, JWTSecret: "secret", JWTIssuer: "test",
            Users: []UserConfig{
                {Username: "viewer", Password: "pw", Permissions: []PermissionConfig{{Resource: "cluster.user", Actions: []string{"r"}}}},
                {Username: "channel", Password: "pw", Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}}},
            },
        },
        Management: managementStub{recentConversations: managementusecase.RecentConversationsResponse{UID: "u1", Limit: 50, MsgCount: 1}},
    })

    denied := httptest.NewRecorder()
    deniedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
    deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
    srv.Engine().ServeHTTP(denied, deniedReq)
    require.Equal(t, http.StatusForbidden, denied.Code)

    allowed := httptest.NewRecorder()
    allowedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
    allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "channel"))
    srv.Engine().ServeHTTP(allowed, allowedReq)
    require.Equal(t, http.StatusOK, allowed.Code)
}
```

- [ ] **Step 2: Run HTTP tests and verify failure**

Run:

```sh
go test ./internal/access/manager -run 'TestManagerConversations' -count=1
```

Expected: FAIL because the route, DTOs, and management stub method do not exist.

- [ ] **Step 3: Extend manager `Management` interface**

Add to `internal/access/manager/server.go` inside the `Management` interface near message methods:

```go
// ListRecentConversations returns one manager-facing UID recent conversation working set.
ListRecentConversations(ctx context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error)
```

- [ ] **Step 4: Register the route**

Modify `internal/access/manager/routes.go`. In the group that protects channel reads with `cluster.channel:r`, add this route before `/messages`:

```go
channelRuntimeMeta.GET("/conversations", s.handleConversations)
```

- [ ] **Step 5: Implement HTTP handler**

Create `internal/access/manager/conversations.go`:

```go
package manager

import (
    "errors"
    "net/http"
    "strconv"
    "strings"

    managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
    metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
    "github.com/gin-gonic/gin"
)

const (
    defaultRecentConversationsLimit    = 50
    maxRecentConversationsLimit        = 200
    defaultRecentConversationMsgCount  = 1
    maxRecentConversationMsgCount      = 10
)

// RecentConversationsResponseDTO is the manager recent-conversation response body.
type RecentConversationsResponseDTO struct {
    // UID is the normalized queried user id.
    UID string `json:"uid"`
    // Limit is the applied conversation limit.
    Limit int `json:"limit"`
    // MsgCount is the applied recent-message preview limit.
    MsgCount int `json:"msg_count"`
    // OnlyUnread reports whether unread filtering was applied.
    OnlyUnread bool `json:"only_unread"`
    // Truncated reports whether more matching conversations were detected.
    Truncated bool `json:"truncated"`
    // Items contains recent conversation rows.
    Items []RecentConversationDTO `json:"items"`
}

// RecentConversationDTO is one manager recent-conversation row.
type RecentConversationDTO struct {
    // UID is the owner user for this conversation row.
    UID string `json:"uid"`
    // ChannelID is the display channel id returned by conversation sync.
    ChannelID string `json:"channel_id"`
    // ChannelType is the WuKong channel type.
    ChannelType int64 `json:"channel_type"`
    // Unread counts unread messages for UID in this conversation.
    Unread int `json:"unread"`
    // Timestamp is the latest message timestamp in Unix seconds.
    Timestamp int64 `json:"timestamp"`
    // LastMsgSeq is the latest message sequence known to conversation sync.
    LastMsgSeq uint32 `json:"last_msg_seq"`
    // LastClientMsgNo is the latest client message number when present.
    LastClientMsgNo string `json:"last_client_msg_no"`
    // ReadToMsgSeq is UID's read cursor for this conversation.
    ReadToMsgSeq uint32 `json:"read_to_msg_seq"`
    // Version is the sync compatibility version timestamp.
    Version int64 `json:"version"`
    // RecentMessages contains newest message previews for this conversation.
    RecentMessages []MessageDTO `json:"recent_messages"`
}

func (s *Server) handleConversations(c *gin.Context) {
    if s.management == nil {
        jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
        return
    }

    uid := strings.TrimSpace(c.Query("uid"))
    if uid == "" {
        jsonError(c, http.StatusBadRequest, "bad_request", "uid is required")
        return
    }
    limit, err := parseRecentConversationsLimit(c.Query("limit"))
    if err != nil {
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
        return
    }
    msgCount, err := parseRecentConversationMsgCount(c.Query("msg_count"))
    if err != nil {
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid msg_count")
        return
    }
    onlyUnread, err := parseOptionalBool(c.Query("only_unread"))
    if err != nil {
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid only_unread")
        return
    }

    result, err := s.management.ListRecentConversations(c.Request.Context(), managementusecase.RecentConversationsRequest{
        UID: uid, Limit: limit, MsgCount: msgCount, OnlyUnread: onlyUnread,
    })
    if err != nil {
        switch {
        case errors.Is(err, metadb.ErrInvalidArgument):
            jsonError(c, http.StatusBadRequest, "bad_request", "invalid conversation query")
        case errors.Is(err, managementusecase.ErrRecentConversationsUnavailable):
            jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "recent conversations unavailable")
        case channelLeaderUnavailable(err):
            jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel leader unavailable")
        default:
            jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
        }
        return
    }

    c.JSON(http.StatusOK, recentConversationsDTO(result))
}

func parseRecentConversationsLimit(raw string) (int, error) {
    if raw == "" {
        return defaultRecentConversationsLimit, nil
    }
    value, err := strconv.Atoi(raw)
    if err != nil || value <= 0 || value > maxRecentConversationsLimit {
        return 0, strconv.ErrSyntax
    }
    return value, nil
}

func parseRecentConversationMsgCount(raw string) (int, error) {
    if raw == "" {
        return defaultRecentConversationMsgCount, nil
    }
    value, err := strconv.Atoi(raw)
    if err != nil || value < 0 || value > maxRecentConversationMsgCount {
        return 0, strconv.ErrSyntax
    }
    return value, nil
}

func parseOptionalBool(raw string) (bool, error) {
    if raw == "" {
        return false, nil
    }
    return strconv.ParseBool(raw)
}

func recentConversationsDTO(resp managementusecase.RecentConversationsResponse) RecentConversationsResponseDTO {
    return RecentConversationsResponseDTO{
        UID: resp.UID, Limit: resp.Limit, MsgCount: resp.MsgCount, OnlyUnread: resp.OnlyUnread, Truncated: resp.Truncated,
        Items: recentConversationDTOs(resp.Items),
    }
}

func recentConversationDTOs(items []managementusecase.RecentConversation) []RecentConversationDTO {
    out := make([]RecentConversationDTO, 0, len(items))
    for _, item := range items {
        out = append(out, RecentConversationDTO{
            UID: item.UID, ChannelID: item.ChannelID, ChannelType: int64(item.ChannelType), Unread: item.Unread,
            Timestamp: item.Timestamp, LastMsgSeq: item.LastMsgSeq, LastClientMsgNo: item.LastClientMsgNo,
            ReadToMsgSeq: item.ReadToMsgSeq, Version: item.Version, RecentMessages: messageDTOs(item.RecentMessages),
        })
    }
    return out
}
```

- [ ] **Step 6: Extend `managementStub`**

Modify `internal/access/manager/server_test.go` `managementStub` struct:

```go
recentConversationsReqSink *managementusecase.RecentConversationsRequest
recentConversations        managementusecase.RecentConversationsResponse
recentConversationsErr     error
```

Add method:

```go
func (s managementStub) ListRecentConversations(_ context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error) {
    if s.recentConversationsReqSink != nil {
        *s.recentConversationsReqSink = req
    }
    return s.recentConversations, s.recentConversationsErr
}
```

- [ ] **Step 7: Run HTTP tests and verify pass**

Run:

```sh
go test ./internal/access/manager -run 'TestManagerConversations' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit manager HTTP API**

Run:

```sh
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/server_test.go internal/access/manager/conversations.go internal/access/manager/conversations_test.go
git commit -m "feat: expose manager recent conversations API"
```

## Task 3: App Wiring

**Files:**
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write wiring compile check command**

Run before editing:

```sh
go test ./internal/app -run TestBuild -count=1
```

Expected: PASS package compilation. This baseline proves the optional field did not break app construction before explicit wiring.

- [ ] **Step 2: Wire conversation app into management app**

Modify the `management.New(managementusecase.Options{...})` call in `internal/app/build.go` to include:

```go
Conversations: app.conversationApp,
```

Place it near the existing business/message dependencies:

```go
SystemUsers:             clusterUsers,
UserPresence:            authorityClient,
UserActions:             authorityClient,
Conversations:           app.conversationApp,
ChannelReplicaStatus: managerChannelReplicaStatusReader{
```

- [ ] **Step 3: Run app compile/tests**

Run:

```sh
go test ./internal/app -run 'TestBuild|TestApp' -count=1
```

Expected: PASS or no matching tests with successful package compilation.

- [ ] **Step 4: Commit app wiring**

Run:

```sh
git add internal/app/build.go
git commit -m "chore: wire recent conversations into manager"
```

## Task 4: Web API Client, Navigation, Router, And I18n

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/lib/navigation.test.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Inspect dirty web files before editing**

Run:

```sh
git diff -- web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/pages/page-shells.test.tsx web/src/lib/navigation.ts web/src/app/router.tsx
```

Expected: Review output and preserve unrelated existing changes. Do not revert node/slot page edits or `web/dist/index.html`.

- [ ] **Step 2: Write failing API client test**

Modify `web/src/lib/manager-api.test.ts` imports to include `getRecentConversations`. Add this test near other manager API path tests:

```ts
it("fetches recent conversations with query params", async () => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify({ uid: "u1", items: [] }), { status: 200 }))

  await getRecentConversations({ uid: "u1", limit: 25, msgCount: 2, onlyUnread: true })

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/conversations?uid=u1&limit=25&msg_count=2&only_unread=true",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 3: Run API client test and verify failure**

Run:

```sh
cd web && yarn test src/lib/manager-api.test.ts
```

Expected: FAIL because `getRecentConversations` is not exported.

- [ ] **Step 4: Add TypeScript types**

Modify `web/src/lib/manager-api.types.ts` after message types or near business channel types:

```ts
export type RecentConversationsParams = {
  uid: string
  limit?: number
  msgCount?: number
  onlyUnread?: boolean
}

export type ManagerRecentConversation = {
  uid: string
  channel_id: string
  channel_type: number
  unread: number
  timestamp: number
  last_msg_seq: number
  last_client_msg_no: string
  read_to_msg_seq: number
  version: number
  recent_messages: ManagerMessage[]
}

export type ManagerRecentConversationsResponse = {
  uid: string
  limit: number
  msg_count: number
  only_unread: boolean
  truncated: boolean
  items: ManagerRecentConversation[]
}
```

- [ ] **Step 5: Add API client function**

Modify `web/src/lib/manager-api.ts` imports to include:

```ts
RecentConversationsParams,
ManagerRecentConversationsResponse,
```

Add path builder near `buildMessageListPath`:

```ts
function buildRecentConversationsPath(params: RecentConversationsParams) {
  const search = new URLSearchParams()
  search.set("uid", params.uid)
  if (typeof params.limit === "number") {
    search.set("limit", String(params.limit))
  }
  if (typeof params.msgCount === "number") {
    search.set("msg_count", String(params.msgCount))
  }
  if (typeof params.onlyUnread === "boolean") {
    search.set("only_unread", String(params.onlyUnread))
  }
  return `/manager/conversations?${search.toString()}`
}
```

Add export near `getMessages`:

```ts
export function getRecentConversations(params: RecentConversationsParams) {
  return jsonManagerFetch<ManagerRecentConversationsResponse>(buildRecentConversationsPath(params))
}
```

- [ ] **Step 6: Write failing navigation/router shell tests**

Modify `web/src/lib/navigation.test.ts` active-section test:

```ts
expect(getActiveNavigationSection("/business/conversations")?.id).toBe("business")
```

Modify legacy redirect test:

```ts
expect(legacyRouteRedirects["/conversations"]).toBe("/business/conversations")
```

Modify `web/src/pages/page-shells.test.tsx`:

```ts
const getRecentConversationsMock = vi.fn()
```

Include it in the manager API mock:

```ts
getRecentConversations: (...args: unknown[]) => getRecentConversationsMock(...args),
```

Reset and default it in `beforeEach`:

```ts
getRecentConversationsMock.mockReset()
getRecentConversationsMock.mockResolvedValue({ uid: "", limit: 50, msg_count: 1, only_unread: false, truncated: false, items: [] })
```

Add to English shell cases:

```ts
["/business/conversations", "Recent Conversations", "Enter a UID to inspect recent conversations."],
```

Add to path label cases:

```ts
["/business/conversations", "BUSINESS / CONVERSATIONS"],
```

Add to Chinese shell cases:

```ts
["/business/conversations", "最近会话", "输入 UID 查看最近会话。"],
```

- [ ] **Step 7: Run navigation tests and verify failure**

Run:

```sh
cd web && yarn test src/lib/navigation.test.ts src/pages/page-shells.test.tsx
```

Expected: FAIL because route, nav item, i18n strings, and page component do not exist.

- [ ] **Step 8: Add navigation item and redirect**

Modify `web/src/lib/navigation.ts` business section. Insert after `/business/channels` or before `/business/messages`:

```ts
{
  href: "/business/conversations",
  titleMessageId: "nav.conversations.title",
  descriptionMessageId: "nav.conversations.description",
  pathLabelMessageId: "nav.path.business.conversations",
  icon: MessageSquare,
  aliases: ["/conversations"],
},
```

Add legacy redirect:

```ts
"/conversations": "/business/conversations",
```

- [ ] **Step 9: Add router route using a temporary import target**

Modify `web/src/app/router.tsx` imports:

```ts
import { ConversationsPage } from "@/pages/conversations/page"
```

Add under Business management:

```tsx
{ path: "business/conversations", element: <ConversationsPage /> },
```

Add legacy redirect:

```tsx
{ path: "conversations", element: <Navigate replace to="/business/conversations" /> },
```

- [ ] **Step 10: Add i18n strings**

Modify `web/src/i18n/messages/en.ts`:

```ts
"nav.conversations.title": "Recent Conversations",
"nav.conversations.description": "Inspect a user's recent conversation working set.",
"nav.path.business.conversations": "BUSINESS / CONVERSATIONS",
"conversations.title": "Recent Conversations",
"conversations.description": "Enter a UID to inspect recent conversations.",
"conversations.form.uid": "UID",
"conversations.form.uidPlaceholder": "Search UID",
"conversations.form.limit": "Limit",
"conversations.form.msgCount": "Message previews",
"conversations.form.onlyUnread": "Only unread",
"conversations.summary.scopePending": "Enter a UID to query recent conversations.",
"conversations.summary.scopeValue": "UID {uid}",
"conversations.summary.loadedValue": "Loaded {count} conversations",
"conversations.summary.truncated": "More conversations matched; increase the limit to inspect a larger working set.",
"conversations.validation.uid": "UID is required.",
"conversations.validation.limit": "Limit must be between 1 and 200.",
"conversations.validation.msgCount": "Message previews must be between 0 and 10.",
"conversations.table.channel": "Channel",
"conversations.table.type": "Type",
"conversations.table.unread": "Unread",
"conversations.table.lastSeq": "Last Seq",
"conversations.table.clientMsgNo": "Client Msg No",
"conversations.table.timestamp": "Timestamp",
"conversations.table.preview": "Last Message",
"conversations.table.actions": "Actions",
"conversations.viewMessages": "View messages",
"conversations.viewMessagesFor": "View messages for {channel}",
"conversations.empty.title": "Recent Conversations",
```

Modify `web/src/i18n/messages/zh-CN.ts`:

```ts
"nav.conversations.title": "最近会话",
"nav.conversations.description": "查看用户最近会话工作集。",
"nav.path.business.conversations": "BUSINESS / CONVERSATIONS",
"conversations.title": "最近会话",
"conversations.description": "输入 UID 查看最近会话。",
"conversations.form.uid": "UID",
"conversations.form.uidPlaceholder": "搜索 UID",
"conversations.form.limit": "数量",
"conversations.form.msgCount": "消息预览数",
"conversations.form.onlyUnread": "只看未读",
"conversations.summary.scopePending": "输入 UID 查询最近会话。",
"conversations.summary.scopeValue": "UID {uid}",
"conversations.summary.loadedValue": "已加载 {count} 个会话",
"conversations.summary.truncated": "还有更多匹配会话；可调大数量查看更多工作集。",
"conversations.validation.uid": "UID 不能为空。",
"conversations.validation.limit": "数量必须在 1 到 200 之间。",
"conversations.validation.msgCount": "消息预览数必须在 0 到 10 之间。",
"conversations.table.channel": "频道",
"conversations.table.type": "类型",
"conversations.table.unread": "未读",
"conversations.table.lastSeq": "最后 Seq",
"conversations.table.clientMsgNo": "客户端消息号",
"conversations.table.timestamp": "时间",
"conversations.table.preview": "最后消息",
"conversations.table.actions": "操作",
"conversations.viewMessages": "查看消息",
"conversations.viewMessagesFor": "查看 {channel} 的消息",
"conversations.empty.title": "最近会话",
```

- [ ] **Step 11: Create temporary page stub for route compilation**

Create `web/src/pages/conversations/page.tsx` with a minimal component that Task 5 will replace:

```tsx
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

export function ConversationsPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "conversations.title" })}
        description={intl.formatMessage({ id: "conversations.description" })}
      />
      <p className="text-sm text-muted-foreground">
        {intl.formatMessage({ id: "conversations.summary.scopePending" })}
      </p>
    </PageContainer>
  )
}
```

- [ ] **Step 12: Run web API/navigation tests and verify pass**

Run:

```sh
cd web && yarn test src/lib/manager-api.test.ts src/lib/navigation.test.ts src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 13: Commit web shell and client**

Run:

```sh
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/app/router.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/pages/page-shells.test.tsx web/src/pages/conversations/page.tsx
git commit -m "feat: add recent conversations web route"
```

## Task 5: Web Recent Conversations Page

**Files:**
- Modify: `web/src/pages/conversations/page.tsx`
- Create: `web/src/pages/conversations/page.test.tsx`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/conversations/page.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { ConversationsPage } from "@/pages/conversations/page"

const getRecentConversationsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getRecentConversations: (...args: unknown[]) => getRecentConversationsMock(...args),
  }
})

const conversationPage = {
  uid: "u1",
  limit: 50,
  msg_count: 1,
  only_unread: false,
  truncated: true,
  items: [{
    uid: "u1",
    channel_id: "g1",
    channel_type: 2,
    unread: 4,
    timestamp: 1778852000,
    last_msg_seq: 12,
    last_client_msg_no: "c12",
    read_to_msg_seq: 8,
    version: 1000,
    recent_messages: [{
      message_id: 99,
      message_seq: 12,
      client_msg_no: "c12",
      channel_id: "g1",
      channel_type: 2,
      from_uid: "u2",
      timestamp: 1778852000,
      payload: btoa("hello manager"),
    }],
  }],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getRecentConversationsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [{ resource: "cluster.channel", actions: ["r"] }],
  })
})

function renderConversationsPage(initialEntry = "/business/conversations") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <I18nProvider>
        <ConversationsPage />
      </I18nProvider>
    </MemoryRouter>,
  )
}

test("renders initial empty prompt", () => {
  renderConversationsPage()

  expect(screen.getByRole("heading", { name: "Recent Conversations" })).toBeInTheDocument()
  expect(screen.getByText("Enter a UID to query recent conversations.")).toBeInTheDocument()
  expect(getRecentConversationsMock).not.toHaveBeenCalled()
})

test("queries conversations by UID and renders previews", async () => {
  getRecentConversationsMock.mockResolvedValueOnce(conversationPage)

  const user = userEvent.setup()
  renderConversationsPage()

  await user.type(screen.getByPlaceholderText("Search UID"), "u1")
  await user.click(screen.getByRole("button", { name: "Search" }))

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(screen.getByText("hello manager")).toBeInTheDocument()
  expect(screen.getByText("More conversations matched; increase the limit to inspect a larger working set.")).toBeInTheDocument()
  expect(getRecentConversationsMock).toHaveBeenCalledWith({ uid: "u1", limit: 50, msgCount: 1, onlyUnread: false })
  expect(screen.getByRole("link", { name: "View messages for g1" })).toHaveAttribute("href", "/business/messages?channel_id=g1&channel_type=2")
})

test("auto queries uid from URL and passes only unread", async () => {
  getRecentConversationsMock.mockResolvedValueOnce({ ...conversationPage, only_unread: true, truncated: false })

  renderConversationsPage("/business/conversations?uid=u1&only_unread=true")

  expect(await screen.findByText("g1")).toBeInTheDocument()
  expect(getRecentConversationsMock).toHaveBeenCalledWith({ uid: "u1", limit: 50, msgCount: 1, onlyUnread: true })
})

test("validates limit and message count", async () => {
  const user = userEvent.setup()
  renderConversationsPage()

  await user.type(screen.getByPlaceholderText("Search UID"), "u1")
  await user.clear(screen.getByLabelText("Limit"))
  await user.type(screen.getByLabelText("Limit"), "0")
  await user.click(screen.getByRole("button", { name: "Search" }))
  expect(screen.getByText("Limit must be between 1 and 200.")).toBeInTheDocument()

  await user.clear(screen.getByLabelText("Limit"))
  await user.type(screen.getByLabelText("Limit"), "50")
  await user.clear(screen.getByLabelText("Message previews"))
  await user.type(screen.getByLabelText("Message previews"), "11")
  await user.click(screen.getByRole("button", { name: "Search" }))
  expect(screen.getByText("Message previews must be between 0 and 10.")).toBeInTheDocument()
})

test("maps permission and availability errors", async () => {
  getRecentConversationsMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderConversationsPage("/business/conversations?uid=u1")

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getRecentConversationsMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderConversationsPage("/business/conversations?uid=u1")

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run page tests and verify failure**

Run:

```sh
cd web && yarn test src/pages/conversations/page.test.tsx
```

Expected: FAIL because the page stub does not query or render the table.

- [ ] **Step 3: Replace page stub with real implementation**

Replace `web/src/pages/conversations/page.tsx` with:

```tsx
import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl } from "react-intl"
import { Link, useSearchParams } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { ManagerApiError, getRecentConversations } from "@/lib/manager-api"
import type { ManagerRecentConversationsResponse } from "@/lib/manager-api.types"

type ConversationQuery = {
  uid: string
  limit: string
  msgCount: string
  onlyUnread: boolean
}

type SubmittedConversationQuery = {
  uid: string
  limit: number
  msgCount: number
  onlyUnread: boolean
}

type ConversationsState = {
  data: ManagerRecentConversationsResponse | null
  loading: boolean
  refreshing: boolean
  queried: boolean
  error: Error | null
}

const defaultQuery: ConversationQuery = {
  uid: "",
  limit: "50",
  msgCount: "1",
  onlyUnread: false,
}

function mapErrorKind(error: Error | null) {
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

function decodePayload(value: string) {
  if (!value) {
    return ""
  }
  try {
    const binary = atob(value)
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
    const decoded = new TextDecoder().decode(bytes)
    const printable = /^[\x09\x0A\x0D\x20-\x7E]*$/.test(decoded)
    return printable ? decoded : value
  } catch {
    return value
  }
}

function formatTimestamp(timestamp: number) {
  if (!timestamp) {
    return "-"
  }
  return new Date(timestamp * 1000).toLocaleString()
}

function firstPreview(page: ManagerRecentConversationsResponse["items"][number]) {
  const message = page.recent_messages[0]
  return message ? decodePayload(message.payload) || "-" : "-"
}

function messagesHref(channelId: string, channelType: number) {
  return `/business/messages?channel_id=${encodeURIComponent(channelId)}&channel_type=${channelType}`
}

export function ConversationsPage() {
  const intl = useIntl()
  const [searchParams] = useSearchParams()
  const initialUID = searchParams.get("uid")?.trim() ?? ""
  const initialOnlyUnread = searchParams.get("only_unread") === "true" || searchParams.get("only_unread") === "1"
  const [query, setQuery] = useState<ConversationQuery>(() => ({ ...defaultQuery, uid: initialUID, onlyUnread: initialOnlyUnread }))
  const [submitted, setSubmitted] = useState<SubmittedConversationQuery | null>(null)
  const [validationError, setValidationError] = useState<string | null>(null)
  const autoQueryStartedRef = useRef(false)
  const [state, setState] = useState<ConversationsState>({
    data: null,
    loading: false,
    refreshing: false,
    queried: false,
    error: null,
  })

  const runQuery = useCallback(async (nextQuery: SubmittedConversationQuery, refreshing = false) => {
    setState((current) => ({ ...current, loading: !refreshing, refreshing, queried: true, error: null }))
    try {
      const data = await getRecentConversations({
        uid: nextQuery.uid,
        limit: nextQuery.limit,
        msgCount: nextQuery.msgCount,
        onlyUnread: nextQuery.onlyUnread,
      })
      setState({ data, loading: false, refreshing: false, queried: true, error: null })
    } catch (error) {
      setState({
        data: null,
        loading: false,
        refreshing: false,
        queried: true,
        error: error instanceof Error ? error : new Error("recent conversations request failed"),
      })
    }
  }, [])

  const parseQuery = useCallback((): SubmittedConversationQuery | null => {
    const uid = query.uid.trim()
    if (!uid) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.uid" }))
      return null
    }
    const limit = Number(query.limit)
    if (!Number.isInteger(limit) || limit <= 0 || limit > 200) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.limit" }))
      return null
    }
    const msgCount = Number(query.msgCount)
    if (!Number.isInteger(msgCount) || msgCount < 0 || msgCount > 10) {
      setValidationError(intl.formatMessage({ id: "conversations.validation.msgCount" }))
      return null
    }
    setValidationError(null)
    return { uid, limit, msgCount, onlyUnread: query.onlyUnread }
  }, [intl, query])

  useEffect(() => {
    if (autoQueryStartedRef.current) {
      return
    }
    autoQueryStartedRef.current = true
    if (!initialUID) {
      return
    }
    const nextQuery = { uid: initialUID, limit: 50, msgCount: 1, onlyUnread: initialOnlyUnread }
    setSubmitted(nextQuery)
    void runQuery(nextQuery)
  }, [initialOnlyUnread, initialUID, runQuery])

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const nextQuery = parseQuery()
    if (!nextQuery) {
      return
    }
    setSubmitted(nextQuery)
    void runQuery(nextQuery)
  }

  const refresh = () => {
    if (!submitted) {
      return
    }
    void runQuery(submitted, true)
  }

  const summary = useMemo(() => {
    if (!submitted) {
      return intl.formatMessage({ id: "conversations.summary.scopePending" })
    }
    return intl.formatMessage({ id: "conversations.summary.scopeValue" }, { uid: submitted.uid })
  }, [intl, submitted])

  return (
    <PageContainer>
      <PageHeader
        actions={submitted ? (
          <Button onClick={refresh} size="sm" variant="outline">
            {state.refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        ) : null}
        description={intl.formatMessage({ id: "conversations.description" })}
        title={intl.formatMessage({ id: "conversations.title" })}
      />

      <SectionCard description={summary} title={intl.formatMessage({ id: "conversations.title" })}>
        <form className="mb-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_8rem_10rem_10rem_auto] md:items-end" onSubmit={submitSearch}>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.uid" })}</span>
            <input
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, uid: event.target.value }))}
              placeholder={intl.formatMessage({ id: "conversations.form.uidPlaceholder" })}
              value={query.uid}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.limit" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "conversations.form.limit" })}
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, limit: event.target.value }))}
              type="number"
              value={query.limit}
            />
          </label>
          <label className="grid gap-2 text-sm text-foreground">
            <span>{intl.formatMessage({ id: "conversations.form.msgCount" })}</span>
            <input
              aria-label={intl.formatMessage({ id: "conversations.form.msgCount" })}
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setQuery((current) => ({ ...current, msgCount: event.target.value }))}
              type="number"
              value={query.msgCount}
            />
          </label>
          <label className="flex h-9 items-center gap-2 text-sm text-foreground md:mb-0">
            <input
              checked={query.onlyUnread}
              onChange={(event) => setQuery((current) => ({ ...current, onlyUnread: event.target.checked }))}
              type="checkbox"
            />
            {intl.formatMessage({ id: "conversations.form.onlyUnread" })}
          </label>
          <Button size="sm" type="submit">{intl.formatMessage({ id: "common.search" })}</Button>
        </form>

        {validationError ? <p className="mb-3 text-sm text-destructive">{validationError}</p> : null}
        {state.data ? (
          <div className="mb-3 flex flex-wrap gap-3 text-sm text-muted-foreground">
            <span>{intl.formatMessage({ id: "conversations.summary.loadedValue" }, { count: state.data.items.length })}</span>
            {state.data.truncated ? <span>{intl.formatMessage({ id: "conversations.summary.truncated" })}</span> : null}
          </div>
        ) : null}

        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "conversations.empty.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState kind={mapErrorKind(state.error)} onRetry={submitted ? refresh : undefined} title={intl.formatMessage({ id: "conversations.empty.title" })} />
        ) : null}
        {!state.loading && !state.error && !state.queried ? (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "conversations.empty.title" })} />
        ) : null}
        {!state.loading && !state.error && state.data ? (
          state.data.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.channel" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.type" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.unread" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.lastSeq" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.clientMsgNo" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.timestamp" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.preview" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "conversations.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.data.items.map((item) => (
                    <tr className="border-t border-border" key={`${item.channel_type}-${item.channel_id}`}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{item.channel_id}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.channel_type}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.unread}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.last_msg_seq}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{item.last_client_msg_no || "-"}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatTimestamp(item.timestamp)}</td>
                      <td className="max-w-[24rem] px-3 py-3 text-sm text-foreground">{firstPreview(item)}</td>
                      <td className="px-3 py-3 text-sm">
                        <Button asChild size="sm" variant="outline">
                          <Link aria-label={intl.formatMessage({ id: "conversations.viewMessagesFor" }, { channel: item.channel_id })} to={messagesHref(item.channel_id, item.channel_type)}>
                            {intl.formatMessage({ id: "conversations.viewMessages" })}
                          </Link>
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "conversations.empty.title" })} />
          )
        ) : null}
      </SectionCard>
    </PageContainer>
  )
}
```

- [ ] **Step 4: Run page tests and fix compile-only API mismatch if needed**

Run:

```sh
cd web && yarn test src/pages/conversations/page.test.tsx
```

Expected: PASS. If `Button` does not support `asChild` in this codebase, replace the action cell with a plain `Link` styled as an outline button using existing classes:

```tsx
<Link
  aria-label={intl.formatMessage({ id: "conversations.viewMessagesFor" }, { channel: item.channel_id })}
  className="inline-flex h-8 items-center rounded-md border border-input bg-background px-3 text-xs font-medium hover:bg-accent hover:text-accent-foreground"
  to={messagesHref(item.channel_id, item.channel_type)}
>
  {intl.formatMessage({ id: "conversations.viewMessages" })}
</Link>
```

- [ ] **Step 5: Run full relevant web tests**

Run:

```sh
cd web && yarn test src/lib/manager-api.test.ts src/lib/navigation.test.ts src/pages/page-shells.test.tsx src/pages/conversations/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit web page**

Run:

```sh
git add web/src/pages/conversations/page.tsx web/src/pages/conversations/page.test.tsx
git commit -m "feat: add recent conversations page"
```

## Task 6: Final Verification And Documentation Check

**Files:**
- Potential modify: `AGENTS.md` only if directory structure changed in a way this file tracks.
- Potential modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a concise durable project rule was discovered.

- [ ] **Step 1: Check whether AGENTS directory structure needs an update**

Run:

```sh
rg -n "pages/conversations|business/conversations|internal/usecase/management" AGENTS.md docs/development/PROJECT_KNOWLEDGE.md 2>/dev/null || true
```

Expected: No required update for adding one page under existing `web/src/pages` and one usecase file under an existing package. If you discover a durable rule such as “manager recent conversation APIs must reuse conversation sync instead of scanning globally,” add a one-line note to `docs/development/PROJECT_KNOWLEDGE.md`.

- [ ] **Step 2: Run focused Go tests**

Run:

```sh
go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused web tests**

Run:

```sh
cd web && yarn test src/lib/manager-api.test.ts src/lib/navigation.test.ts src/pages/page-shells.test.tsx src/pages/conversations/page.test.tsx
```

Expected: PASS.

- [ ] **Step 4: Run broader web build if focused tests pass**

Run:

```sh
cd web && yarn build
```

Expected: PASS. This may update `web/dist/index.html`; keep that change only if this repository expects built assets to be committed for web changes.

- [ ] **Step 5: Inspect final diff for unrelated changes**

Run:

```sh
git status --short
git diff --stat
git diff --name-only
```

Expected: Only recent-conversation files and intentional touched shared files are staged/unstaged for this task. Existing unrelated dirty files from before the task must remain untouched or clearly separated.

- [ ] **Step 6: Final commit for verification/doc edits if needed**

If Task 6 made documentation or build-output changes, commit only those files:

```sh
git add docs/development/PROJECT_KNOWLEDGE.md AGENTS.md web/dist/index.html
git commit -m "docs: record recent conversations manager notes"
```

If none of those files changed, skip this commit.

## Self-Review Notes

- Spec coverage: the plan covers `GET /manager/conversations`, `cluster.channel:r` protection, management aggregation, app wiring, web API client, route/navigation/i18n, read-only page behavior, and targeted tests.
- Scope boundary: no mutation endpoints, global scans, storage indexes, or cursor pagination are introduced.
- Type consistency: Go request/response names use `RecentConversations*`; web types use `ManagerRecentConversation*`; JSON fields match the spec (`msg_count`, `only_unread`, `recent_messages`).
- Dirty worktree safety: Task 4 explicitly inspects dirty web files before edits and Task 6 checks final diff.
