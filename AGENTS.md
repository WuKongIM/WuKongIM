# AGENTS.md

## 项目概览

本仓库是 `WuKongIM` 的 Go 单仓，WuKongIM是一款通用高性能通讯系统。

## 运行语义约束

- 本项目没有绕过集群的独立部署语义；单节点部署统一视为“单节点集群”。
- 新增或修改功能时，不再引入绕过集群语义的业务分支；文档、注释、测试命名在描述部署形态时统一使用“单节点集群”，仅节点内行为保留“本地”表述。
- 所有的设计都需要考虑性能问题 比如场景：10万人大群，高频率消息，大量频道，大量在线用户等
- hash slot 数量一般默认是256

## 常用命令

- 单元测试：`GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`
- 集成测试：`GOWORK=off go test -tags=integration ./internal/... ./pkg/... -count=1`（不要随便跑，开发一般跑单元测试即可）
- 端到端测试：`GOWORK=off go test -tags=e2e ./test/e2e/... -count=1`（真实子进程测试，耗时较长）
- 运行主程序：`go run ./cmd/wukongim`
- 显式指定配置文件：`go run ./cmd/wukongim -config ./wukongim.toml`
- 定向测试：`go test ./internal/... ./pkg/...`
- 仓库级 Go 门禁禁止使用根目录 `./...`；Go 不读取 `.gitignore`，会把本机 `tmp/` 和 `web/node_modules/` 下的 Go 包纳入扫描。


## 必须遵循的规则

- 单元测试不要太耗时，如果模拟真实耗时测试做成集成测试 集成测试通过`go test -tags=integration`运行
- 关键方法和主要结构体的属性需要增加英文注释
- 架构设计时不要过度设计

## 找BUG的原则

1. 需要证据，不能靠猜
2. 优先看metrics指标，如果没指标可以补齐观测指标
3. 性能问题可以优先使用pprof

## 配置约定

- 主配置文件使用 `wukongim.toml`，格式为 TOML。
- TOML 文件键名使用按领域分组的 snake_case；环境变量键名统一使用 `WK_` 前缀。
- 不传 `-config` 时，程序按 `./wukongim.toml`、`./conf/wukongim.toml`、`/etc/wukongim/wukongim.toml` 顺序查找。
- 环境变量优先级高于配置文件；列表字段在环境变量中使用 JSON 字符串整体覆盖，例如 `WK_CLUSTER_NODES='[{"id":1,"addr":"wk-node1:7000"}]'`。
- 当配置发生变化时 需要把 `wukongim.toml.example` 对齐。
- 涉及到配置相关的字段必须有详细的英文注释

## 目录结构

```text
.github/
  cloud-sim/              云模拟 bootstrap 示例、工具链版本与 Diagnosis JSON Schema
  workflows/              PR/main 快速门禁与 nightly/manual 重型验证

cmd/
  wukongim/              官方产品入口，负责读取配置并启动 internal/app
  wkanalysis/            运行级 Analysis MCP 网关，提供有界日志、指标、诊断与 pprof 工具
  wkcloudbootstrap/      CloudShell 云账号 OIDC、RAM 角色与最小权限策略引导 CLI
  wkcloudbundle/         云模拟不可变部署 Bundle 生成、封装与校验 CLI
  wkcloudgate/           三节点集群 Bootstrap Gate 严格校验 CLI
  wkcloudhost/           云主机数据盘、配置和 systemd 服务安装 CLI
  wkcloudsim/            云模拟生命周期 CLI，支持 fake 与 Alibaba Provider
  wkcloudview/           云模拟公网 Manager、Demo、Prometheus 与 WebSocket 统一入口
  wkclouddiagnosis/      云模拟 Diagnosis Result 严格校验 CLI
  wkcli/                 可扩展 Cobra 运维 CLI 骨架，预留 top/bench 等子命令入口
  wkbench/               wkbench 黑盒 benchmark CLI，提供 validate/doctor/run/worker/dev-sim/report 入口
  wkdb/                  节点本地只读存储排查 CLI，提供 query/repl 入口

internal/
  app/                   新架构组合根；负责 cluster、message usecase、HTTP API、gateway handler/runtime 装配与生命周期
  access/                新架构入口适配层
    api/                 health、readyz、内嵌聊天 Demo、bench/v1 target 与 legacy channel/user/message/conversation HTTP API 入口
      demoui/            Demo 静态资源 handler 与受 CI 校验的内嵌生产构建产物
    cloudanalysismcp/    运行级 Analysis MCP 的认证、工具 schema 与 usecase 适配
    cloudview/           云模拟公网 HTTP/WS 代理、健康选路、限流与交互判定
    gateway/             gateway presence activation/deactivation、SendPacket/SendBatch -> usecase，Sendack 写回与协议错误映射
    manager/             后台管理 HTTP API 入口、JWT 登录、权限适配与内嵌 Web UI
      webui/             Manager SPA 静态资源 handler 与受 CI 校验的内嵌生产构建产物
    node/                节点间 presence authority/owner-action RPC codec、handler、client
    plugin/              PDK host RPC 入口适配
  bench/                 wkbench 黑盒客户端配置、规划、执行与协调预检，保持中立且不依赖服务端内部包
    config/              wkbench YAML 加载与严格解码
    devsim/              docker compose 开发模拟器 supervisor、状态 API 与配置派生
    planner/             worker 权重、identity pool 与 channel/member/traffic 分片规划
    target/              target HTTP bench API 黑盒客户端
    coordinator/         coordinator preflight 检查 target、worker 与 gateway placeholder
    worker/              wkbench worker 控制 HTTP API 与运行状态
  contracts/             跨用例/运行时的轻量事件合约
    channelappend/       channel append command/result 合约
    channelmembers/      legacy 兼容 member-list channel id 命名合约
    messageevents/       消息提交事件合约
    pluginevents/        插件生命周期与 hook 事件合约
    protocolmeta/        入口无关的协议枚举值合约
  infra/                 新架构外部运行时适配器
    cloudanalysis/       Analysis MCP 到 manager、Prometheus、pprof 与 run inventory 的适配
    cloudsim/            云厂商生命周期适配器；提供 Alibaba、持久化 fake 与原生主机部署能力
    cluster/             cluster/channel append、channel/user metadata 与 presence authority/owner-action 路由适配、typed error 映射
  log/                   新架构应用日志配置与 zap/lumberjack 封装
  observability/         新架构节点内诊断事件、追踪采样与 sendtrace 辅助
    diagnostics/         节点内有界诊断事件、采样、索引与查询
    taskaudit/           控制器任务审计事件投递与查询辅助
  usecase/               可复用业务用例，不依赖具体入口协议
    channel/             频道资料、订阅者、黑白名单等兼容用例
    cloudanalysis/       Run Identity 绑定、工具输入边界、响应上限与诊断预算
    cloudsim/            云模拟生命周期、成本/容量/租期护栏与 Run Locator 合约
    cmdsync/             基于统一会话投影的 CMD 离线同步与 syncack 用例
    conversation/        最近会话列表读模型，基于 UID membership 与 channel_latest 读时 join
    delivery/            投递提交与运行时入队用例
    management/          后台管理节点列表等只读展示用例
    message/             SEND/SendBatch 编排、消息 ID 分配、append port 与 committed event 提交
    plugin/              插件生命周期、绑定、host RPC 和 hook 编排
    presence/            入口无关连接寻址编排、激活/注销/查询、冲突动作调度
    user/                用户 token、device quit、在线状态与 system UID legacy 兼容用例
  runtime/               新架构节点内运行时原语
    cloudviewstate/      云模拟交互与管理修改状态的单调持久化和指标投影
    channelappend/       channel authority write group 与单写者 append 状态机
    conversationactive/  节点内最近会话活跃缓存 admission runtime
    delivery/            节点内在线 fanout、owner push 与 retry runtime
    online/              节点内真实 gateway session 注册、状态、dirty touch 批量标记
    pluginhook/          插件 hook 有界 worker runtime
    presence/            Slot leader 内存权威连接目录、authority epoch、OwnerSeq fencing
    webhook/             节点内 webhook 有界队列、重试和发送 runtime

pkg/
  bench/
    model/               wkbench spec-shaped 配置、计划与 bench/v1 API 共享 DTO
  gateway/               通用客户端网关基础设施，提供 listener、transport、protocol、session、auth、dispatch、testkit
  db/                    节点本地统一存储库；message/meta 共享 engine/key/row/schema/commit/cache 基础设施
    internal/            Pebble engine、key/row codec、schema、commit coordinator、轻量 cache 等内部原语
    message/             Channel 消息日志、索引、checkpoint、epoch history、snapshot、retention、兼容 ChannelStore API
    meta/                Hash-slot 元数据表、批处理、快照、channel runtime meta、conversation、plugin、migration 等存储
  hashslot/              Hash-slot 路由表、迁移状态与再平衡算法
  cluster/               新版集群组合根：control/routing/net/slots/propose/channels/observe 分层，集成 controller、slot/multiraft、channel
  channel/               多 Reactor channel log runtime，用于 v0 append/fetch/replication、runtime 观测、worker 池与 message DB 适配
  controller/            新版控制面：Raft apply 维护最终 cluster-state.json，含 state/statefile/command/fsm/planner/sync/raft/server
    docs/                controller 库用法文档
  observability/         可被 pkg 与 internal 复用的可观测性轻量合约
    sendtrace/           消息发送链路 trace 事件与全局窄 sink
  plugin/
    pluginhost/           节点本地插件进程、Unix socket、热重载和期望状态运行时
    pluginproto/          插件 RPC protobuf wire contract，与 go-pdk 字段号兼容
  protocol/              协议对象、编解码与协议级 helper
    channelid/           个人频道、命令频道、agent 频道、请求级临时频道等 channel id 派生
    codec/               WuKong 二进制协议编解码
    frame/               WuKong frame/object 模型
    jsonrpc/             JSON-RPC schema 与 frame bridge
  raftlog/               Raft 日志持久化实现（存储controller和slot的分布式日志 但是不存储channel层的）
  slot/                  槽位级多副本运行时与分布式元数据
    fsm/                 槽位状态机与命令编解码
    multiraft/           Multi-Raft 基础库
    proxy/               基于 cluster 的分布式存储 / RPC facade
  transport/             新版节点间 transport / RPC 基础库，含 wire/conn/peer/rpc/sched/buffer/testkit
  workqueue/             通用有界 worker pool 与分片 mailbox 底层运行时原语
  wklog/                 通用日志接口与字段封装

docs/
  development/           项目知识、代码质量、CI 门禁与性能排查 runbook
  raw/                   草稿、重构提案与原始设计记录
  superpowers/           specs / plans / reports / runbooks
  wiki/                  项目 wiki 与架构文档

docker/
  conf/                  本地 docker compose 节点配置
  sim/                   可选 wkbench dev-sim 模拟器配置与使用说明

scripts/                 仓库辅助脚本
  cloud-sim/             CloudShell 一键初始化、本地 Codex 云模拟分析、部署门禁与演练辅助脚本
  wukongim/              wukongim 本地启动脚本使用的真实配置文件

test/
  e2e/                   转正后的真实 cmd/wukongim 黑盒 e2e 测试与子进程 harness
    cluster/             动态节点、控制面任务、故障注入等黑盒场景
    message/             internal 消息、会话与 recipient authority 黑盒场景
    control/             控制面 bootstrap、Slot leader transfer 等黑盒场景
    plugin/              插件生命周期、HTTP forward 等黑盒场景
    suite/               e2e 共享黑盒 harness、配置、API 与 metrics 辅助

web/                     Manager Web UI 的 React/Vite 源码、测试与开发服务器配置

demo/
  chatdemo/              内嵌聊天 Demo 的 Vue/Vite 源码与本地开发配置

learn_project/           调研/实验代码，非主执行路径
```

- 当项目目录结构发生变化时更新此文件的`目录结构`

## 代码阅读规则

当阅读某个包的代码内容时先确定此包下是否有`FLOW.md` 文件，如果有则阅读
当代码发生变化与`FLOW.md`文件内容描述不一致时 请更新`FLOW.md`

## 分层约定

- `internal/access/*` 只做新架构入口协议适配，不承载通用业务规则。
- `internal/usecase/*` 承载新架构入口无关业务编排，不能依赖 gateway/frame/cluster 具体实现。
- `internal/runtime/*` 放节点内可复用运行时能力，不放入口逻辑。
- `internal/infra/*` 只做新架构到 pkg 运行时或外部基础设施的适配。
- `internal/app/*` 是 internal 唯一组合根；依赖装配只放这里。
- 旧 v1 runtime 已删除；新增代码不得重新引入 `internal/legacy` 或 `pkg/legacy/*`。
- `pkg/gateway/*` 放可复用网关通用基础设施，不放面向具体业务的用例编排。

## 变更规则

- 新增 HTTP、RPC、任务入口时，优先落到 `internal/access/<entry>`。
- 新增可复用业务能力时，优先落到 `internal/usecase/<domain>`。
- 新增本地状态、在线路由、分配器等能力时，优先落到 `internal/runtime/<capability>`。
- 新增 internal 能力时，保持 `access -> usecase -> ports/contracts`，由 `infra` 实现具体 pkg 运行时适配。
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
