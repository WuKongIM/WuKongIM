# pkg/gateway 流程文档

## 1. 职责定位

`pkg/gateway` 是公开可复用的客户端接入基础设施包。它负责监听器绑定、底层传输适配、协议编解码、会话生命周期、认证握手、帧分发、transport 异步写、空闲关闭，以及网关级观测事件。

**负责**:
- TCP / WebSocket 客户端连接接入。
- `wkproto`、`jsonrpc`、`wsmux` 协议适配到统一 `frame.Frame`。
- Session 创建、关闭、直接出站写、出站限流、空闲检测与请求上下文取消。
- WKProto CONNECT 认证、协议版本协商和可选端到端加密会话材料保存。
- 将认证后的 Frame 交给注入的 `Handler`，不理解业务含义。
- 网关 drain admission、会话统计、连接/认证/帧处理观测事件。

**不负责**:
- 消息发送、RecvAck、Ping 等业务路由 → `internal/access/gateway`。
- 在线状态、投递、会话、消息用例编排 → `internal/usecase/*`。
- Channel / Slot / Controller 集群运行时 → `pkg/channel`、`pkg/slot`、`pkg/cluster`。
- 配置文件读取与默认配置声明 → `cmd/wukongim` + `internal/app`。

## 2. 集群语义

网关只是当前节点的客户端入口。单节点部署也按“单节点集群”运行，网关不能引入绕过集群语义的业务分支。需要根据 Channel / Slot / UID 做权威路由、写入或状态变更时，必须通过 `internal/access/gateway` 调用用例层，再由用例层进入 runtime / pkg 集群能力。

## 3. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|--------------|------|
| `.` | `gateway.New` → `Gateway` | 对外门面：注册内置 transport/protocol，创建 `core.Server`，暴露启停、监听地址、drain admission 和 session summary |
| `binding/` | `TCPWKProto` / `WSJSONRPC` / `WSMux` | 内置 listener preset，统一生成 `ListenerOptions` |
| `core/` | `core.NewServer` → `Server` | 网关核心：listener runtime、session state、认证流程、decode/dispatch/write loop、idle tracker、async SEND worker pool |
| `protocol/` | `Adapter` | 协议适配接口：Decode/Encode/OnOpen/OnClose |
| `protocol/wkproto/` | `wkproto.New` | WuKong 二进制协议适配，支持粘包拆包、版本协商和加密 RecvPacket 出站封装 |
| `protocol/jsonrpc/` | `jsonrpc.New` | JSON-RPC 与 `frame.Frame` 互转，并保存 request id 作为 reply token |
| `protocol/wsmux/` | `wsmux.New` | WebSocket 复用协议：首包按内容选择 JSON-RPC 或 WKProto，之后固定到 Session |
| `session/` | `session.New` → `Session` | Session 抽象、Session manager、session values 与出站 WriteFrame 串行化 |
| `transport/` | `Factory` / `Listener` / `Conn` | 底层传输抽象，约束 Build 顺序、Start/Stop 和 Conn 回调语义 |
| `transport/gnet/` | `gnet.NewFactory` | 唯一内置 transport，多个逻辑 listener 共享 gnet engine，actor shard 串行化 conn callbacks，内置 TCP 和 WebSocket handshake/frame 支持 |
| `types/` | `Options` / `Handler` / `Context` | 对外类型、错误、close reason、session value key、observer contract |
| `wkprotoenc/` | `SessionCryptoFromSession` 等 | 网关层加密 helper，桥接 `pkg/protocol/wkprotoenc` 与 session values |
| `testkit/` | fake transport/protocol/session | 网关测试桩与 WKProto 加密测试辅助 |

## 4. 对外接口

### 4.1 Gateway 门面

```go
gateway.New(opts Options) (*Gateway, error)
Gateway.Start() error
Gateway.Stop() error
Gateway.ListenerAddr(name string) string
Gateway.SetAcceptingNewSessions(accepting bool)
Gateway.AcceptingNewSessions() bool
Gateway.SessionSummary() core.SessionSummary
```

`ListenerAddr` 会去掉 `http://` / `https://` 前缀，便于 app readyz 和测试直接使用 host:port。

### 4.2 入口配置

```go
type Options struct {
    Handler        Handler
    Authenticator  Authenticator
    Observer       Observer
    DefaultSession SessionOptions
    Transport      TransportOptions
    Listeners      []ListenerOptions
    Logger         wklog.Logger
}

type ListenerOptions struct {
    Name      string
    Network   string // tcp | websocket
    Address   string
    Path      string
    Transport string // gnet
    Protocol  string // wkproto | jsonrpc | wsmux
}
```

`Options.Validate` 会修剪 listener 字段、校验 listener name/address 不重复、补齐 `DefaultSession` 默认值，并要求 `Handler` 非空。

### 4.3 Handler / Context

```go
type Handler interface {
    OnListenerError(listener string, err error)
    OnSessionOpen(ctx Context) error
    OnFrame(ctx Context, f frame.Frame) error
    OnSessionClose(ctx Context) error
    OnSessionError(ctx Context, err error)
}

type SessionActivator interface {
    OnSessionActivate(ctx *Context) (*frame.ConnackPacket, error)
}

type SendBatchHandler interface {
    OnSendBatch(items []SendBatchItem) error
}

type Context struct {
    Session        session.Session
    Listener       string
    Network        string
    Transport      string
    Protocol       string
    CloseReason    CloseReason
    ReplyToken     string
    RequestContext context.Context
}
```

`SessionActivator` 是可选 hook。WKProto CONNECT 认证成功后、CONNACK 写出前调用它；`internal/access/gateway` 使用该 hook 完成 presence activate。

`Handler` 的 `Context` 按值传递，避免热路径为每个 Frame 分配 context wrapper；handler 不应依赖 `Context` 指针身份。`SessionActivator` 仍使用指针参数以兼容认证/激活阶段的 nil 检查习惯。

`SendBatchHandler` 是可选 SEND 微批 hook。实现方必须按 `items` 顺序写回对应 sendack；未实现时 `core` 自动退回逐帧 `OnFrame`，协议语义不变。

### 4.4 扩展接口

```go
// protocol.Adapter
Name() string
Decode(session.Session, []byte) ([]frame.Frame, int, error)
Encode(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error)
OnOpen(session.Session) error
OnClose(session.Session) error

// optional protocol.DecodedFrameOwner
OwnsDecodedFrames() bool

// transport.Factory
Name() string
Build([]transport.ListenerSpec) ([]transport.Listener, error)
```

新增 protocol 或 transport 时，要实现以上接口、补测试，并在 `buildRegistry` 注册；不要把业务处理写进 protocol/transport。

协议适配器只有在 `Decode` 返回的 frame 对象与 payload 字节在返回后保持有效且不会被复用/修改时，才可以实现 `DecodedFrameOwner` 并返回 true。`core` 会利用该能力在异步 SEND 路径中跳过 payload 深拷贝；未声明该能力的适配器继续走深拷贝保护。

## 5. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `Gateway` | `gateway.go` | 对 `core.Server` 的薄封装 |
| `core.Server` | `core/server.go` | 网关核心状态机，管理 listener、session、decode/dispatch/write、idle monitor、async dispatch |
| `core.Registry` | `core/registry.go` | transport factory 与 protocol adapter 注册表 |
| `listenerRuntime` | `core/server.go` | 单个逻辑 listener 的 options、factory、adapter、reply token tracker 和实际 listener |
| `sessionState` | `core/server.go` | 单连接运行时状态：transport conn、session、inbound buffer、认证状态、close reason、request context |
| `dispatcher` | `core/dispatcher.go` | 把 session state 转为 `types.Context` 并调用业务 handler |
| `idleTracker` | `core/idle_tracker.go` | 基于最小堆和 session 索引的读空闲 deadline 调度，刷新时原地更新 deadline，避免按 tick 全量扫描 session |
| `asyncDispatchQueue` | `core/server.go` | SEND 帧异步分发队列，容量有界，满队列时关闭当前 session 形成背压 |
| `asyncSendBatchCollector` | `core/server.go` | 单个 SEND shard 内的有界微批收集器，按等待时间、条数和 payload 字节数 flush，并用 pending slot 保留超限帧 |
| `session.Session` | `session/session.go` | 会话抽象，持有 values，并通过 WriteFrameFn 直接写 transport |
| `transport.Conn` | `transport/transport.go` | 底层连接抽象；gnet Conn.Write 使用 transport 管理的异步写与出站字节限制 |
| `WKProtoAuthOptions` | `auth.go` | WKProto 认证、访客、封禁、版本协商和加密配置 |

## 6. 核心流程

### 6.1 构建与启动

入口: `internal/app/build.go` → `pkg/gateway.New` → `core.NewServer` → `Gateway.Start`

```text
app build:
  ① 创建 `internal/access/gateway`.Handler，注入 message/presence usecase
  ② 根据 metrics 配置创建 gateway.Observer
  ③ gateway.New:
     - buildRegistry 注册 gnet、wkproto、jsonrpc、wsmux
     - core.NewServer 校验 Options
     - 按 listener 的 Transport / Protocol 解析 factory 和 adapter
     - 创建 session.Manager、dispatcher、idleTracker

Gateway.Start:
  ④ buildListeners 按 transport factory 分组
  ⑤ 每个 factory.Build(specs) 必须按 spec 顺序返回 listener
  ⑥ 逐个 listener.Start，失败时反向 Stop 已启动 listener
  ⑦ 启动共享 idle monitor
  ⑧ 启动有界 SEND worker pool
```

默认配置来自 `cmd/wukongim/config.go`:

```text
tcp-wkproto: tcp + gnet + wkproto, 0.0.0.0:5100
ws-gateway: websocket + gnet + wsmux, 0.0.0.0:5200
```

### 6.2 连接接入与 Session 创建

入口: transport accept/read loop → `connHandler.OnOpen` → `core.Server.onOpen`

```text
OnOpen:
  ① 若 AcceptingNewSessions=false，立即关闭底层 conn，用于节点 drain
  ② 创建 sessionState 和 requestContext
  ③ wkproto + Authenticator 非空时标记 authRequired
     - 非 wkproto 且非 wsmux 默认 authenticated
     - wsmux 需要首包确定真实协议后再同步认证策略
  ④ 创建 session.Session，注入 WriteFrameFn=encodeAndWrite
  ⑤ 注册 state 到 core.states 和 session.Manager
  ⑥ adapter.OnOpen
  ⑦ 无需认证且非 wsmux 时立即 dispatch OnSessionOpen
```

`gnet` transport 会让同一个 factory.Build 调用中的多个逻辑 listener 共享一个 engine group；每条连接固定分配到 actor shard，避免每连接 goroutine。

### 6.3 入站 Decode 与 Frame 分发

入口: `core.Server.onData`

```text
OnData:
  ① 更新读活跃时间，刷新 idle deadline；同一 session 在 idleTracker 中只保留一个可调整的 heap entry
  ② 追加到 state.inbound，超过 MaxInboundBytes 则按 inbound_overflow 关闭
  ③ adapter.Decode(session, inbound) 循环解出 0..N 个 frame
     - consumed==0 且无 frame: 等待更多字节
     - consumed 非法或有 frame 但未消费字节: protocol_error 关闭
  ④ syncSessionProtocol:
     - wsmux 首包选出 wkproto/jsonrpc 后写入 SessionValueProtocolName
     - wkproto 且有 Authenticator 时继续要求 CONNECT 认证
     - 非认证协议在首次成功 decode 后补发 OnSessionOpen
  ⑤ 从 ReplyTokenTracker 取 JSON-RPC reply token
  ⑥ handleAuthFrame 优先处理 WKProto CONNECT
  ⑦ 认证完成后的业务 frame:
     - 记录 OnFrameIn
     - SEND: 浅拷贝 packet 元数据后按原始 `ChannelID + ChannelType` 选择 shard，入有界队列；若协议实现 `DecodedFrameOwner` 则复用 decoded payload 并收紧 slice cap，否则深拷贝 payload；worker 在 shard 内收集微批，优先调用 `SendBatchHandler.OnSendBatch`
       - shard 较多时按总缓冲槽位上限动态降低单 shard 容量，避免 worker 数扩张导致启动期常驻内存线性放大
     - SEND 微批只作为入口批处理 hint；个人频道归一化、权限检查和最终严格顺序仍在 `internal/access/gateway` → `internal/usecase/message` → `pkg/channel` 链路内完成
     - Handler 未实现 `SendBatchHandler` 时，worker 保持原顺序逐帧调用 `dispatchFrame`
     - 其他 frame: 同步调用 dispatchFrame
     - SEND 队列满: 按 async_dispatch_queue_full 关闭当前 session
```

`core` 只处理统一的 `frame.Frame` 和生命周期语义，不判断 Send / RecvAck / Ping 的业务含义。业务路由由 `internal/access/gateway.Handler.OnFrame` 完成。

### 6.4 WKProto 认证与激活

入口: `core.Server.handleAuthFrame`

```text
WKProto CONNECT:
  ① 未认证时只接受 ConnectPacket；其他 frame 按 policy_violation 关闭
  ② Authenticator.Authenticate:
     - tokenAuthOn 且非 visitor 时校验 token
     - 可选 IsBanned
     - 协商 server protocol version
     - 加密开启时校验 client key，生成 server key / AES key / AES IV / SessionCrypto
  ③ 写入 session values:
     gateway.uid / device_id / device_flag / device_level / protocol_version
     gateway.encryption_enabled / aes_key / aes_iv / wkproto_crypto
  ④ 若 Handler 实现 SessionActivator，调用 OnSessionActivate
     - access/gateway 在这里调用 presence.Activate
     - 可返回 Connack override
  ⑤ 写出 CONNACK；非 success 时关闭连接
  ⑥ 标记 authenticated，并在首次认证成功后 dispatch OnSessionOpen
```

认证失败会尽量先写出对应 CONNACK，再关闭连接。认证成功后的 `RequestContext` 会在 session close / gateway stop 时取消，用例层可用它中止正在进行的发送。

### 6.5 出站写入与 Reply

入口: `Context.WriteFrame` / `session.Session.WriteFrame`

```text
WriteFrame:
  ① Context.WriteFrame 把 ReplyToken 带入 session.OutboundMeta
  ② session.WriteFrame 串行化出站 encode，避免 Close 与 Write 并发破坏 session 状态
  ③ core.encodeAndWrite 调用当前 protocol adapter.Encode
     - jsonrpc 使用 ReplyToken 作为 response id
     - wkproto 按 session protocol version 编码
     - encrypted session 的 RecvPacket 会自动封装加密 payload 和 msg key
  ④ 记录 OnFrameOut
  ⑤ 直接调用 transport Conn.Write / WriteWebSocketMessage
  ⑥ gnet transport 负责异步写和 MaxOutboundBytes 出站字节限制
  ⑦ transport 出站字节超限会映射到 CloseReasonOutboundOverflow
```

出站流量不会刷新 idle timeout；只有入站数据会刷新读空闲 deadline。

### 6.6 关闭、错误与 Drain

入口: transport close / handler error / idle timeout / gateway stop

```text
sessionState.close:
  ① cancel requestContext
  ② 保存 CloseReason
  ③ 记录 OnConnectionClose
  ④ 从 idleTracker、states、session.Manager 删除
  ⑤ 如 err 非空且已 dispatch OnSessionOpen，调用 OnSessionError(err)
  ⑥ 关闭 session 元数据对象
  ⑦ adapter.OnClose，清理 jsonrpc reply token 等协议状态
  ⑧ 关闭底层 conn
  ⑨ 如已 dispatch OnSessionOpen，调用 OnSessionClose
```

`CloseOnHandlerError` 默认为 true。为 true 时 handler 返回错误会关闭 session；显式设为 false 时只调用 `OnSessionError`，连接继续保留。

Drain 由 `Gateway.SetAcceptingNewSessions(false)` 控制，只拒绝新连接，不主动踢出现有连接。`Gateway.SessionSummary` 会统计所有仍被 core 跟踪的 session，包括尚未认证的连接，供节点 drain 安全判断使用。

## 7. 协议与传输要点

### 7.1 WKProto

- Decode 使用 session 中的 `gateway.protocol_version`，未认证前默认 latest version。
- Encode 未拿到版本时回退 legacy message-seq version，以兼容早期连接。
- 只在出站 `RecvPacket` 自动加密；入站 `SendPacket` 解密在 `internal/access/gateway` 完成，因为需要按业务 reason 写 SendAck。
- 加密材料存放在 session values，`wkprotoenc` helper 会复制 key/iv，避免外部修改底层切片。

### 7.2 JSON-RPC

- Decode 每次从 inbound buffer 解一个 JSON-RPC message，不完整时返回 consumed=0。
- request id 会作为 reply token 存到 adapter 内部队列。
- Encode 时把 `Context.ReplyToken` 转回 response id，保证请求/响应对应。
- `OnClose` 必须清理该 session 的 token 队列。

### 7.3 WSMux

- 首包去掉空白后以 `{` / `[` 开头选择 JSON-RPC，否则选择 WKProto。
- 选择结果写入 `gateway.protocol_name`，后续 Decode/Encode 都固定使用同一个 adapter。
- `TakeReplyTokens` 只在已选择 JSON-RPC 时透传到底层 jsonrpc adapter。

### 7.4 gnet actor runtime

- `gnet` 是唯一内置 transport，支持 TCP 与 WebSocket，并让多个逻辑 listener 共享一个 engine group。
- gnet callbacks 只做协议前置处理并把 open/data/close 事件入 conn queue；固定 actor shard 执行 handler 回调，避免每连接 goroutine。
- `gnet` WebSocket 实现包含 handshake 校验、masked client frame 校验、ping/pong/close、fragment reassembly 和写出 frame 封装。
- transport 层只负责 bytes 与 conn lifecycle，不应依赖 gateway handler 之外的业务对象。

## 8. 依赖边界

- `pkg/gateway` 可以依赖 Go 标准库、`pkg/protocol/*`、`pkg/observability/sendtrace`、`pkg/wklog` 和外部 transport/runtime 依赖。
- `pkg/gateway` 不得依赖 `internal/*`。
- `internal/access/gateway` 可以依赖 `pkg/gateway`，负责把 `frame.Frame` 转为 message/presence 用例命令。
- Session value key 是 gateway 与 access/gateway 的窄合约；新增或重命名时需要同步 mapper、lifecycle 和测试。
- 新业务帧支持优先加在 `internal/access/gateway`；只有 wire format 或连接语义变化才修改 `pkg/gateway`。

## 9. 测试与变更检查

常用测试:

```bash
GOWORK=off go test ./pkg/gateway -count=1
GOWORK=off go test ./pkg/gateway/... -count=1
GOWORK=off go test ./internal/access/gateway -count=1
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

涉及真实 TCP / WebSocket 行为时，优先补 `pkg/gateway` 对应 transport/protocol/core 的单元测试；跨到 message/presence 用例时，补 `internal/access/gateway` 或 `internal/app` 装配测试。配置字段变更必须同步 `cmd/wukongim/config.go`、`internal/app/config.go`、`wukongim.conf.example` 和字段英文注释。

## 10. 避坑清单

- 不要在 `pkg/gateway` 内写消息发送、频道路由、在线注册等业务规则。
- 不要新增“单机直写”或“非集群模式”分支；单节点部署仍是单节点集群。
- 不要让 protocol adapter 持有业务状态；adapter 只处理 wire format 和协议级临时状态。
- 不要在 transport event loop 上执行长耗时业务；SEND 热路径统一进入按 ChannelID 分片的有界 async worker pool，并在 worker 内微批处理。
- 不要让出站写入刷新 idle timeout；idle 语义以客户端入站活跃为准。
- 不要绕过 session encoded queue 直接并发写 conn，除认证 CONNACK 这类必须立即响应的握手帧外都走 `WriteFrame`。
- 新增 listener preset 时保持 `Name`、`Network`、`Address`、`Transport`、`Protocol` 字段完整，并补 `options_test`。
