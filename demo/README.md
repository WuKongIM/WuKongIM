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
聊天 Demo 默认勾选“创建或更新演示凭据”，填写自选测试 UID / Token 即可登录；使用已有凭据时取消勾选。
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

## 公网部署配置

本地体验继续使用 `node demo/start.mjs`，不需要公网配置。公网部署使用独立配置生成器：

```bash
cp demo/deployment.json.example deployment.json
# 修改 deployment.json 的域名、已有 TLS 网关网络与部署标识
node demo/deployment.mjs --config deployment.json --output public-demo
```

输出 `compose.json`、`wukongim.toml`、`frontdoor.conf`、业务进程启动器与解析后的
`deployment.json`，只生成文件，不自动启动服务，也拒绝覆盖已有目录。
公网配置、TLS 网关和独立 Demo 数据均由部署者管理；不会读取或改变本地启动器的配置。

| 配置字段 | 说明 |
| --- | --- |
| `public_url` | 浏览器可访问的 HTTPS 根 Origin；统一生成 API、允许的页面 Origin、`/ws` 与 `/mqtt` 地址。仅 loopback 可使用 HTTP 测试。 |
| `project_name` / `service_prefix` | 独立 Compose 项目与服务名称；网关上游为 `<service_prefix>-product:8088`。 |
| `network` | 已存在的 TLS 网关 Docker 网络，只有前门代理监听该网络。 |
| `cluster_id` / `mqtt_namespace` | 独立演示集群与 MQTT 标识，初始化后保持不变。 |
| `source_revision` | 与 Product 镜像对应的完整 Git commit，业务源码和内嵌首页必须来自该版本。 |
| `chat_ui_revision`（可选） | 单独更新聊天前端时指定完整 Git commit；省略时使用 Product 内嵌聊天页。将该版本已提交的 `internal/access/api/demoui/dist` 导出到 `chat-ui/`，仅供代理只读挂载。 |
| `stream_ui_revision`（可选） | 单独更新流式前端时指定完整 Git commit；将对应 `streamdist` 导出到 `stream-ui/`。代理保留配置生成的 API、首页与模型代理元数据；省略时继续使用业务后端的前端。 |
| `product_image` / `node_image` / `proxy_image` | 三个镜像必须使用 `@sha256:` 固定摘要；示例对应 beta.24。升级时同时更新 Product 镜像与源码版本。 |

Configuration comments: `public_url` is the browser-reachable root origin, shared
by API metadata, allowed browser origins and both public WebSocket endpoints;
HTTPS is mandatory outside loopback. `project_name` isolates Compose ownership,
and `service_prefix` determines service names and the gateway upstream alias.
`network` selects an existing TLS gateway network without creating host ports.
`cluster_id` and `mqtt_namespace` identify this separate Demo deployment and must
remain stable across restarts. `source_revision` is the exact 40-character Product
source commit used for helper code and embedded pages. `product_image`,
`node_image` and `proxy_image` select immutable image digests; upgrade Product
and its source together. These settings belong to the deployment renderer,
while generated Product settings remain in `wukongim.toml`.

每个字段都支持对应的 `WK_DEMO_<大写字段名>` 环境变量覆盖，例如
`WK_DEMO_PUBLIC_URL=https://demo.example.org`。未知字段、非根地址与可变镜像标签会报错。
公网域名和服务器地址不写入任何前端源码或本地默认值。

`chat_ui_revision` / `WK_DEMO_CHAT_UI_REVISION` optionally pins a separately served
chat bundle to an exact source commit. It changes only `/demo/`; Product APIs,
WebSockets, image and business source stay pinned by their existing settings.
For a frontend-only update, export that commit's checked-in bundle to a new
`chat-ui/` directory, validate the generated proxy configuration, then recreate
only the proxy after backing up its existing configuration:

```bash
mkdir public-demo/chat-ui
WK_DEMO_CHAT_UI_REVISION=$(node -p "require('./public-demo/deployment.json').chat_ui_revision")
git archive "$WK_DEMO_CHAT_UI_REVISION" internal/access/api/demoui/dist | tar -x --strip-components=5 -C public-demo/chat-ui
```

`stream_ui_revision` / `WK_DEMO_STREAM_UI_REVISION` similarly selects a read-only
streaming UI, while `/streamdemo/api/chat` retains its guarded loopback relay.
Export its committed bundle before recreating the proxy:

```bash
mkdir public-demo/stream-ui
WK_DEMO_STREAM_UI_REVISION=$(node -p "require('./public-demo/deployment.json').stream_ui_revision")
git archive "$WK_DEMO_STREAM_UI_REVISION" internal/access/api/demoui/streamdist | tar -x --strip-components=5 -C public-demo/stream-ui
```

准备 `public-demo/source`，保留仓库目录结构，包含所选 `source_revision` 下的
`demo/{stream,support,agent,mqtt,live}demo` 和 `internal/access/api/demoui`。
例如在此仓库使用配置中的版本导出：

```bash
mkdir public-demo/source public-demo/data
WK_DEMO_SOURCE_REVISION=$(node -p "require('./public-demo/deployment.json').source_revision")
git archive "$WK_DEMO_SOURCE_REVISION" demo/streamdemo demo/supportdemo demo/agentdemo demo/mqttdemo demo/livedemo internal/access/api/demoui | tar -x -C public-demo/source
WK_DEMO_NODE_IMAGE=$(node -p "require('./public-demo/deployment.json').node_image")
for WK_DEMO_HELPER in agent support; do
  docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/public-demo/source:/workspace" -w "/workspace/demo/${WK_DEMO_HELPER}demo" "$WK_DEMO_NODE_IMAGE" npm ci --ignore-scripts --no-audit --no-fund
  docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/public-demo/source:/workspace" -w "/workspace/demo/${WK_DEMO_HELPER}demo" "$WK_DEMO_NODE_IMAGE" node node_modules/typescript/bin/tsc -p tsconfig.model.json
done
# Product 镜像以 UID 10001 写入独立数据目录；Node 使用 UID 1000 读取源码
sudo chown 10001:10001 public-demo/data
docker compose -f public-demo/compose.json up -d --wait
```

已有 TLS 网关仅将该域名请求转发至生成器输出的 `upstream`，使用 HTTP/1.1，传递
`Upgrade` / `Connection`，关闭响应缓冲并设置至少 190 秒读取超时。
前门负责 `/demos/` 首页、当前 Product 的 `/demo/`、五个独立业务后端、WSMUX 和 MQTT。
`/ws` 转发至 WSMUX 的 `/`；MQTT 保留 `/mqtt`，不能复用 WSMUX 监听器。
业务后端仍只监听 loopback，代理先检查页面 Origin，再按各后端的 loopback Host 契约转发。
流式模型代理还需要 loopback Origin；实际模型密钥仍为请求级配置，默认模拟模型无需 Key。

修改配置时先生成到新目录，校验 `docker compose config` 与 `nginx -t`，再保留现有数据目录、
备份并原位更新配置文件，按变更重载代理或重建服务。不要重新初始化现有集群标识。
仅部署静态前端不会启动业务后端；还必须用浏览器验证开始演示、WS/MQTT 连接与真实消息收发。

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
node demo/test/deployment.integration.mjs
```

真实 256 hash slots 单节点集群验收入口、资源、缓存、一键启动、MQTT、直播与 SDK 消息、流式事件、
端口冲突、异常退出和进程清理。输出报告与日志，不调用付费模型。
