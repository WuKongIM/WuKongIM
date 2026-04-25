# `pkg/` 包结构重构方案

> 目标：让包结构一眼映射出三层分布式架构（Controller / Group / Channel）+ 横向基础设施

## 1. 当前问题

| 问题 | 具体表现 |
|------|----------|
| **架构不可见** | 看 `pkg/` 目录无法直接对应"三层"——Controller 碎片化在 `controllerraft/` + `groupcontroller/` + `controllermeta/`；Channel 碎片化在 `isr/` + `isrnode/` + `isrnodetransport/` + `channellog/` |
| **中间层过深** | `replication/` 同时包含 `multiraft/`（Group 层）和 `isr/`（Channel 层），跨了两层 |
| **storage/ 大杂烩** | 6 个子包混在一起：`channellog/`、`controllermeta/`、`metadb/`、`metafsm/`、`metastore/`、`raftstorage/`，跨三层 |
| **过度嵌套** | `transport/nodetransport/` 只有一个子包，多一层没有意义 |
| **wk 前缀冗余** | `protocol/` 下 `wkcodec/`、`wkframe/`、`wkjsonrpc/` 的 `wk` 前缀在项目内部是噪音 |

## 2. 目标结构

```
pkg/
├── controller/          # ═══ L1 · 控制层 ═══
│   ├── raft/            #   Controller Raft 引擎（原 controllerraft/）
│   ├── plane/           #   控制面决策：Planner + StateMachine（原 groupcontroller/）
│   └── meta/            #   控制面持久化 Pebble 存储（原 storage/controllermeta/）
│
├── group/               # ═══ L2 · 元数据层 ═══
│   ├── multiraft/       #   MultiRaft 引擎（原 replication/multiraft/）
│   ├── meta/            #   元数据 Pebble 存储（原 storage/metadb/）
│   ├── fsm/             #   元数据状态机（原 storage/metafsm/）
│   └── proxy/           #   元数据存取代理（原 storage/metastore/）
│
├── channel/             # ═══ L3 · 消息层 ═══
│   ├── isr/             #   ISR 共识核心（原 replication/isr/）
│   ├── node/            #   ISR Node 编排（原 replication/isrnode/）
│   ├── transport/       #   ISR Node 传输（原 replication/isrnodetransport/）
│   └── log/             #   频道日志存储（原 storage/channellog/）
│
├── cluster/             # ═══ 跨层集成（胶水）═══
│   ├── cluster.go       #   原 raftcluster/ 的 15 个文件，提升到 cluster/ 本层
│   ├── agent.go
│   ├── router.go
│   ├── forward.go
│   ├── ...
│   └── managed_groups.go
│
├── transport/           # ═══ 横向 · 集群网络 ═══
│   ├── server.go        #   原 nodetransport/ 的 7 个文件，提升到 transport/ 本层
│   ├── client.go
│   ├── pool.go
│   ├── codec.go
│   ├── rpcmux.go
│   ├── types.go
│   └── errors.go
│
├── raftlog/             # ═══ 横向 · Raft 日志存储 ═══
│   └── ...              #   原 storage/raftstorage/，Controller + Group 共用
│
└── protocol/            # ═══ 外部协议 ═══
    ├── codec/           #   原 wkcodec/（去 wk 前缀）
    ├── frame/           #   原 wkframe/
    └── jsonrpc/         #   原 wkjsonrpc/
```

## 3. 逐层迁移映射

### 3.1 L1 · Controller 控制层

| 原路径 | 新路径 | 说明 |
|--------|--------|------|
| `cluster/controllerraft/` | `controller/raft/` | 包名 `raft`，import 路径 `controller/raft` |
| `cluster/groupcontroller/` | `controller/plane/` | 包名 `plane`，消除原名中与 Group 层混淆 |
| `storage/controllermeta/` | `controller/meta/` | 包名 `meta`，与控制层在同一父目录下 |

### 3.2 L2 · Group 元数据层

| 原路径 | 新路径 | 说明 |
|--------|--------|------|
| `replication/multiraft/` | `group/multiraft/` | 包名不变 |
| `storage/metadb/` | `group/meta/` | 包名 `meta` |
| `storage/metafsm/` | `group/fsm/` | 包名 `fsm` |
| `storage/metastore/` | `group/proxy/` | 包名 `proxy`，避免与 `meta/` 混淆 |

### 3.3 L3 · Channel 消息层

| 原路径 | 新路径 | 说明 |
|--------|--------|------|
| `replication/isr/` | `channel/isr/` | 包名不变 |
| `replication/isrnode/` | `channel/node/` | 包名 `node` |
| `replication/isrnodetransport/` | `channel/transport/` | 包名 `transport` |
| `storage/channellog/` | `channel/log/` | 包名 `log`（与 stdlib `log` 不冲突，因为 import 路径不同） |

### 3.4 横向基础设施

| 原路径 | 新路径 | 说明 |
|--------|--------|------|
| `transport/nodetransport/` | `transport/`（提升） | 消除无意义中间层，包名 `transport` |
| `cluster/raftcluster/` | `cluster/`（提升） | 消除无意义中间层，包名 `cluster` |
| `storage/raftstorage/` | `raftlog/` | 提升到顶层，表达"跨层共用"语义 |
| `protocol/wkcodec/` | `protocol/codec/` | 去 wk 前缀 |
| `protocol/wkframe/` | `protocol/frame/` | 去 wk 前缀 |
| `protocol/wkjsonrpc/` | `protocol/jsonrpc/` | 去 wk 前缀 |

## 4. 待决策点

以下 5 个设计选择需要确认后再动手：

### 决策 1：multiraft 是否放 group/ 下？

| 选项 | 理由 |
|------|------|
| **A. group/multiraft/**（推荐） | multiraft 目前仅被 Group 层使用；架构上它是 Group 层的"引擎" |
| B. 保持 pkg/multiraft/ 顶层 | 如果未来其他层也可能复用 MultiRaft |

### 决策 2：controller/plane/ vs controller/planner/？

| 选项 | 理由 |
|------|------|
| **A. controller/plane/**（推荐） | 更短，"控制面"是标准术语（control plane），覆盖 Planner + StateMachine |
| B. controller/planner/ | 更直白，但只覆盖了 Planner 没覆盖 StateMachine |

### 决策 3：channel/transport/ 命名

| 选项 | 理由 |
|------|------|
| A. channel/xport/ | 避免与顶层 transport/ 读起来混淆 |
| **B. channel/transport/**（推荐） | Go import 路径全限定，不会实际冲突；语义清晰 |
| C. 合并进 channel/node/ | 如果 isrnodetransport 代码量很小 |

### 决策 4：raftlog/ 放顶层还是 storage/ 下？

| 选项 | 理由 |
|------|------|
| **A. pkg/raftlog/**（推荐） | Controller Raft 和 Group MultiRaft 都用它，不属于任何一层 |
| B. pkg/storage/raftlog/ | 保留 storage/ 作为"存储类"聚合目录 |

### 决策 5：protocol/ 的 wk 前缀是否安全移除？

| 选项 | 理由 |
|------|------|
| **A. 移除 wk 前缀**（推荐） | 项目内部无歧义，codec/frame/jsonrpc 语义足够清晰 |
| B. 保留 wk 前缀 | 如果有外部项目依赖 `wkcodec` 等包名 |

## 5. 变更规模评估

| 项目 | 数量 |
|------|------|
| 包目录移动 | ~15 个 |
| import 路径修改 | ~200+ 处 |
| package 声明修改 | ~15 个 |
| 文档更新 | 4 个 wiki 文件 |
| 测试文件移动 | 跟随各包一起移动 |

## 6. 推荐执行顺序（8 批次）

每批次独立可编译、可测试：

| 批次 | 内容 | 风险 |
|------|------|------|
| **Batch 1** | `storage/raftstorage/` → `raftlog/` | 低——被引用点少 |
| **Batch 2** | `transport/nodetransport/` → `transport/` 提升 | 低——单纯提升 |
| **Batch 3** | `controllerraft/` + `groupcontroller/` + `controllermeta/` → `controller/` | 中——三合一 |
| **Batch 4** | `metadb/` + `metafsm/` + `metastore/` + `multiraft/` → `group/` | 中——四合一 |
| **Batch 5** | `isr/` + `isrnode/` + `isrnodetransport/` + `channellog/` → `channel/` | 中——四合一 |
| **Batch 6** | `raftcluster/` → `cluster/` 提升 | 中——引用面广 |
| **Batch 7** | `wkcodec/` + `wkframe/` + `wkjsonrpc/` → `protocol/` 重命名 | 低——可选 |
| **Batch 8** | 更新 `docs/wiki/architecture/` 文档中的代码路径 | 低 |

### 每批次标准流程

```
1. git checkout -b refactor/batch-N
2. mkdir -p 新路径
3. git mv 旧路径/* 新路径/
4. 修改 package 声明
5. sed 全局替换 import 路径
6. go build ./...  （确认编译通过）
7. go test ./...   （确认测试通过）
8. git commit
```

## 7. 重构前后对比

### 重构前

```
pkg/
├── cluster/                    ← 看不出哪些是 Controller，哪些是 Group
│   ├── controllerraft/
│   ├── groupcontroller/        ← 名字暗示 Group，实际是 Controller 的决策引擎
│   └── raftcluster/            ← 跨层胶水，与 controllerraft 容易混淆
├── replication/                ← 同时包含 Group 和 Channel 两层
│   ├── isr/
│   ├── isrnode/
│   ├── isrnodetransport/
│   └── multiraft/
├── storage/                    ← 6 个子包，跨三层混在一起
│   ├── channellog/
│   ├── controllermeta/
│   ├── metadb/
│   ├── metafsm/
│   ├── metastore/
│   └── raftstorage/
├── transport/
│   └── nodetransport/          ← 无意义的中间层
└── protocol/
    ├── wkcodec/                ← wk 前缀冗余
    ├── wkframe/
    └── wkjsonrpc/
```

### 重构后

```
pkg/
├── controller/     ← L1 一目了然
│   ├── raft/
│   ├── plane/
│   └── meta/
├── group/          ← L2 一目了然
│   ├── multiraft/
│   ├── meta/
│   ├── fsm/
│   └── proxy/
├── channel/        ← L3 一目了然
│   ├── isr/
│   ├── node/
│   ├── transport/
│   └── log/
├── cluster/        ← 跨层胶水，扁平化
├── transport/      ← 横向网络，扁平化
├── raftlog/        ← 横向存储
└── protocol/       ← 外部协议，干净命名
    ├── codec/
    ├── frame/
    └── jsonrpc/
```
