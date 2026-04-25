# Web Manager Login And Auth 设计

- 日期：2026-04-22
- 范围：为 `web/` 管理台新增登录界面、JWT 登录态管理、路由鉴权、退出登录，以及对接 manager `POST /manager/login`
- 关联目录：
  - `web/src/app/router.tsx`
  - `web/src/app/providers.tsx`
  - `web/src/app/layout/topbar.tsx`
  - `web/src/pages/login/page.tsx`
  - `web/src/auth/`
  - `web/src/lib/manager-api.ts`
  - `web/src/pages/`
  - `web/src/test/`
  - `internal/access/manager/routes.go`
  - `internal/access/manager/login.go`
  - `docs/superpowers/specs/2026-04-22-web-admin-shell-design.md`
  - `docs/superpowers/specs/2026-04-22-web-admin-shell-monochrome-simplification-design.md`

## 1. 背景

当前仓库中的 `web/` 工程已经具备以下基础：

- React + Vite + TypeScript 的 SPA 壳子
- `AppShell + SidebarNav + Topbar` 的统一管理台布局
- `dashboard`、`nodes`、`slots` 等页面骨架
- 单元测试基础设施（Vitest + Testing Library）

但根据 `web/README.md`，当前 `web/` 仍处于“只提供管理壳子”的阶段，明确不包含：

- auth
- data fetching
- backend API integration

与此同时，manager 后端已经提供静态用户登录接口：

- 路径：`POST /manager/login`
- 位置：`internal/access/manager/routes.go`、`internal/access/manager/login.go`

该接口在认证成功后返回：

- `username`
- `token_type`
- `access_token`
- `expires_in`
- `expires_at`
- `permissions`

因此本轮需要在现有 monochrome admin shell 基础上，补齐第一版最小认证闭环：

- 登录页
- JWT 登录态保存
- 刷新后的会话恢复
- 未登录访问受保护页面时自动回到登录页
- 顶部退出登录入口
- manager API 请求自动携带 Bearer Token
- 遇到 `401` 时自动清空登录态并回到登录页

## 2. 目标与非目标

### 2.1 目标

本次设计目标如下：

1. 为 `web/` 新增独立 `/login` 页面
2. 对接 manager `POST /manager/login` 接口完成真实登录
3. 在前端使用 `Zustand` 管理认证状态
4. 将登录态持久化到浏览器 `localStorage`
5. 页面刷新后能够恢复未过期会话
6. 为现有管理页面增加统一路由保护
7. 已登录访问 `/login` 时自动跳回 `/dashboard`
8. 在 `Topbar` 增加当前用户信息和退出登录入口
9. manager API 统一支持 `Authorization: Bearer <token>`
10. manager API 遇到 `401` 时自动登出并重定向到 `/login`
11. 支持 `VITE_API_BASE_URL` 作为可选 API 基础地址；未配置时默认同源
12. 为上述行为补齐前端单元测试

### 2.2 非目标

当前阶段明确不做以下内容：

- 不实现 refresh token
- 不实现后端 logout 接口
- 不实现密码找回、修改密码、验证码、多因子认证
- 不实现基于 `permissions` 的导航裁剪或页面级按钮裁剪
- 不实现 manager 其他真实数据接口的接入
- 不引入数据请求缓存框架，例如 `tanstack query`
- 不处理生产环境 `web/dist` 静态资源托管集成
- 不新增深色模式或重新设计现有 monochrome shell 视觉体系

## 3. 设计原则

### 3.1 认证能力单独收口，不把登录态散落在页面中

登录、退出登录、恢复会话、处理 `401` 都是跨页面共享的能力。如果把这些逻辑直接放进登录页、路由文件或单个页面组件里，后续接入真实 manager API 时会快速失控。

因此本次采用：

- `Zustand` store 持有认证状态
- 统一 `manager-api` 负责带 token 和处理 `401`
- 路由守卫只判断认证状态，不直接处理请求细节
- 登录页只负责表单输入和触发登录动作

### 3.2 保持现有 shell 结构稳定，登录页独立于 AppShell

当前 `web/` 的核心价值是已经有一套稳定的壳子布局：

- 左侧导航
- 顶部栏
- 页面内容区域

这套壳子只应该用于“已登录的管理台内部页面”。登录页属于入口界面，不能为了它去污染整个 `AppShell`。

因此本次设计为：

- `/login` 独立渲染，不挂在 `AppShell` 下
- `/dashboard`、`/nodes` 等现有页面继续保留在 `AppShell` 下
- 只在受保护页面外层增加统一鉴权包装

### 3.3 API 配置默认同源，开发时允许显式覆盖

manager shell 最自然的部署方式是与 Go 服务同源访问，因此默认行为应保持简单：

- 未配置 `VITE_API_BASE_URL` 时，直接请求 `/manager/*`

但开发或联调时，也需要支持前端指向独立 manager 地址。因此保留：

- `VITE_API_BASE_URL`

该变量存在时，所有 manager 请求都以它作为前缀。

### 3.4 第一版只做认证闭环，不提前引入权限可视化复杂度

后端 `POST /manager/login` 已经返回 `permissions`，但当前用户要求只先完成登录闭环。因此第一版应：

- 保存 `permissions`
- 供后续页面使用
- 但暂不按权限隐藏侧边栏或页面按钮

这样既保留扩展空间，也避免在没有真实业务页面的前提下过早增加复杂度。

## 4. 技术选型

## 4.1 状态管理：`Zustand`

本次前端认证状态管理使用 `Zustand`，原因如下：

- 当前 `web/` 规模较轻，`Zustand` 比 `Redux Toolkit` 更适合第一版登录闭环
- store 可在 React 组件外访问，方便 `manager-api` 直接读取 token 并处理 `401`
- 后续如果增加 `overview`、`nodes`、`slots` 等前端状态，也可以继续沿用相同模式

### 4.2 请求层：原生 `fetch` 封装

本次不引入额外 HTTP SDK，统一基于浏览器 `fetch` 封装一个轻量 manager client：

- 统一处理基础 URL
- 统一处理 JSON body
- 统一处理 `Authorization`
- 统一处理错误响应与 `401`

### 4.3 持久化：`localStorage`

第一版认证持久化使用浏览器 `localStorage`：

- 简单直接
- 能覆盖刷新恢复场景
- 与 `Zustand` 的状态模型天然兼容

## 5. 路由与页面结构

## 5.1 路由拆分

当前 `router.tsx` 只有一个根路由和 `AppShell`。本次调整为“两段式”结构：

### 公共路由

- `/login`

### 受保护路由

以下页面继续挂在 `AppShell` 下，但外层增加统一保护：

- `/dashboard`
- `/nodes`
- `/channels`
- `/connections`
- `/slots`
- `/network`
- `/topology`

## 5.2 `ProtectedRoute` 行为

新增 `ProtectedRoute` 或等价保护组件，规则如下：

1. 如果认证状态还未完成恢复（例如首次启动正在从 `localStorage` 恢复），不立即重定向
2. 如果会话恢复完成且用户未登录，跳转到 `/login`
3. 如果用户已登录，放行渲染 `AppShell`

这样可以避免刷新时出现“先跳登录页再跳回来”的闪烁。

## 5.3 `/login` 页面行为

### 未登录访问 `/login`

- 展示登录表单

### 已登录访问 `/login`

- 直接重定向到 `/dashboard`

### 登录成功

- 写入认证状态
- 跳转到 `/dashboard`

### 登录失败

- 保持当前页
- 展示明确错误文案
- 恢复提交按钮可用状态

## 6. 认证状态模型

## 6.1 Store 结构

认证 store 需要覆盖以下状态：

- `status`
  - `anonymous`
  - `authenticated`
- `isHydrated`
- `username`
- `tokenType`
- `accessToken`
- `expiresAt`
- `permissions`

建议的前端会话结构如下：

```ts
export type AuthStatus = "anonymous" | "authenticated"

export type AuthPermission = {
  resource: string
  actions: string[]
}

export type AuthSession = {
  username: string
  tokenType: string
  accessToken: string
  expiresAt: string
  permissions: AuthPermission[]
}
```

其中：

- `expiresAt` 保持 ISO 时间字符串
- 到期判断使用 `Date.parse(expiresAt)`
- `permissions` 保持与后端 DTO 一致的数组结构，避免无谓转换

## 6.2 Store 动作

认证 store 需要提供至少以下动作：

- `login(credentials)`
- `restoreSession()`
- `logout()`
- `handleUnauthorized()`

### `login(credentials)`

职责：

- 调用 `POST /manager/login`
- 校验响应结构
- 把 `username`、`tokenType`、`accessToken`、`expiresAt`、`permissions` 写入 store
- 同步持久化

### `restoreSession()`

职责：

- 从 `localStorage` 恢复上次会话
- 如果没有本地会话，标记 `isHydrated=true` 并保持匿名态
- 如果本地 `expiresAt` 已过期，清理本地数据并回到匿名态
- 如果本地会话有效，恢复到已登录状态并标记 `isHydrated=true`

### `logout()`

职责：

- 清空内存登录态
- 清空 `localStorage`
- 跳回 `/login`

### `handleUnauthorized()`

职责：

- 用于统一处理 manager API 返回 `401`
- 行为与 `logout()` 接近，但语义上表示“服务端已判定当前会话失效”

## 6.3 持久化键名

为了避免与未来其他前端状态混淆，建议使用单独键名：

- `wukongim_manager_auth`

本地保存内容只包括认证必要字段，不保存页面临时 UI 状态。

## 7. Manager API 设计

## 7.1 基础地址

新增 `web/src/lib/manager-api.ts`，统一构造 manager 请求地址：

- 若 `VITE_API_BASE_URL` 未设置：使用同源相对地址，例如 `/manager/login`
- 若 `VITE_API_BASE_URL` 已设置：使用 `${VITE_API_BASE_URL}/manager/login`

为了避免路径拼接错误，需要在构造时规避：

- 末尾多余 `/`
- base URL 为空字符串

## 7.2 请求头策略

统一规则如下：

- 默认发送 `Accept: application/json`
- 有 JSON body 时发送 `Content-Type: application/json`
- 当前 store 中存在 token 时，自动注入：

```text
Authorization: Bearer <accessToken>
```

即使后端登录返回 `token_type="Bearer"`，前端第一版也固定按 Bearer 发送请求，不引入可变 token scheme 逻辑。

## 7.3 登录接口封装

新增 `loginManager(credentials)` 或等价方法，专门封装：

- 请求：`POST /manager/login`
- body：

```json
{
  "username": "admin",
  "password": "secret"
}
```

- 成功响应读取：

```json
{
  "username": "admin",
  "token_type": "Bearer",
  "access_token": "...",
  "expires_in": 3600,
  "expires_at": "2026-04-22T12:00:00Z",
  "permissions": [
    {
      "resource": "cluster.node",
      "actions": ["r"]
    }
  ]
}
```

前端只依赖当前后端已经稳定暴露的字段，不读取任何 legacy `token` 字段。

## 7.4 错误归一

登录页需要把后端错误码映射成稳定的 UI 提示：

- `400` → “Request is invalid.”
- `401` → “Invalid username or password.”
- `500` / `502` / `503` → “Login service is unavailable. Please try again.”
- 其他未知错误 → “Unable to sign in right now.”

本轮只做英文提示，与当前 shell 英文页面文案保持一致。

## 7.5 `401` 自动回退

对于除登录接口以外的 manager 请求：

- 如果返回 `401`
- `manager-api` 调用 `authStore.getState().handleUnauthorized()`
- 清空本地登录态
- 触发前端回到 `/login`

这样可以覆盖：

- token 过期
- manager JWT secret 变化
- token 被服务端判定无效

## 8. UI 设计

## 8.1 登录页布局

登录页延续当前 monochrome shell 视觉，不引入彩色品牌页风格。布局采用双区块：

### 左侧信息区

用于表达产品上下文：

- 标题：`WuKongIM Manager`
- 一句简短说明，强调其是 cluster/runtime 管理入口
- 1 到 2 行简洁的辅助说明，例如 node、slot、channel runtime 的观察与运维语义

### 右侧登录卡片

用于承载表单：

- `Username`
- `Password`
- `Sign in` 按钮
- 错误提示区域
- 可选的简短安全说明文案

整体视觉要求：

- 保持浅色、黑白灰、边框分层的现有方向
- 不引入渐变、发光、玻璃拟态
- 按钮和输入框延续现有基础组件风格

## 8.2 响应式规则

### 桌面端

- 双栏布局
- 登录卡片保持较集中宽度，避免表单过宽

### 移动端

- 自动收成单列布局
- 信息区缩短为紧凑标题和说明
- 表单占主视觉焦点

## 8.3 提交交互

表单行为如下：

- 用户名、密码都为空时禁用按钮不是必须要求；第一版可允许提交后由后端返回错误
- 提交中按钮显示 loading 文案，例如 `Signing in...`
- 提交中禁用重复点击
- 密码字段使用标准 password input
- 登录成功后不弹 toast，直接进入 `/dashboard`

## 8.4 顶栏退出登录入口

`Topbar` 增加登录用户相关信息：

- 显示当前 `username`
- 提供 `Logout` 按钮

要求：

- 保持当前 top bar 的薄型 monochrome 结构
- 不引入复杂菜单
- 第一版直接用按钮即可

## 9. 页面跳转与会话流转

## 9.1 首次访问

### 未登录

- 访问 `/`
- 根路由按既有规则进入受保护区域
- `ProtectedRoute` 发现匿名态
- 重定向到 `/login`

### 已登录且本地会话有效

- 访问 `/` 或任意受保护页面
- `restoreSession()` 恢复成功
- 正常渲染目标页面

## 9.2 主动登录

- 用户在 `/login` 提交用户名和密码
- 登录成功
- store 更新为 `authenticated`
- 跳转到 `/dashboard`

## 9.3 主动退出

- 用户点击 `Topbar` 中的 `Logout`
- store 清空
- `localStorage` 清空
- 跳转到 `/login`

## 9.4 被动失效

- 用户已在任意受保护页面
- 某次 manager API 返回 `401`
- 前端自动清空会话
- 返回 `/login`

## 10. 目录结构建议

本次新增或调整后的前端目录建议如下：

```text
web/src/
  app/
    providers.tsx
    router.tsx
    layout/
      app-shell.tsx
      topbar.tsx
  auth/
    auth-store.ts
    protected-route.tsx
    auth-types.ts
  lib/
    env.ts
    manager-api.ts
  pages/
    login/
      page.tsx
```

说明：

- `auth/` 负责认证领域的类型、store 和路由守卫
- `lib/manager-api.ts` 负责 manager HTTP 请求，不让 API 细节散进页面
- `Topbar` 只消费 `useAuthStore()` 暴露的用户状态和 `logout()` 动作

如果实现时发现单独抽 `auth-types.ts` 没有明显收益，也可以并回 `auth-store.ts`，但认证领域边界应保持独立。

## 11. 测试设计

本次改动至少需要覆盖以下前端单元测试：

### 11.1 路由鉴权

- 未登录访问 `/dashboard` 时跳转到 `/login`
- 已登录访问 `/login` 时跳转到 `/dashboard`
- 会话恢复尚未完成时，不应错误跳转

### 11.2 登录页

- 成功提交后写入 store 并跳转到 `/dashboard`
- `401` 返回时显示错误文案
- 提交中按钮处于禁用或 loading 状态

### 11.3 `Topbar`

- 已登录时显示当前用户名
- 点击 `Logout` 后清空登录态并跳转 `/login`

### 11.4 `manager-api`

- 存在 token 时自动附带 `Authorization` 头
- 返回 `401` 时触发 `handleUnauthorized()`
- `VITE_API_BASE_URL` 配置存在时正确拼接请求地址

### 11.5 会话恢复

- 本地存在未过期会话时能恢复成功
- 本地存在已过期会话时会被清理并回到匿名态

## 12. 风险与取舍

## 12.1 不立即做权限裁剪

风险：

- 登录后仍可能看到当前用户没有权限访问的导航入口

当前取舍：

- 第一版只做登录闭环
- 真实权限约束仍以后端 `403` 为准
- 后续如需要，可基于当前已保存的 `permissions` 再做导航或页面元素裁剪

## 12.2 不做 refresh token

风险：

- 会话到期后需要重新登录

当前取舍：

- manager 当前只有 `POST /manager/login`
- 第一版先保证失效行为清晰、自动回退一致
- 不引入前端伪刷新逻辑

## 12.3 401 回退依赖全局跳转能力

风险：

- `manager-api` 在 React 组件外触发登出时，需要可用的导航回退路径

当前取舍：

- 统一由认证 store 持有“清空状态”能力
- 跳转逻辑通过受保护路由状态变化自然生效，避免在 API 层直接深耦合 router 实例
- 如实现中仍需显式导航，应以最小侵入方式封装，不让 API 层直接散落路由依赖

## 13. 最终方案摘要

本次设计最终确定为：

1. 在 `web/` 中新增独立 `/login` 页面
2. 使用 `Zustand` 作为认证状态管理框架
3. 使用 `localStorage` 持久化 `username`、`accessToken`、`expiresAt`、`permissions`
4. 用统一 `ProtectedRoute` 保护现有管理页面
5. 用统一 `manager-api` 处理基础 URL、Bearer Token 和 `401` 自动登出
6. 在 `Topbar` 增加用户名展示与 `Logout` 入口
7. 默认同源访问 manager API，同时支持 `VITE_API_BASE_URL`
8. 第一版只实现认证闭环，不扩展到权限裁剪和其他真实业务接口

这套方案与当前 `web/` 的 SPA 壳子、manager 后端 JWT 登录接口，以及用户已确认的交互边界保持一致，并为后续接入 nodes、slots、overview 等真实管理数据留下了稳定扩展点。
