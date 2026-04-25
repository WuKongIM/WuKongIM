# wk-web API Proxy Config Design

- 范围：让 `wk-web` 的 `/manager/` 反向代理目标不再写死在 `web/nginx.conf` 中，而是支持通过容器环境变量配置。
- 背景：当前 `web/nginx.conf` 把 manager API 地址固定为 `http://wk-node1:5301`，导致 `wk-web` 在不同 compose 场景下无法复用同一镜像，只能改镜像内配置。

## Goals

1. 为 `wk-web` 增加 `WK_WEB_API_URL` 环境变量，用于配置 `/manager/` 的上游地址。
2. 在未显式传值时，保持默认回退到 `http://wk-node1:5301`，兼容现有开发集群。
3. 允许在 `docker-compose.yml` 中直接为 `wk-web` 动态配置该地址，而不需要重新构建镜像。

## Non-Goals

- 不改前端 `web/src/*` 的请求逻辑。
- 不改 Go manager API 路由或任何后端监听地址。
- 不新增独立的配置文件解析逻辑。

## Design

### Container-side templated nginx config

把 `web/nginx.conf` 改为 nginx 模板文件，在 `proxy_pass` 位置使用 `${WK_WEB_API_URL}` 占位。`wk-web` 容器继续基于官方 `nginx:1.27-alpine` 镜像启动，并复用其 `/etc/nginx/templates/*.template` 启动期渲染机制生成最终的 `default.conf`。

### Default behavior

在 `web/Dockerfile` 中声明：

- `ENV WK_WEB_API_URL=http://wk-node1:5301`

这样即使 `docker-compose.yml` 没有显式传入该变量，容器内的 nginx 模板渲染结果仍与当前行为一致。

### Compose contract

`docker-compose.yml` 的 `wk-web` 服务新增：

- `WK_WEB_API_URL=${WK_WEB_API_URL:-http://wk-node1:5301}`

这样既可以直接改 compose 文件中的默认值，也可以通过外部环境变量覆盖，而无需改镜像内容。

## Files

- Rename: `web/nginx.conf` -> `web/nginx.conf.template`
- Modify: `web/Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `web/README.md`

## Verification

1. 运行 `docker compose config`，确认 `wk-web.environment.WK_WEB_API_URL` 已出现在展开结果中。
2. 渲染模板并检查生成结果，确认 `proxy_pass` 指向期望的 `WK_WEB_API_URL`。
3. 确认未提供变量时仍回退到 `http://wk-node1:5301`。
