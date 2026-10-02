# WuKongIM Demo

**共 6 个 Demo。** 一条命令启动并打开首页：

```bash
node demo/start.mjs
```

需要 **Go 1.25+、Node.js 22.12+（或 20.19+）、npm**。首次会下载 Go 与 npm 依赖。
默认使用已提交的前端产物，自动编译服务端和业务运行文件；修改前端后，按各 Demo 的说明重新构建。

| 启动行为 | 结果 |
| --- | --- |
| 自动准备 | WuKongIM 单节点集群 + 首页 + 模型代理 + 客服、Agent、MQTT 与直播演示后端 |
| 就绪后 | 自动打开首页；首页端口默认 5174，占用时换用空闲端口 |
| Ctrl+C / 进程异常 | 关闭本次启动的全部进程 |
| 数据与日志 | 每次独立目录 `demo/.runs/`；保留配置、日志、数据和 `run.json` |

所有服务只监听本机，默认模拟模型无需 Key。六个 Demo 自动指向本次集群。
每个 Demo 顶部都有“返回首页”，聊天登录页也可返回；动态端口和刷新后仍指向进入时的首页。
聊天 Demo 可填写自选测试 UID / Token，勾选“创建或更新演示凭据”登录。
使用现有 WuKongIM 服务时，按下表各 Demo 的说明独立启动；Product HTTP 根地址会打开内嵌首页。

| Demo | 体验 | 入口 | 源码与运行说明 |
| --- | --- | --- | --- |
| 即时聊天 | 单聊、群聊、消息编辑、历史恢复 | `/demo/` | [chatdemo](chatdemo/README.md) |
| 流式回复 | 逐段回复、真实模型、取消与失败 | `/streamdemo/` | [streamdemo](streamdemo/README.md) |
| 在线客服 | AI 接待、人工接管、多访客会话 | `/supportdemo/` | [supportdemo](supportdemo/README.md) |
| Agent | 工具调用、待办确认、暂停与取消 | `/agentdemo/` | [agentdemo](agentdemo/README.md) |
| MQTT 智能门店 | 冷柜告警、远程制冷、设备回执、离线恢复与 IM 协作 | `/mqttdemo/` | [mqttdemo](mqttdemo/README.md) |
| 直播新品发布会 | 实时弹幕、点赞、置顶公告、房间禁言与重连恢复 | `/livedemo/` | [livedemo](livedemo/README.md) |

MQTT Demo 的设备与店员工作台在浏览器中各自建立真实 MQTT 5 WebSocket 连接。
演示后端只准备身份和门店群；告警、指令、执行回执与离线积压都经过 WuKongIM。
本次独立的 MQTT TCP / WebSocket 地址记录在 `run.json` 的 `mqttTcp` / `mqttWs` 中。
MQTT 当前仍为开发预览；本场景验收不替代完整故障和规模资格验证。

直播 Demo 使用内置新品演示画面，两位独立观众的弹幕和点赞直接经过真实 SDK 连接。
主播公告和房间禁言由独立业务后端管理；重连恢复当前公告与权限，不补播旧弹幕。

| 可选设置 | 用途 |
| --- | --- |
| `--no-open` | 仅输出地址，不自动打开浏览器 |
| `WK_DEMO_HOME_PORT` | 固定首页端口；被占用时明确报错 |
| `WK_DEMO_RUN_DIR` | 指定新的运行目录；已有目录保留且拒绝覆盖 |
| `WK_DEMO_SERVER_BIN` | 使用已构建的二进制，跳过 Go 构建 |

启动失败会指出步骤和日志位置；修复后重新执行命令即可。

## 首页开发与预览

```bash
# 无额外依赖
node demo/home/build.mjs
node demo/home/server.mjs
```

打开 **http://127.0.0.1:5174/demos/**。
预览入口跳转至各自的开发服务；可用环境变量修改地址：

| 变量 | 默认地址 |
| --- | --- |
| `WK_DEMO_CHAT_URL` | `http://127.0.0.1:5176/demo/` |
| `WK_DEMO_STREAM_URL` | `http://127.0.0.1:5175/streamdemo/` |
| `WK_DEMO_SUPPORT_URL` | `http://127.0.0.1:5177/supportdemo/` |
| `WK_DEMO_AGENT_URL` | `http://127.0.0.1:5178/agentdemo/` |
| `WK_DEMO_MQTT_URL` | `http://127.0.0.1:5179/mqttdemo/` |
| `WK_DEMO_LIVE_URL` | `http://127.0.0.1:5180/livedemo/` |

例如聊天 Demo 使用 Vite 默认端口时，设置 `WK_DEMO_CHAT_URL=http://127.0.0.1:5173/demo/`。
`WK_DEMO_PORT` 可修改首页预览端口。

首页内嵌资源输出至 `internal/access/api/demoui/homedist/`，需随源码提交。
Go 服务的首页链接使用同源地址。

## 验证

```bash
GOWORK=off go build -o /tmp/wukongim-demo-home ./cmd/wukongim
WK_DEMO_SERVER_BIN=/tmp/wukongim-demo-home node demo/home/test/home.integration.mjs
WK_DEMO_SERVER_BIN=/tmp/wukongim-demo-home node demo/test/start.integration.mjs
```

真实 256 hash slots 单节点集群验收入口、资源、缓存、一键启动、MQTT、直播与 SDK 消息、流式事件、
端口冲突、异常退出和进程清理。输出报告与日志，不调用付费模型。
