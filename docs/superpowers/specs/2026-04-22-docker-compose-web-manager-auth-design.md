# Docker Compose Web Manager Auth 设计

- 日期：2026-04-22
- 范围：在 `docker-compose.yml` 中新增 `web` 服务构建与运行，并为 compose 开发集群配置 manager 登录账号
- 关联目录：
  - `docker-compose.yml`
  - `docker/conf/node1.conf`
  - `web/`
  - `wukongim.conf.example`
  - `internal/access/manager`

## 1. 背景

当前仓库已经具备：

- 三节点开发集群的 `docker-compose.yml`
- 前端管理台 `web/`，已经支持 `/login` 和 manager JWT 登录
- 后端 manager 登录接口 `POST /manager/login`

但当前 compose 环境还缺少两块关键能力：

1. 没有独立的 `web` 容器来构建和托管管理台前端
2. compose 中的开发节点配置尚未显式启用 manager 登录账号，导致前端登录无法直接开箱即用

用户明确要求：

- 在 `docker-compose.yml` 里增加 `web` 服务的 build
- `web` 服务通过反向代理对接固定 manager 节点，而不是手动配置 `VITE_API_BASE_URL`
- 登录账号由本次直接配置，用户名可自定，密码固定为 `a1234567`

## 2. 目标与非目标

### 2.1 目标

本次设计目标如下：

1. 在 `docker-compose.yml` 中新增独立 `wk-web` 服务
2. `wk-web` 使用前端多阶段镜像构建并托管 `web/dist`
3. `wk-web` 通过容器内反向代理把 `/manager/*` 转发到固定 manager 节点 `wk-node1:5301`
4. `wk-node1` 开启 manager 登录配置并提供固定开发账号
5. 浏览器只访问 `wk-web`，不需要手动配置 `VITE_API_BASE_URL`
6. 同步 `wukongim.conf.example`，保持 manager 配置示例与仓库配置约定一致
7. 提供最小可验证的 compose 部署闭环：构建、启动、访问登录页、使用固定账号登录

### 2.2 非目标

当前阶段明确不做以下内容：

- 不做 manager 高可用前端负载均衡
- 不让 `wk-web` 在多个 manager 节点间自动切换
- 不暴露 manager `5301` 端口到宿主机作为主要访问方式
- 不引入运行时动态配置前端 API 地址的机制
- 不增加生产级 secret 管理或密码注入系统
- 不修改三节点集群的现有网关/API/raft 端口映射策略
- 不处理 HTTPS、域名、证书等生产部署细节

## 3. 设计原则

### 3.1 浏览器只访问一个同源入口

既然前端已经实现登录流程，本次 compose 部署应尽量让浏览器只感知一个地址：

- `http://localhost:8080`

这样可以避免：

- 手工配置 `VITE_API_BASE_URL`
- 暴露额外 manager 端口给浏览器
- 增加跨域和开发联调复杂度

因此本次前端容器负责同时承载：

- 静态资源托管
- `/manager/*` 反向代理

### 3.2 manager 固定指向一个节点，先保证闭环成立

当前用户明确选择“前端容器反代到固定 manager 节点”的方案。因此第一版不做 manager 多节点路由，而是固定：

- `/manager/*` -> `wk-node1:5301`

这意味着：

- 结构清晰
- compose 最小改动
- 登录和页面访问可以快速闭环

代价是：

- 若 `wk-node1` 不可用，web 管理入口也不可用

对于当前开发 compose 场景，这个取舍是可接受的。

### 3.3 登录账号只为开发环境服务，配置要明显可识别

本次账号只用于本地/compose 开发环境，所以配置策略应满足：

- 开箱即用
- 明确是开发凭证
- 不与生产默认值混淆

本次固定为：

- `username=admin`
- `password=a1234567`

并配合单独的开发 JWT secret，避免与示例配置或其他环境混淆。

### 3.4 配置变更要同步示例文件

根据仓库 `AGENTS.md`：

- 涉及配置变更时，需要把 `wukongim.conf.example` 对齐

因此即使 compose 主要使用 `docker/conf/node1.conf`，本次也必须同步更新 `wukongim.conf.example`，确保 manager 配置示例完整、英文注释清晰。

## 4. 部署结构设计

## 4.1 `wk-web` 服务

`docker-compose.yml` 新增：

- 服务名：`wk-web`
- 职责：托管前端静态站点并反向代理 `/manager/*`
- 对外端口：`8080:80`
- 依赖：`wk-node1`

该服务不直接运行前端开发服务器，而是运行构建后的静态站点容器，保持和现有 compose 中其他服务一致的“稳定运行”语义。

## 4.2 前端镜像构建

在 `web/` 目录新增独立 Docker build：

- 第一阶段：安装依赖并执行前端 build
- 第二阶段：用 `nginx:alpine` 复制 `dist/` 与 Nginx 配置

此方案的优点：

- 不污染当前仓库根部 Go 服务 Dockerfile
- 前后端镜像职责清晰
- `docker-compose.yml` 中的 `wk-web` 可以独立 build

## 4.3 Nginx 反代规则

新增 `web/nginx.conf`，规则如下：

### 静态资源

- `/assets/*`、`/favicon` 等静态文件直接返回

### SPA 路由

- 非文件路径统一回退到 `/index.html`

### Manager API

- `/manager/` 反向代理到 `http://wk-node1:5301`

因此浏览器访问链路为：

```text
Browser -> wk-web:80 -> nginx
  /              -> web dist/index.html
  /assets/*      -> web dist assets
  /manager/*     -> wk-node1:5301
```

## 5. Manager 配置设计

## 5.1 开启节点 manager 服务

在 `docker/conf/node1.conf` 中新增：

- `WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301`
- `WK_MANAGER_AUTH_ON=true`
- `WK_MANAGER_JWT_SECRET=<dev-only-secret>`
- `WK_MANAGER_JWT_ISSUER=wukongim-manager`
- `WK_MANAGER_JWT_EXPIRE=24h`
- `WK_MANAGER_USERS=[...]`

本次只在 `wk-node1` 上显式开启 manager 服务，原因是：

- `wk-web` 当前固定反代到 `wk-node1`
- 先保证闭环成立
- 避免在三节点上重复配置而不产生实际收益

## 5.2 固定开发登录账号

本次 compose 登录账号固定为：

```text
username: admin
password: a1234567
```

这是用户明确接受的开发环境凭证。

## 5.3 权限配置

为了避免登录成功后访问 manager 页面出现大量 `403`，本次账号应直接赋予当前 web 管理台已接入页面所需的开发全权限。

建议资源配置为：

- `cluster.node`
- `cluster.slot`
- `cluster.task`
- `cluster.overview`
- `cluster.channel`

每个资源 action 为：

```json
["*"]
```

这样可以覆盖当前 manager 已有读写接口，减少开发环境阻塞。

## 5.4 JWT Secret 策略

本次使用固定开发 secret，例如：

```text
WK_MANAGER_JWT_SECRET=wukongim-dev-manager-secret
```

要求：

- 明显是开发用途
- 不复用 `change-me`
- 不伪装成生产 secret

## 6. `wukongim.conf.example` 对齐

`wukongim.conf.example` 需要同步体现 manager 配置示例，至少包括：

- `WK_MANAGER_LISTEN_ADDR`
- `WK_MANAGER_AUTH_ON`
- `WK_MANAGER_JWT_SECRET`
- `WK_MANAGER_JWT_ISSUER`
- `WK_MANAGER_JWT_EXPIRE`
- `WK_MANAGER_USERS`

并保持已有英文注释风格清晰，明确说明：

- 这是 manager API 专用端口
- 默认密码仅供开发示例使用，生产必须替换

## 7. 文件变更设计

本次预计变更如下：

- `docker-compose.yml`
  - 新增 `wk-web` 服务
- `docker/conf/node1.conf`
  - 新增 manager 登录配置
- `web/Dockerfile`
  - 新增前端镜像多阶段构建
- `web/nginx.conf`
  - 新增静态站点与 manager 反代配置
- `wukongim.conf.example`
  - 对齐 manager 配置示例

## 8. 验证方案

本次实现完成后需要至少验证：

### 8.1 Compose 配置校验

运行：

- `docker compose config`

确认：

- `wk-web` 服务语法正确
- build、depends_on、ports、volume 路径都可解析

### 8.2 前端镜像构建

运行：

- `docker compose build wk-web`

确认：

- `web` Docker build 成功
- Nginx 运行镜像成功产出

### 8.3 Compose 启动闭环

运行：

- `docker compose up -d`

确认：

- `wk-node1` 正常启动并监听 manager 端口容器内地址 `5301`
- `wk-web` 正常启动并监听宿主机 `8080`

### 8.4 登录验证

浏览器访问：

- `http://localhost:8080/login`

使用以下账号登录：

- `admin / a1234567`

确认：

- 登录请求发往同源 `/manager/login`
- 不需要额外设置 `VITE_API_BASE_URL`
- 登录成功后可进入前端管理台

## 9. 风险与取舍

## 9.1 固定反代单节点 manager

风险：

- `wk-node1` 故障时，web 管理入口不可用

当前取舍：

- 开发 compose 场景可接受
- 先保证登录闭环
- 后续如需要，可再升级为反代到可切换目标或独立 manager LB

## 9.2 开发密码明文写入配置

风险：

- 明文密码进入开发配置文件

当前取舍：

- 用户明确要求由本次直接配置登录账号
- 该密码仅用于本地开发 compose
- 文档与命名会明确其开发属性

## 9.3 只在 `node1.conf` 中配置 manager

风险：

- 其他节点不具备相同 manager 登录入口

当前取舍：

- 与固定反代目标一致
- 避免重复配置
- 当前需求只要求 web 容器有稳定 manager 后端可登录

## 10. 最终方案摘要

本次设计最终确定为：

1. 在 `docker-compose.yml` 中新增 `wk-web` 服务
2. `wk-web` 使用独立前端 Docker build，并通过 Nginx 托管静态站点
3. `wk-web` 把 `/manager/*` 固定反代到 `wk-node1:5301`
4. 在 `docker/conf/node1.conf` 中开启 manager 登录配置
5. 固定开发账号为 `admin / a1234567`
6. 赋予该账号当前 manager 资源的开发全权限
7. 同步更新 `wukongim.conf.example`
8. 用 `docker compose config`、`docker compose build wk-web` 和浏览器登录闭环验证实现结果

这套方案满足用户对 compose 内 web build、固定 manager 反代和开箱即用登录账号的要求，同时保持实现范围最小、结构清晰。
