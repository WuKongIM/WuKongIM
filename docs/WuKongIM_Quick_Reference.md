# WuKongIM 快速参考手册

## 快速启动

### 单机模式
```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
go run main.go
```

### 集群模式
```bash
# 节点1
go run main.go --config ./exampleconfig/cluster1.yaml

# 节点2  
go run main.go --config ./exampleconfig/cluster2.yaml

# 节点3
go run main.go --config ./exampleconfig/cluster3.yaml
```

### Docker 部署
```bash
# 单机
docker run -d --name wukongim \
  -p 5001:5001 -p 5100:5100 -p 5200:5200 \
  registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2

# 集群
cd docker/cluster && docker-compose up -d
```

## 核心配置

### 基础配置 (config/wk.yaml)
```yaml
mode: "release"                    # 运行模式
addr: "tcp://0.0.0.0:5100"        # TCP 地址
httpAddr: "0.0.0.0:5001"          # HTTP API 地址
wsAddr: "ws://0.0.0.0:5200"       # WebSocket 地址
rootDir: "./wukongimdata"         # 数据目录
tokenAuthOn: false                # Token 认证
```

### 集群配置
```yaml
cluster:
  nodeId: 1001                    # 节点 ID
  serverAddr: "0.0.0.0:11110"     # 集群通信地址
  initNodes:                      # 初始节点
    1001: "127.0.0.1:11110"
    1002: "127.0.0.1:11111"
    1003: "127.0.0.1:11112"
```

## 核心 API

### 发送消息
```http
POST /message/send
Content-Type: application/json

{
    "from_uid": "sender123",
    "channel_id": "channel456", 
    "channel_type": 2,
    "payload": {
        "type": "text",
        "content": "Hello World"
    }
}
```

### 创建频道
```http
POST /channel
{
    "channel_id": "group789",
    "channel_type": 2,
    "large": false,
    "ban": false
}
```

### 添加订阅者
```http
POST /channel/subscriber_add
{
    "channel_id": "group789",
    "channel_type": 2,
    "subscribers": [
        {"uid": "user123", "role": 1},
        {"uid": "user456", "role": 0}
    ]
}
```

### 同步消息
```http
POST /channel/messagesync
{
    "login_uid": "user123",
    "channel_id": "channel456",
    "channel_type": 2,
    "start_message_seq": 0,
    "end_message_seq": 100,
    "limit": 50
}
```

## 客户端 SDK

### JavaScript SDK
```javascript
import { WKIM, WKIMChannelType, WKIMEvent } from 'easyjssdk';

// 初始化
const im = WKIM.init("ws://localhost:5200", {
    uid: "user123",
    token: "auth_token"
});

// 监听连接
im.on(WKIMEvent.Connect, () => {
    console.log("Connected!");
});

// 监听消息
im.on(WKIMEvent.Message, (message) => {
    console.log("Received:", message);
});

// 发送消息
await im.send("target_user", WKIMChannelType.Person, {
    type: "text",
    content: "Hello!"
});

// 连接
await im.connect();
```

### Android SDK
```java
// 初始化
WKIM.getInstance().init(context, "ws://localhost:5200");

// 连接
WKIMConnectOptions options = new WKIMConnectOptions();
options.uid = "user123";
options.token = "auth_token";
WKIM.getInstance().connect(options);

// 发送消息
WKTextContent textContent = new WKTextContent("Hello!");
WKIM.getInstance().sendMessage(textContent, "target_user", WKChannelType.PERSON);

// 监听消息
WKIM.getInstance().addOnNewMsgListener(new INewMsgListener() {
    @Override
    public void newMsg(WKMsg msg) {
        // 处理新消息
    }
});
```

## 协议格式

### 二进制协议
```
+----------+----------+----------+----------+
|   Type   |  Flag    |  Length  |  Payload |
| (1 byte) | (1 byte) | (4 bytes)|  (变长)   |
+----------+----------+----------+----------+
```

### 消息类型
- `CONNECT(1)`: 连接请求
- `CONNACK(2)`: 连接响应  
- `SEND(3)`: 发送消息
- `SENDACK(4)`: 发送响应
- `RECV(5)`: 接收消息
- `RECVACK(6)`: 接收确认
- `PING(7)`: 心跳请求
- `PONG(8)`: 心跳响应

### JSON 协议 (WebSocket)
```json
// 请求
{
    "id": "request_id",
    "method": "send",
    "params": {
        "clientMsgNo": "uuid",
        "channelId": "channel_id",
        "channelType": 1,
        "payload": {"type": "text", "content": "Hello"}
    }
}

// 响应
{
    "id": "request_id", 
    "result": {
        "messageId": "123456",
        "clientSeq": 1
    }
}
```

## 插件开发

### 插件结构
```go
type MyPlugin struct {
    plugin.BasePlugin
}

func (p *MyPlugin) Send(ctx context.Context, req *proto.SendPacket) (*proto.SendPacket, error) {
    // 处理发送消息
    return req, nil
}

func main() {
    plugin.Run(&plugin.Config{
        Info: plugin.PluginInfo{
            No:      "my-plugin",
            Name:    "My Plugin", 
            Version: "1.0.0",
        },
        Plugin: &MyPlugin{},
    })
}
```

### 插件方法
- `Send`: 发送消息处理
- `ChannelGet`: 获取频道信息
- `UserGet`: 获取用户信息
- `MessageStore`: 消息存储处理

## Webhook 配置

### HTTP Webhook
```yaml
webhook:
  httpUrl: "http://your-server.com/webhook"
  events:
    - "user.connect"
    - "user.disconnect" 
    - "message.send"
    - "message.receive"
```

### 事件格式
```json
{
    "event": "message.send",
    "data": {
        "message_id": "123456789",
        "from_uid": "sender123",
        "channel_id": "channel456",
        "channel_type": 2,
        "payload": {"type": "text", "content": "Hello"},
        "timestamp": 1640995200
    }
}
```

## 监控指标

### Prometheus 指标
- `wukongim_connections_total`: 总连接数
- `wukongim_messages_sent_total`: 发送消息数
- `wukongim_messages_received_total`: 接收消息数
- `wukongim_memory_usage_bytes`: 内存使用量
- `wukongim_cpu_usage_percent`: CPU 使用率

### 健康检查
```bash
curl http://localhost:5001/health
```

### 系统信息
```bash
curl http://localhost:5001/varz
```

## 常用命令

### 编译
```bash
go build -o wukongim main.go
```

### 测试
```bash
go test ./...
```

### 性能测试
```bash
go test -bench=. ./pkg/wkdb
```

### 查看日志
```bash
tail -f logs/wukongim.log
```

## 故障排查

### 常见问题
1. **连接失败**: 检查端口和防火墙
2. **消息丢失**: 检查频道订阅关系
3. **集群同步**: 检查网络和时钟同步
4. **性能问题**: 调整连接池和缓存配置

### 错误码
- `1001`: 连接失败
- `1002`: 认证失败
- `2001`: 消息过大
- `3001`: 频道不存在
- `5001`: 系统过载

### 日志分析
```bash
# 查看错误
grep "ERROR" logs/wukongim.log

# 统计连接数
grep "connection" logs/wukongim.log | wc -l

# 查看慢查询
grep "slow" logs/wukongim.log
```

## 性能调优

### 连接配置
```yaml
userMsgQueueMaxSize: 1000    # 用户消息队列大小
connectTimeout: 30s          # 连接超时
writeTimeout: 10s            # 写超时
readTimeout: 10s             # 读超时
```

### 数据库配置
```yaml
db:
  memTableSize: 64MB         # 内存表大小
  blockCacheSize: 256MB      # 块缓存大小
  writeBufferSize: 32MB      # 写缓冲区大小
  maxOpenFiles: 1000         # 最大打开文件数
```

## 访问地址

- **管理后台**: http://127.0.0.1:5300/web
- **聊天演示**: http://127.0.0.1:5172/chatdemo  
- **API 文档**: http://127.0.0.1:5001/swagger
- **监控指标**: http://127.0.0.1:5001/metrics
- **健康检查**: http://127.0.0.1:5001/health

## 相关链接

- [官方文档](https://githubim.com)
- [GitHub](https://github.com/WuKongIM/WuKongIM)
- [问题反馈](https://github.com/WuKongIM/WuKongIM/issues)
- [更新日志](https://github.com/WuKongIM/WuKongIM/releases)
