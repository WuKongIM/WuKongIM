# AGENTS.md

## 项目概览

本仓库是 `WuKongIM` 的 Go 单仓，当前核心方向包括：

- 网关接入与协议适配
- 消息相关用例编排
- 集群、Raft、存储运行时
- 最小 HTTP API 入口

代码组织采用“薄入口 + 可复用用例 + 本地运行时 + 单一组合根”原则，避免把业务继续堆回一个泛化的 `service` 层。

## 运行语义约束

- 本项目没有绕过集群的独立部署语义；单节点部署统一视为“单节点集群”。
- 新增或修改功能时，不再引入绕过集群语义的业务分支；文档、注释、测试命名在描述部署形态时统一使用“单节点集群”，仅节点内行为保留“本地”表述。

## 常用命令

- 单元测试：`go test ./...`
- 集成测试：`go test -tags=integration ./...` (不要随便跑 时间很长 开发一般跑单元测试即可)
- 运行主程序：`go run ./cmd/wukongim`
- 显式指定配置文件：`go run ./cmd/wukongim -config ./wukongim.conf`
- 定向测试：`go test ./internal/... ./internalv2/... ./pkg/...`

## 性能调试规则

- 分析 `wk-sim`、`wkbench dev-sim` 或三节点 Docker Compose 性能/超时/吞吐问题时，先阅读并遵循 `docs/development/PERF_TRIAGE.md`。
- 采集证据后再分类、假设和实验，不要凭感觉调参或改代码。

## 必须遵循的规则

- 单元测试不要太耗时，如果模拟真实耗时测试做成集成测试 集成测试通过`go test -tags=integration`运行
- 关键方法和主要结构体的属性需要增加英文注释
- 架构设计时不要过度设计

## 配置约定

- 主配置文件使用 `wukongim.conf`，格式为 `KEY=value`。
- 文件键名与环境变量键名统一，均使用 `WK_` 前缀。
- 不传 `-config` 时，程序按 `./wukongim.conf`、`./conf/wukongim.conf`、`/etc/wukongim/wukongim.conf` 顺序查找。
- 环境变量优先级高于配置文件；列表字段使用 JSON 字符串整体覆盖。
- 当配置发生变化时 需要把 `wukongim.conf.example` 对齐。
- 涉及到配置相关的字段必须有详细的英文注释

## 目录结构

```text
cmd/
  wukongim/              程序入口，负责读取配置并启动应用
  wukongimv2/            internalv2/app 迁移期独立验证入口，当前验证单节点集群 SEND -> SENDACK 骨架
  wkbench/               wkbench 黑盒 benchmark CLI，提供 validate/doctor/run/worker/dev-sim/report 入口
  wkdb/                  节点本地只读存储排查 CLI，提供 query/repl 入口

internal/
  bench/                 wkbench 黑盒客户端配置、模型、规划与协调预检
    config/              wkbench YAML 加载与严格解码
    devsim/              docker compose 开发模拟器 supervisor、状态 API 与配置派生
    model/               wkbench spec-shaped 配置、计划与 bench API DTO
    planner/             worker 权重、identity pool 与 channel/member/traffic 分片规划
    target/              target HTTP bench API 黑盒客户端
    coordinator/         coordinator preflight 检查 target、worker 与 gateway placeholder
    worker/              wkbench worker 控制 HTTP API 与运行状态
  app/                   组合根；负责 build、lifecycle、config、依赖装配
    lifecycle/           生命周期管理器与资源栈原语
  access/                接入层，只做入口适配
    api/                 HTTP API 入口与路由适配
    gateway/             网关 frame -> usecase 的适配
    manager/             后台管理 HTTP API 入口、JWT 与权限适配
    node/                节点间 RPC / 转发入口适配
  log/                   应用日志配置与 zap/lumberjack 封装
  observability/         节点内可观测性辅助
    diagnostics/         节点内有界诊断事件、采样、索引与查询
    sendtrace/           消息发送链路 trace 记录
  contracts/             跨用例/运行时的轻量事件合约
    deliveryevents/      投递回执与离线事件合约
    messageevents/       消息提交事件合约
  usecase/               可复用业务用例，不依赖具体入口协议
    benchdata/           benchmark 数据准备、能力描述与受限批量变更用例
    channel/             频道资料、订阅者、黑白名单等兼容用例
    cmdsync/             CMD 离线同步、syncack 与独立 CMD 会话状态用例
    conversation/        会话投影、同步等用例
    delivery/            投递、离线、订阅等用例
    management/          后台管理聚合查询用例
    message/             消息发送、回执、重试等用例
    presence/            在线状态登记与权威查询用例
    user/                用户与 token 相关用例
  runtime/               节点内运行时原语
    channelmeta/         节点内 channel runtime meta resolver / bootstrap / repair / liveness 合约
    channelid/           个人频道等 channel id 派生
    delivery/            节点内投递 actor / mailbox / retry runtime
    messageid/           消息 ID 分配
    online/              在线会话注册与本地投递
    sequence/            序列号分配
    userlimit/           节点内用户发送令牌桶限流

internalv2/
  app/                   新架构组合根；负责 clusterv2、message usecase、HTTP API、gateway handler/runtime 装配与生命周期
  access/                新架构入口适配层
    api/                 phase-1 health、readyz、bench/v1 target 与 legacy channel/user/message/conversation HTTP API 入口
    gateway/             gateway presence activation/deactivation、SendPacket/SendBatch -> usecase，Sendack 写回与协议错误映射
    node/                新架构节点间 presence authority/owner-action RPC codec、handler、client
  contracts/             新架构跨用例/运行时轻量事件合约
    channelmembers/      legacy 兼容 member-list channel id 命名合约
    messageevents/       消息提交事件合约
  log/                   新架构应用日志配置与 zap/lumberjack 封装
  runtime/               新架构节点内运行时原语
    online/              节点内真实 gateway session 注册、状态、dirty touch 批量标记
    presence/            Slot leader 内存权威连接目录、authority epoch、OwnerSeq fencing
  usecase/               新架构入口无关业务用例
    channel/             频道资料、订阅者、黑白名单等 legacy 兼容用例
    conversation/        最近会话列表读模型，基于 UID membership 与 channel_latest 读时 join
    delivery/            投递提交与运行时入队用例
    message/             SEND/SendBatch 编排、消息 ID 分配、append port 与 committed event 提交
    presence/            入口无关连接寻址编排、激活/注销/查询、冲突动作调度
    user/                用户 token、device quit、在线状态与 system UID legacy 兼容用例
  infra/                 新架构外部运行时适配器
    cluster/             clusterv2/channelv2 append、channel/user metadata 与 presence authority/owner-action 路由适配、typed error 映射

pkg/
  gateway/               通用客户端网关基础设施，提供 listener、transport、protocol、session、auth、dispatch、testkit
  db/                    节点本地统一存储库；message/meta 共享 engine/key/row/schema/commit/cache 基础设施
    internal/            Pebble engine、key/row codec、schema、commit coordinator、轻量 cache 等内部原语
    message/             Channel 消息日志、索引、checkpoint、epoch history、snapshot、retention、兼容 ChannelStore API
    meta/                Hash-slot 元数据表、批处理、快照、channel runtime meta、conversation、plugin、migration 等存储
  cluster/               集群运行时
  clusterv2/             新版集群组合根：control/routing/net/slots/propose/channels/observe 分层，集成 controllerv2、slot/multiraft、channelv2
  channel/               Channel 维度复制、日志与节点间数据面
    handler/             append/fetch/query 入口逻辑与兼容 DurableMessage 编解码
    replica/             单 channel ISR 副本状态机、reconcile、checkpoint、retention 与 promotion 评估
    runtime/             channel runtime 生命周期、复制调度、backpressure 与 tombstone 管理
    transport/           Channel 数据面 RPC transport 适配
  channelv2/             实验性多 Reactor channel log runtime，用于 v0 append/fetch/replication 验证
  controller/            控制面元数据、规划器与控制器 Raft 服务
    meta/                控制面元数据存储
    plane/               控制面 planner / reconcile 编排
    raft/                控制器单组 Raft 服务
  controllerv2/          并行新版控制面：Raft apply 维护最终 cluster-state.json，含 state/statefile/command/fsm/planner/sync/raft/server
    docs/                controllerv2 库用法文档
  observability/         可被 pkg 与 internal 复用的可观测性轻量合约
    sendtrace/           消息发送链路 trace 事件与全局窄 sink
  protocol/              协议对象与编解码
    codec/               WuKong 二进制协议编解码
    frame/               WuKong frame/object 模型
    jsonrpc/             JSON-RPC schema 与 frame bridge
  raftlog/               Raft 日志持久化实现（存储controller和slot的分布式日志 但是不存储channel层的）
  slot/                  槽位级多副本运行时与分布式元数据
    fsm/                 槽位状态机与命令编解码
    multiraft/           Multi-Raft 基础库
    proxy/               基于 cluster 的分布式存储 / RPC facade
  transport/             节点间 transport / RPC 抽象与实现
  transportv2/           新版节点间 transport / RPC 基础库，含 wire/conn/peer/rpc/sched/buffer/testkit
  wklog/                 通用日志接口与字段封装

docs/
  development/           项目知识、代码质量记录与性能排查 runbook
  raw/                   草稿、重构提案与原始设计记录
  superpowers/           specs / plans / reports / runbooks
  wiki/                  项目 wiki 与架构文档

docker/
  conf/                  本地 docker compose 节点配置
  sim/                   可选 wkbench dev-sim 模拟器配置与使用说明

scripts/                 仓库辅助脚本
  wukongimv2/            wukongimv2 本地启动脚本使用的真实配置文件

test/
  e2e/                   真实二进制黑盒 e2e 测试与子进程 harness
    bench/               wkbench 黑盒 CLI e2e 场景
      wkbench_smoke/     单节点集群 wkbench smoke 与 bench API disabled preflight
    cluster/             集群拓扑、快照、扩缩容 e2e 场景
    message/             WKProto 消息投递闭环 e2e 场景
    suite/               e2e 共享黑盒 harness 与客户端辅助

ui/                      内置管理 UI 静态页面
  assets/                前端静态资源
  placeholder/           占位页面与未完成视图

learn_project/           调研/实验代码，非主执行路径
```

- 当项目目录结构发生变化时更新此文件的`目录结构`

## 代码阅读规则

当阅读某个包的代码内容时先确定此包下是否有`FLOW.md` 文件，如果有则阅读
当代码发生变化与`FLOW.md`文件内容描述不一致时 请更新`FLOW.md`

## 分层约定

- `internal/access/*` 只做入口协议适配，不承载通用业务规则。
- `internal/usecase/*` 承载业务编排，输入输出应尽量入口无关。
- `internal/runtime/*` 放节点内可复用运行时能力，不放入口逻辑。
- `internal/app/*` 是唯一组合根；依赖装配只放这里。
- `internalv2/access/*` 只做新架构入口协议适配，不承载通用业务规则。
- `internalv2/usecase/*` 承载新架构入口无关业务编排，不能依赖 gateway/frame/cluster 具体实现。
- `internalv2/infra/*` 只做新架构到 pkg 运行时或外部基础设施的适配。
- `internalv2/app/*` 是 internalv2 唯一组合根；依赖装配只放这里。
- `pkg/gateway/*` 放可复用网关通用基础设施，不放面向具体业务的用例编排。

## 变更规则

- 新增 HTTP、RPC、任务入口时，优先落到 `internal/access/<entry>`。
- 新增可复用业务能力时，优先落到 `internal/usecase/<domain>`。
- 新增本地状态、在线路由、分配器等能力时，优先落到 `internal/runtime/<capability>`。
- 新增 internalv2 能力时，保持 `access -> usecase -> ports/contracts`，由 `infra` 实现具体 pkg 运行时适配。
- 不再引入新的“大而全 service 包”或新的全局聚合服务对象。

## 提交前检查

- 至少运行与改动相关的测试。
- 若改动跨层，优先补 `internal/app` 装配测试或入口集成测试。
- 保持依赖方向清晰：`access -> usecase/runtime`，`usecase -> runtime/pkg`，`app -> all`。

## 编写端对端黑盒测试流程

- 第一步： 通过pkg/metrics 埋点
- 第二步： 在internal/bench 里编写测试计划
- 第三步： 编写script固化流程

## 知识积累

- 发现重要的项目规则或业务知识时，记录到：**`docs/development/PROJECT_KNOWLEDGE.md`** 记录的内容需要简单，清晰，明确 不要啰嗦，保持整个文档内容不要太多
- 在做任务过程中发现一些与此任务无关的代码质量问题可以先记录到 **`docs/development/CODE_QUALITY.md`** 然后继续任务不要中断当前任务
