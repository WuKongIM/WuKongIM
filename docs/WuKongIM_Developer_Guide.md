# WuKongIM 开发者快速上手指南

## 项目概述

WuKongIM 是一个高性能的分布式即时通讯服务，支持即时通讯、消息中台、物联网通讯、音视频信令等多种场景。项目采用 Go 语言开发，基于自研的分布式存储引擎，具有去中心化、高可用、高性能的特点。

### 核心特性
- **去中心化设计**：无单点故障，节点平等
- **高性能**：单机支持20万+并发连接
- **分布式存储**：基于 PebbleDB 的自研存储引擎
- **自动容灾**：基于魔改 Raft 协议的故障自动转移
- **多协议支持**：二进制协议 + JSON 协议（WebSocket）
- **插件系统**：支持动态插件扩展

## 项目架构

### 整体架构图
```
┌─────────────────────────────────────────────────────────────┐
│                    客户端层 (Client Layer)                    │
│  Android SDK │ iOS SDK │ Web SDK │ Flutter SDK │ 其他 SDK    │
└─────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────┐
│                    网络层 (Network Layer)                     │
│         TCP │ WebSocket │ WSS │ HTTP API                     │
└─────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────┐
│                    核心服务层 (Core Layer)                     │
│  User Handler │ Channel Handler │ Pusher │ EventBus         │
└─────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────┐
│                   分布式层 (Cluster Layer)                    │
│  Slot Manager │ Channel Cluster │ Config Server │ Event     │
└─────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────┐
│                   存储层 (Storage Layer)                      │
│              WuKongDB (基于 PebbleDB)                        │
└─────────────────────────────────────────────────────────────┘
```

### 目录结构
```
WuKongIM/
├── cmd/                    # 命令行入口
├── config/                 # 配置文件
├── internal/               # 内部核心模块
│   ├── api/               # HTTP API 服务
│   ├── server/            # 服务启动入口
│   ├── user/              # 用户连接处理
│   ├── channel/           # 频道消息处理
│   ├── pusher/            # 消息推送
│   ├── eventbus/          # 事件总线
│   ├── webhook/           # Webhook 服务
│   ├── plugin/            # 插件系统
│   └── options/           # 全局配置
├── pkg/                   # 公共包
│   ├── wkdb/             # 分布式存储引擎
│   ├── wknet/            # 网络层
│   ├── cluster/          # 分布式集群
│   ├── wkserver/         # 服务器框架
│   └── trace/            # 监控追踪
├── web/                   # 管理后台前端
└── demo/                  # 演示应用
```

## 核心模块详解

### 1. 网络层 (pkg/wknet)

网络层基于 Reactor 模式设计，支持 TCP 和 WebSocket 连接。

**核心组件：**
- `Engine`: 网络引擎主入口
- `ReactorMain`: 主 Reactor，负责接受连接
- `ReactorSub`: 子 Reactor，负责处理 I/O 事件
- `Conn`: 连接抽象接口

**关键代码示例：**
```go
// 创建网络引擎
engine := wknet.NewEngine(
    wknet.WithAddr("tcp://0.0.0.0:5100"),
    wknet.WithWSAddr("ws://0.0.0.0:5200"),
)

// 设置事件处理器
engine.OnConnect(func(conn wknet.Conn) {
    // 连接建立处理
})

engine.OnData(func(conn wknet.Conn, data []byte) {
    // 数据接收处理
})

engine.OnClose(func(conn wknet.Conn) {
    // 连接关闭处理
})

// 启动引擎
engine.Start()
```

### 2. 存储层 (pkg/wkdb)

基于 PebbleDB 实现的分布式存储引擎，支持消息、用户、频道等数据的存储。

**核心特性：**
- LSM-Tree 存储结构
- 支持事务操作
- 自动分片和负载均衡
- 数据压缩和去重

**数据模型：**
```go
// 消息结构
type Message struct {
    MessageID   int64     // 消息ID
    MessageSeq  uint64    // 消息序号
    FromUID     string    // 发送者
    ChannelID   string    // 频道ID
    ChannelType uint8     // 频道类型
    Payload     []byte    // 消息内容
    Timestamp   int32     // 时间戳
}

// 频道结构
type Channel struct {
    ChannelID   string    // 频道ID
    ChannelType uint8     // 频道类型 (1:个人 2:群组)
    Ban         bool      // 是否封禁
    Large       bool      // 是否大群
}

// 用户结构
type User struct {
    UID         string    // 用户ID
    Token       string    // 认证令牌
    DeviceFlag  uint64    // 设备标识
    DeviceLevel uint8     // 设备等级
}
```

### 3. 分布式集群 (pkg/cluster)

实现了去中心化的分布式架构，基于魔改的 Raft 协议。

**核心组件：**
- `Server`: 集群服务主入口
- `SlotServer`: 槽位管理服务
- `ChannelServer`: 频道分布式服务
- `ConfigServer`: 配置管理服务
- `NodeManager`: 节点管理

**槽位分片机制：**
```go
// 槽位计算
func (s *SlotServer) GetSlot(channelID string, channelType uint8) uint32 {
    key := fmt.Sprintf("%s-%d", channelID, channelType)
    return wkutil.GetSlotNum(key) % s.opts.SlotCount
}

// 获取槽位领导者
func (s *SlotServer) GetSlotLeader(slot uint32) uint64 {
    slotInfo := s.getSlot(slot)
    if slotInfo == nil {
        return 0
    }
    return slotInfo.Leader
}
```

## 消息处理流程

### 消息发送流程
```
客户端发送 → 网络层接收 → 用户处理器 → 频道处理器 → 存储层 → 推送器 → 目标用户
```

**详细步骤：**

1. **客户端发送消息**
```go
// 发送包结构
type SendPacket struct {
    ClientSeq   uint32  // 客户端序号
    ClientMsgNo string  // 客户端消息编号
    ChannelID   string  // 频道ID
    ChannelType uint8   // 频道类型
    Payload     []byte  // 消息内容
}
```

2. **用户处理器处理**
```go
func (h *Handler) handleOnSend(event *eventbus.Event) {
    // 1. 消息解密
    // 2. 插件处理
    // 3. 权限验证
    // 4. 转发到频道处理器
    eventbus.Channel.OnMessage(event)
}
```

3. **频道处理器处理**
```go
func (h *Handler) handleMessage(event *eventbus.Event) {
    // 1. 存储消息
    // 2. 获取订阅者列表
    // 3. 转发到推送器
    eventbus.Pusher.Push(event)
}
```

4. **推送器分发**
```go
func (p *Pusher) Push(event *eventbus.Event) {
    // 1. 获取在线用户
    // 2. 构造接收包
    // 3. 推送到用户连接
}
```

## 协议设计

### 二进制协议

WuKongIM 使用自定义的二进制协议，具有高效、紧凑的特点。

**协议格式：**
```
+----------+----------+----------+----------+
|   Type   |  Flag    |  Length  |  Payload |
| (1 byte) | (1 byte) | (4 bytes)|  (变长)   |
+----------+----------+----------+----------+
```

**消息类型：**
- `CONNECT`: 连接请求
- `CONNACK`: 连接响应
- `SEND`: 发送消息
- `SENDACK`: 发送响应
- `RECV`: 接收消息
- `RECVACK`: 接收确认
- `PING`: 心跳请求
- `PONG`: 心跳响应

### JSON 协议 (WebSocket)

为 Web 端提供的 JSON-RPC 协议。

**请求格式：**
```json
{
    "id": "request_id",
    "method": "send",
    "params": {
        "clientMsgNo": "uuid",
        "channelId": "channel_id",
        "channelType": 1,
        "payload": {
            "type": "text",
            "content": "Hello World"
        }
    }
}
```

**响应格式：**
```json
{
    "id": "request_id",
    "result": {
        "messageId": "123456",
        "clientSeq": 1
    }
}
```

## 开发环境搭建

### 环境要求
- Go 1.20+
- Git
- Make (可选)

### 快速启动

1. **克隆项目**
```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
```

2. **单机模式启动**
```bash
go run main.go
# 或指定配置文件
go run main.go --config config/wk.yaml
```

3. **分布式模式启动**
```bash
# 节点1
go run main.go --config ./exampleconfig/cluster1.yaml

# 节点2
go run main.go --config ./exampleconfig/cluster2.yaml

# 节点3
go run main.go --config ./exampleconfig/cluster3.yaml
```

4. **访问服务**
- 管理后台: http://127.0.0.1:5300/web
- 聊天演示: http://127.0.0.1:5172/chatdemo
- API 文档: http://127.0.0.1:5001/swagger

### Docker 部署

```bash
# 单机部署
docker run -d \
  --name wukongim \
  -p 5001:5001 \
  -p 5100:5100 \
  -p 5200:5200 \
  -v ./data:/root/wukongim \
  registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2

# 集群部署
cd docker/cluster
docker-compose up -d
```

## 配置详解

### 基础配置 (config/wk.yaml)

```yaml
# 运行模式
mode: "release"  # debug/release/bench

# 网络配置
addr: "tcp://0.0.0.0:5100"      # TCP 监听地址
httpAddr: "0.0.0.0:5001"        # HTTP API 地址
wsAddr: "ws://0.0.0.0:5200"     # WebSocket 地址
wssAddr: "wss://0.0.0.0:5210"   # WSS 地址

# 存储配置
rootDir: "./wukongimdata"       # 数据目录

# 认证配置
tokenAuthOn: false              # 是否开启 Token 认证
managerUID: "____manager"       # 管理员 UID
managerToken: ""                # 管理员 Token

# 外网配置
external:
  ip: ""                        # 外网 IP
  tcpAddr: ""                   # TCP 外网地址
  wsAddr: ""                    # WebSocket 外网地址
  apiUrl: ""                    # API 外网地址

# 集群配置
cluster:
  nodeId: 1001                  # 节点 ID
  serverAddr: "0.0.0.0:11110"   # 集群通信地址
  initNodes:                    # 初始节点列表
    1001: "127.0.0.1:11110"
    1002: "127.0.0.1:11111"
    1003: "127.0.0.1:11112"

# 监控配置
monitor:
  on: true                      # 是否开启监控
  addr: "0.0.0.0:5300"         # 监控地址

# Webhook 配置
webhook:
  httpUrl: "http://localhost:8080/webhook"  # HTTP Webhook 地址
  grpcAddr: "localhost:8081"                # gRPC Webhook 地址

# 插件配置
plugin:
  on: true                      # 是否开启插件
  dir: "./plugins"              # 插件目录
```

### 集群配置示例

**节点1配置 (cluster1.yaml):**
```yaml
cluster:
  nodeId: 1001
  serverAddr: "127.0.0.1:11110"
  initNodes:
    1001: "127.0.0.1:11110"
    1002: "127.0.0.1:11111"
    1003: "127.0.0.1:11112"
addr: "tcp://0.0.0.0:5100"
httpAddr: "0.0.0.0:5001"
wsAddr: "ws://0.0.0.0:5200"
monitor:
  addr: "0.0.0.0:5300"
```

**节点2配置 (cluster2.yaml):**
```yaml
cluster:
  nodeId: 1002
  serverAddr: "127.0.0.1:11111"
  initNodes:
    1001: "127.0.0.1:11110"
    1002: "127.0.0.1:11111"
    1003: "127.0.0.1:11112"
addr: "tcp://0.0.0.0:5101"
httpAddr: "0.0.0.0:5002"
wsAddr: "ws://0.0.0.0:5201"
monitor:
  addr: "0.0.0.0:5301"
```

## API 接口

### 认证相关

**连接认证**
```http
POST /user/token
Content-Type: application/json

{
    "uid": "user123",
    "token": "auth_token"
}
```

### 消息相关

**发送消息**
```http
POST /message/send
Content-Type: application/json

{
    "header": {
        "no_persist": 0,
        "red_dot": 1,
        "sync_once": 0
    },
    "from_uid": "sender123",
    "channel_id": "channel456",
    "channel_type": 2,
    "payload": {
        "type": "text",
        "content": "Hello World"
    }
}
```

**同步消息**
```http
POST /channel/messagesync
Content-Type: application/json

{
    "login_uid": "user123",
    "channel_id": "channel456",
    "channel_type": 2,
    "start_message_seq": 0,
    "end_message_seq": 100,
    "limit": 50,
    "pull_mode": 1
}
```

### 频道相关

**创建频道**
```http
POST /channel
Content-Type: application/json

{
    "channel_id": "group789",
    "channel_type": 2,
    "large": false,
    "ban": false
}
```

**添加订阅者**
```http
POST /channel/subscriber_add
Content-Type: application/json

{
    "channel_id": "group789",
    "channel_type": 2,
    "subscribers": [
        {
            "uid": "user123",
            "role": 1
        },
        {
            "uid": "user456",
            "role": 0
        }
    ]
}
```

### 用户相关

**获取在线状态**
```http
GET /users/onlinestatus?uids=user123,user456
```

**更新用户设备**
```http
POST /user/device_update
Content-Type: application/json

{
    "uid": "user123",
    "device_flag": 1,
    "device_level": 1
}
```

## 插件开发

### 插件架构

WuKongIM 支持动态插件系统，插件通过 Unix Socket 与主进程通信。

**插件生命周期：**
1. 插件启动并连接到主进程
2. 注册插件信息和支持的方法
3. 接收并处理主进程的调用
4. 插件停止时清理资源

### 插件接口

```go
// 插件信息
type PluginInfo struct {
    No          string `json:"no"`           // 插件编号
    Name        string `json:"name"`         // 插件名称
    Version     string `json:"version"`      // 版本
    Description string `json:"description"`  // 描述
}

// 插件方法
type PluginMethod string

const (
    PluginMethodSend         PluginMethod = "send"         // 发送消息
    PluginMethodChannelGet   PluginMethod = "channel_get"  // 获取频道信息
    PluginMethodUserGet      PluginMethod = "user_get"     // 获取用户信息
)
```

### 插件开发示例

```go
package main

import (
    "context"
    "log"
    
    "github.com/WuKongIM/WuKongIM/pkg/plugin"
    "github.com/WuKongIM/WuKongIM/pkg/plugin/proto"
)

type MyPlugin struct {
    plugin.BasePlugin
}

func (p *MyPlugin) Send(ctx context.Context, req *proto.SendPacket) (*proto.SendPacket, error) {
    // 处理发送消息
    log.Printf("Processing message: %s", string(req.Payload))
    
    // 可以修改消息内容
    // req.Payload = []byte("Modified message")
    
    return req, nil
}

func (p *MyPlugin) ChannelGet(ctx context.Context, req *proto.ChannelGetReq) (*proto.ChannelGetResp, error) {
    // 处理频道信息获取
    return &proto.ChannelGetResp{
        ChannelId:   req.ChannelId,
        ChannelType: req.ChannelType,
        Ban:         false,
        Large:       false,
    }, nil
}

func main() {
    p := &MyPlugin{}
    
    // 启动插件
    plugin.Run(&plugin.Config{
        Info: plugin.PluginInfo{
            No:          "my-plugin",
            Name:        "My Plugin",
            Version:     "1.0.0",
            Description: "A sample plugin",
        },
        Plugin: p,
    })
}
```

## Webhook 集成

### HTTP Webhook

**配置 Webhook**
```yaml
webhook:
  httpUrl: "http://your-server.com/webhook"
  events:
    - "user.connect"
    - "user.disconnect"
    - "message.send"
    - "message.receive"
```

**Webhook 事件格式**
```json
{
    "event": "message.send",
    "data": {
        "message_id": "123456789",
        "message_seq": 100,
        "from_uid": "sender123",
        "channel_id": "channel456",
        "channel_type": 2,
        "payload": {
            "type": "text",
            "content": "Hello World"
        },
        "timestamp": 1640995200
    }
}
```

### gRPC Webhook

```protobuf
service WebhookService {
    rpc SendWebhook(EventReq) returns (EventResp);
}

message EventReq {
    string event = 1;
    bytes data = 2;
}

message EventResp {
    int32 status = 1;
}
```

## 监控和运维

### Prometheus 监控

WuKongIM 内置 Prometheus 监控指标：

**系统指标：**
- `wukongim_connections_total`: 总连接数
- `wukongim_messages_sent_total`: 发送消息总数
- `wukongim_messages_received_total`: 接收消息总数
- `wukongim_memory_usage_bytes`: 内存使用量
- `wukongim_cpu_usage_percent`: CPU 使用率

**集群指标：**
- `wukongim_cluster_nodes_total`: 集群节点数
- `wukongim_cluster_slots_total`: 槽位总数
- `wukongim_cluster_messages_sync_total`: 集群消息同步数

**数据库指标：**
- `wukongim_db_operations_total`: 数据库操作总数
- `wukongim_db_operation_duration_seconds`: 数据库操作耗时

### 日志配置

```yaml
logger:
  level: 2        # 1:debug 2:info 3:warn 4:error
  dir: "./logs"   # 日志目录
  lineNum: true   # 是否显示行号
```

### 性能调优

**连接池配置：**
```yaml
# 用户消息队列配置
userMsgQueueMaxSize: 1000

# 连接超时配置
connectTimeout: 30s
writeTimeout: 10s
readTimeout: 10s
```

**存储优化：**
```yaml
# 数据库配置
db:
  memTableSize: 64MB
  blockCacheSize: 256MB
  writeBufferSize: 32MB
  maxOpenFiles: 1000
```

## 常见问题

### 1. 连接问题

**问题：客户端无法连接到服务器**

解决方案：
1. 检查防火墙设置
2. 确认端口配置正确
3. 检查网络连通性
4. 查看服务器日志

### 2. 消息丢失

**问题：消息发送后接收方收不到**

解决方案：
1. 检查频道订阅关系
2. 确认用户在线状态
3. 查看消息存储日志
4. 检查 Webhook 配置

### 3. 集群同步问题

**问题：集群节点间数据不一致**

解决方案：
1. 检查网络连接
2. 确认时钟同步
3. 查看 Raft 日志
4. 重启问题节点

### 4. 性能问题

**问题：系统响应慢或内存占用高**

解决方案：
1. 调整连接池大小
2. 优化数据库配置
3. 增加服务器资源
4. 启用监控分析

## 贡献指南

### 代码规范

1. **Go 代码规范**
   - 使用 `gofmt` 格式化代码
   - 遵循 Go 官方编码规范
   - 添加必要的注释

2. **提交规范**
   - 使用有意义的提交信息
   - 一个提交只做一件事
   - 提交前运行测试

3. **测试要求**
   - 新功能必须包含测试
   - 确保测试覆盖率
   - 运行 `go test ./...` 确保通过

### 开发流程

1. Fork 项目到个人仓库
2. 创建功能分支
3. 开发并测试功能
4. 提交 Pull Request
5. 代码审查和合并

## 总结

WuKongIM 是一个功能强大、架构清晰的分布式即时通讯系统。通过本文档，开发者可以：

1. 理解项目的整体架构和设计理念
2. 快速搭建开发环境并启动服务
3. 掌握核心模块的工作原理
4. 学会使用 API 接口和协议
5. 开发自定义插件和 Webhook 集成
6. 进行系统监控和性能调优

建议开发者从简单的功能开始，逐步深入了解各个模块的实现细节，并结合实际需求进行二次开发。

## 实战开发示例

### 1. 自定义消息类型

**定义消息类型**
```go
// 自定义消息类型
const (
    MessageTypeText     = 1  // 文本消息
    MessageTypeImage    = 2  // 图片消息
    MessageTypeVoice    = 3  // 语音消息
    MessageTypeVideo    = 4  // 视频消息
    MessageTypeFile     = 5  // 文件消息
    MessageTypeLocation = 6  // 位置消息
    MessageTypeCustom   = 100 // 自定义消息起始值
)

// 自定义消息结构
type CustomMessage struct {
    Type    int         `json:"type"`
    Content interface{} `json:"content"`
    Extra   map[string]interface{} `json:"extra,omitempty"`
}

// 位置消息
type LocationMessage struct {
    Type      int     `json:"type"`
    Latitude  float64 `json:"latitude"`
    Longitude float64 `json:"longitude"`
    Address   string  `json:"address"`
    Title     string  `json:"title"`
}
```

**发送自定义消息**
```go
func SendLocationMessage(channelID string, channelType uint8, lat, lng float64, address string) error {
    message := LocationMessage{
        Type:      MessageTypeLocation,
        Latitude:  lat,
        Longitude: lng,
        Address:   address,
        Title:     "我的位置",
    }

    payload, _ := json.Marshal(message)

    return api.SendMessage(&types.MessageSendReq{
        ChannelID:   channelID,
        ChannelType: channelType,
        FromUID:     "sender_uid",
        Payload:     payload,
    })
}
```

### 2. 实现群组管理

**创建群组**
```go
func CreateGroup(groupID string, ownerUID string, members []string) error {
    // 1. 创建频道
    err := api.CreateChannel(&types.ChannelCreateReq{
        ChannelID:   groupID,
        ChannelType: 2, // 群组类型
        Large:       len(members) > 1000,
    })
    if err != nil {
        return err
    }

    // 2. 添加群主
    subscribers := []types.Subscriber{
        {
            UID:  ownerUID,
            Role: 1, // 群主角色
        },
    }

    // 3. 添加成员
    for _, memberUID := range members {
        subscribers = append(subscribers, types.Subscriber{
            UID:  memberUID,
            Role: 0, // 普通成员
        })
    }

    // 4. 设置订阅者
    return api.AddSubscribers(&types.SubscriberAddReq{
        ChannelID:   groupID,
        ChannelType: 2,
        Subscribers: subscribers,
    })
}
```

**群组权限控制**
```go
func CheckGroupPermission(groupID string, userUID string, action string) (bool, error) {
    // 获取用户在群组中的角色
    subscriber, err := api.GetSubscriber(groupID, 2, userUID)
    if err != nil {
        return false, err
    }

    switch action {
    case "send_message":
        // 所有成员都可以发消息
        return subscriber != nil, nil
    case "add_member":
        // 只有群主和管理员可以添加成员
        return subscriber != nil && subscriber.Role >= 1, nil
    case "remove_member":
        // 只有群主可以移除成员
        return subscriber != nil && subscriber.Role >= 2, nil
    default:
        return false, nil
    }
}
```

### 3. 实现消息撤回

**撤回消息插件**
```go
type RecallPlugin struct {
    plugin.BasePlugin
}

func (p *RecallPlugin) Send(ctx context.Context, req *proto.SendPacket) (*proto.SendPacket, error) {
    var message map[string]interface{}
    if err := json.Unmarshal(req.Payload, &message); err != nil {
        return req, nil
    }

    // 检查是否是撤回消息
    if msgType, ok := message["type"].(float64); ok && int(msgType) == MessageTypeRecall {
        return p.handleRecall(ctx, req, message)
    }

    return req, nil
}

func (p *RecallPlugin) handleRecall(ctx context.Context, req *proto.SendPacket, message map[string]interface{}) (*proto.SendPacket, error) {
    // 获取要撤回的消息ID
    targetMsgID, ok := message["target_message_id"].(string)
    if !ok {
        return nil, errors.New("invalid target_message_id")
    }

    // 验证撤回权限（只能撤回自己的消息，且在2分钟内）
    originalMsg, err := p.getMessageByID(targetMsgID)
    if err != nil {
        return nil, err
    }

    if originalMsg.FromUID != req.FromUid {
        return nil, errors.New("can only recall own messages")
    }

    if time.Since(time.Unix(int64(originalMsg.Timestamp), 0)) > 2*time.Minute {
        return nil, errors.New("can only recall messages within 2 minutes")
    }

    // 构造撤回消息
    recallMsg := map[string]interface{}{
        "type": MessageTypeRecall,
        "content": map[string]interface{}{
            "target_message_id": targetMsgID,
            "recall_time": time.Now().Unix(),
        },
    }

    payload, _ := json.Marshal(recallMsg)
    req.Payload = payload

    return req, nil
}
```

### 4. 实现在线状态管理

**在线状态服务**
```go
type OnlineStatusService struct {
    redis    *redis.Client
    eventBus *eventbus.EventBus
}

func NewOnlineStatusService() *OnlineStatusService {
    return &OnlineStatusService{
        redis: redis.NewClient(&redis.Options{
            Addr: "localhost:6379",
        }),
    }
}

func (s *OnlineStatusService) SetUserOnline(uid string, deviceID string) error {
    key := fmt.Sprintf("online:%s", uid)

    // 设置在线状态，过期时间30秒
    err := s.redis.HSet(context.Background(), key, deviceID, time.Now().Unix()).Err()
    if err != nil {
        return err
    }

    err = s.redis.Expire(context.Background(), key, 30*time.Second).Err()
    if err != nil {
        return err
    }

    // 发布在线状态变更事件
    s.eventBus.Publish("user.online", map[string]interface{}{
        "uid":       uid,
        "device_id": deviceID,
        "timestamp": time.Now().Unix(),
    })

    return nil
}

func (s *OnlineStatusService) SetUserOffline(uid string, deviceID string) error {
    key := fmt.Sprintf("online:%s", uid)

    // 移除设备
    err := s.redis.HDel(context.Background(), key, deviceID).Err()
    if err != nil {
        return err
    }

    // 检查是否还有其他设备在线
    devices, err := s.redis.HGetAll(context.Background(), key).Result()
    if err != nil {
        return err
    }

    if len(devices) == 0 {
        // 用户完全离线
        s.eventBus.Publish("user.offline", map[string]interface{}{
            "uid":       uid,
            "timestamp": time.Now().Unix(),
        })
    }

    return nil
}

func (s *OnlineStatusService) IsUserOnline(uid string) (bool, error) {
    key := fmt.Sprintf("online:%s", uid)
    exists, err := s.redis.Exists(context.Background(), key).Result()
    return exists > 0, err
}

func (s *OnlineStatusService) GetOnlineUsers(uids []string) ([]string, error) {
    var onlineUsers []string

    for _, uid := range uids {
        online, err := s.IsUserOnline(uid)
        if err != nil {
            continue
        }
        if online {
            onlineUsers = append(onlineUsers, uid)
        }
    }

    return onlineUsers, nil
}
```

### 5. 实现消息加密

**端到端加密插件**
```go
type E2EEncryptionPlugin struct {
    plugin.BasePlugin
    keyManager *KeyManager
}

func (p *E2EEncryptionPlugin) Send(ctx context.Context, req *proto.SendPacket) (*proto.SendPacket, error) {
    // 检查是否需要加密
    if req.ChannelType == 1 { // 个人聊天需要加密
        return p.encryptMessage(req)
    }
    return req, nil
}

func (p *E2EEncryptionPlugin) encryptMessage(req *proto.SendPacket) (*proto.SendPacket, error) {
    // 获取接收方公钥
    publicKey, err := p.keyManager.GetPublicKey(req.ChannelId)
    if err != nil {
        return nil, err
    }

    // 加密消息
    encryptedPayload, err := p.encrypt(req.Payload, publicKey)
    if err != nil {
        return nil, err
    }

    // 构造加密消息
    encryptedMsg := map[string]interface{}{
        "type": "encrypted",
        "data": base64.StdEncoding.EncodeToString(encryptedPayload),
        "algorithm": "RSA-OAEP",
    }

    payload, _ := json.Marshal(encryptedMsg)
    req.Payload = payload

    return req, nil
}

func (p *E2EEncryptionPlugin) encrypt(data []byte, publicKey *rsa.PublicKey) ([]byte, error) {
    return rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, data, nil)
}
```

### 6. 实现消息审核

**内容审核插件**
```go
type ContentModerationPlugin struct {
    plugin.BasePlugin
    sensitiveWords []string
    aiModerator    *AIModerator
}

func (p *ContentModerationPlugin) Send(ctx context.Context, req *proto.SendPacket) (*proto.SendPacket, error) {
    var message map[string]interface{}
    if err := json.Unmarshal(req.Payload, &message); err != nil {
        return req, nil
    }

    // 检查文本消息
    if msgType, ok := message["type"].(float64); ok && int(msgType) == MessageTypeText {
        content, ok := message["content"].(string)
        if !ok {
            return req, nil
        }

        // 敏感词过滤
        if p.containsSensitiveWords(content) {
            return nil, errors.New("message contains sensitive words")
        }

        // AI 内容审核
        if p.aiModerator != nil {
            safe, err := p.aiModerator.CheckContent(content)
            if err != nil {
                return nil, err
            }
            if !safe {
                return nil, errors.New("message rejected by AI moderator")
            }
        }
    }

    return req, nil
}

func (p *ContentModerationPlugin) containsSensitiveWords(content string) bool {
    content = strings.ToLower(content)
    for _, word := range p.sensitiveWords {
        if strings.Contains(content, strings.ToLower(word)) {
            return true
        }
    }
    return false
}
```

## 性能优化最佳实践

### 1. 连接管理优化

```go
// 连接池配置
type ConnPoolConfig struct {
    MaxConnections    int           // 最大连接数
    IdleTimeout      time.Duration // 空闲超时
    HeartbeatInterval time.Duration // 心跳间隔
    WriteTimeout     time.Duration // 写超时
    ReadTimeout      time.Duration // 读超时
}

// 推荐配置
var OptimalConfig = ConnPoolConfig{
    MaxConnections:    100000,
    IdleTimeout:      300 * time.Second,
    HeartbeatInterval: 30 * time.Second,
    WriteTimeout:     10 * time.Second,
    ReadTimeout:      30 * time.Second,
}
```

### 2. 消息批处理

```go
type MessageBatcher struct {
    messages []Message
    timer    *time.Timer
    mutex    sync.Mutex
    batchSize int
    flushInterval time.Duration
}

func (b *MessageBatcher) AddMessage(msg Message) {
    b.mutex.Lock()
    defer b.mutex.Unlock()

    b.messages = append(b.messages, msg)

    if len(b.messages) >= b.batchSize {
        b.flush()
    } else if b.timer == nil {
        b.timer = time.AfterFunc(b.flushInterval, b.flush)
    }
}

func (b *MessageBatcher) flush() {
    b.mutex.Lock()
    defer b.mutex.Unlock()

    if len(b.messages) == 0 {
        return
    }

    // 批量处理消息
    go b.processBatch(b.messages)

    b.messages = nil
    if b.timer != nil {
        b.timer.Stop()
        b.timer = nil
    }
}
```

### 3. 缓存策略

```go
type CacheManager struct {
    userCache    *cache.Cache
    channelCache *cache.Cache
    messageCache *cache.Cache
}

func NewCacheManager() *CacheManager {
    return &CacheManager{
        userCache:    cache.New(5*time.Minute, 10*time.Minute),
        channelCache: cache.New(10*time.Minute, 20*time.Minute),
        messageCache: cache.New(1*time.Minute, 2*time.Minute),
    }
}

func (c *CacheManager) GetUser(uid string) (*User, error) {
    // 先从缓存获取
    if cached, found := c.userCache.Get(uid); found {
        return cached.(*User), nil
    }

    // 缓存未命中，从数据库获取
    user, err := db.GetUser(uid)
    if err != nil {
        return nil, err
    }

    // 写入缓存
    c.userCache.Set(uid, user, cache.DefaultExpiration)
    return user, nil
}
```

## 故障排查指南

### 1. 常见错误码

```go
const (
    // 连接相关错误
    ErrConnectFailed     = 1001 // 连接失败
    ErrAuthFailed        = 1002 // 认证失败
    ErrConnectTimeout    = 1003 // 连接超时

    // 消息相关错误
    ErrMessageTooLarge   = 2001 // 消息过大
    ErrMessageEncrypt    = 2002 // 消息加密失败
    ErrMessageFormat     = 2003 // 消息格式错误

    // 频道相关错误
    ErrChannelNotFound   = 3001 // 频道不存在
    ErrChannelBanned     = 3002 // 频道被封禁
    ErrPermissionDenied  = 3003 // 权限不足

    // 系统相关错误
    ErrSystemOverload    = 5001 // 系统过载
    ErrDatabaseError     = 5002 // 数据库错误
    ErrNetworkError      = 5003 // 网络错误
)
```

### 2. 日志分析

```bash
# 查看错误日志
tail -f logs/error.log | grep "ERROR"

# 查看连接日志
tail -f logs/info.log | grep "connection"

# 查看性能日志
tail -f logs/info.log | grep "performance"

# 统计错误类型
grep "ERROR" logs/error.log | awk '{print $4}' | sort | uniq -c | sort -nr
```

### 3. 性能监控

```go
// 性能监控中间件
func PerformanceMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // 执行请求
        next.ServeHTTP(w, r)

        // 记录性能指标
        duration := time.Since(start)
        if duration > 100*time.Millisecond {
            log.Printf("Slow request: %s %s took %v", r.Method, r.URL.Path, duration)
        }

        // 更新监控指标
        metrics.RequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
        metrics.RequestTotal.WithLabelValues(r.Method, r.URL.Path).Inc()
    })
}
```

## 参考资源

- [官方文档](https://githubim.com)
- [API 文档](https://githubim.com/api)
- [SDK 文档](https://githubim.com/sdk)
- [GitHub 仓库](https://github.com/WuKongIM/WuKongIM)
- [问题反馈](https://github.com/WuKongIM/WuKongIM/issues)
- [社区讨论](https://github.com/WuKongIM/WuKongIM/discussions)
- [更新日志](https://github.com/WuKongIM/WuKongIM/releases)
