# AGENTS.md

## 项目概览

本仓库是 `WuKongIM` 的 Go 单仓，当前核心方向包括：

- 网关接入与协议适配
- 消息相关用例编排
- 集群、Raft、存储运行时
- 最小 HTTP API 入口

代码组织采用“薄入口 + 可复用用例 + 本地运行时 + 单一组合根”原则，避免把业务继续堆回一个泛化的 `service` 层。

## 运行语义约束

- 本项目没有独立的单机语义；单节点部署统一视为“单节点集群”。
- 新增或修改功能时，不再引入绕过集群语义的业务分支；文档、注释、测试命名在描述部署形态时统一使用“单节点集群”，仅节点内行为保留“本地”表述。

## 常用命令

- 单元测试：`go test ./...`
- 集成测试：`go test -tags=integration ./...` (不要随便跑 时间很长 开发一般跑单元测试即可)
- 运行主程序：`go run ./cmd/wukongim`
- 显式指定配置文件：`go run ./cmd/wukongim -config ./wukongim.conf`
- 定向测试：`go test ./internal/... ./pkg/...`

## 必须遵循的规则

- 单元测试不要太耗时，如果模拟真实耗时测试做成集成测试 集成测试通过`go test -tags=integration`运行
- 关键方法和主要结构体的属性需要增加英文注释

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

internal/
  app/                   组合根；负责 build、lifecycle、config、依赖装配
  access/                接入层，只做入口适配
    api/                 HTTP API 入口与路由适配
    gateway/             网关 frame -> usecase 的适配
    manager/             后台管理 HTTP API 入口、JWT 与权限适配
    node/                节点间 RPC / 转发入口适配
  gateway/               通用网关基础设施
    binding/             内置 handler 绑定与注册
    core/                网关核心 server/dispatcher/registry
    protocol/            协议适配层（wkproto / jsonrpc）
    session/             网关会话模型与管理
    testkit/             网关测试桩与辅助工具
    transport/           gnet 等底层传输实现
    types/               网关通用类型与选项
  log/                   应用日志配置与 zap/lumberjack 封装
  usecase/               可复用业务用例，不依赖具体入口协议
    conversation/        会话投影、同步等用例
    delivery/            投递、离线、订阅等用例
    management/          后台管理聚合查询用例
    message/             消息发送、回执、重试等用例
    presence/            在线状态登记与权威查询用例
    user/                用户与 token 相关用例
  runtime/               节点内运行时原语
    channelid/           个人频道等 channel id 派生
    delivery/            节点内投递 actor / mailbox / retry runtime
    messageid/           消息 ID 分配
    online/              在线会话注册与本地投递
    sequence/            序列号分配

pkg/
  cluster/               集群运行时
  channel/               Channel 维度复制、日志与节点间数据面
    isr/                 单 channel replica group 的 ISR 运行时
    log/                 Channel 消息日志、提交与元数据适配
    node/                Channel 节点侧服务与批处理编排
    transport/           Channel 数据面 RPC transport 适配
  controller/            控制面元数据、规划器与控制器 Raft 服务
    meta/                控制面元数据存储
    plane/               控制面 planner / reconcile 编排
    raft/                控制器单组 Raft 服务
  protocol/              协议对象与编解码
    codec/               WuKong 二进制协议编解码
    frame/               WuKong frame/object 模型
    jsonrpc/             JSON-RPC schema 与 frame bridge
  raftlog/               Raft 日志持久化实现（存储controller和slot的分布式日志 但是不存储channel层的）
  slot/                  槽位级多副本运行时与分布式元数据
    fsm/                 槽位状态机与命令编解码
    meta/                槽位业务元数据存储
    multiraft/           Multi-Raft 基础库
    proxy/               基于 cluster 的分布式存储 / RPC facade
  transport/             节点间 transport / RPC 抽象与实现
  wklog/                 通用日志接口与字段封装

docs/
  raw/                   草稿、重构提案与原始设计记录
  superpowers/           specs / plans / reports / runbooks
  wiki/                  项目 wiki 与架构文档

scripts/                 仓库辅助脚本

test/
  e2e/                   真实二进制黑盒 e2e 测试与子进程 harness

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
- `internal/gateway/*` 放网关通用基础设施，不放面向具体业务的用例编排。

## 变更规则

- 新增 HTTP、RPC、任务入口时，优先落到 `internal/access/<entry>`。
- 新增可复用业务能力时，优先落到 `internal/usecase/<domain>`。
- 新增本地状态、在线路由、分配器等能力时，优先落到 `internal/runtime/<capability>`。
- 不再引入新的“大而全 service 包”或新的全局聚合服务对象。

## 提交前检查

- 至少运行与改动相关的测试。
- 若改动跨层，优先补 `internal/app` 装配测试或入口集成测试。
- 保持依赖方向清晰：`access -> usecase/runtime`，`usecase -> runtime/pkg`，`app -> all`。
