# wkdb Pebble MVP Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在根目录新增 `wkdb` 包，基于 Pebble 实现 `user` 和 `channel` 两张静态表的最小 CRUD MVP。

**Architecture:** 使用“描述驱动的编码内核 + 类型化 API”。底层统一维护静态 catalog、主记录和索引 key 编码、family value 编码与校验；对外只暴露 `user/channel` 的类型化 CRUD 方法，不暴露通用表引擎接口。

**Tech Stack:** Go 1.23, Pebble (`github.com/cockroachdb/pebble`), Go testing

---

### Task 1: 建立包骨架和错误契约

**Files:**
- Create: `wkdb/errors.go`
- Create: `wkdb/db.go`
- Test: `wkdb/user_test.go`

- [ ] **Step 1: Write the failing test**

编写最小打开/关闭数据库和 `ErrNotFound/ErrAlreadyExists` 行为相关测试，先让编译失败。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wkdb -run TestOpenClose -count=1`
Expected: FAIL with missing package or missing symbols

- [ ] **Step 3: Write minimal implementation**

新增 `wkdb` 包、`DB` 类型、`Open/Close` 和基础错误定义。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wkdb -run TestOpenClose -count=1`
Expected: PASS

### Task 2: 实现静态 catalog 和基础 codec

**Files:**
- Create: `wkdb/catalog.go`
- Create: `wkdb/codec.go`
- Test: `wkdb/codec_test.go`

- [ ] **Step 1: Write the failing test**

编写 key/value 编码测试，覆盖：
- `user` 主记录 key
- `channel` 主记录 key
- `channel` 二级索引 key
- `user/channel` 文档里的 value 示例

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wkdb -run 'Test(User|Channel|Codec)' -count=1`
Expected: FAIL with missing descriptors or codec helpers

- [ ] **Step 3: Write minimal implementation**

实现：
- 静态 ID 和表描述
- string/int64 key 编码
- family payload 编码/解码
- 外层 checksum/tag 包装与校验

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wkdb -run 'Test(User|Channel|Codec)' -count=1`
Expected: PASS

### Task 3: 实现 user CRUD

**Files:**
- Create: `wkdb/user.go`
- Modify: `wkdb/db.go`
- Test: `wkdb/user_test.go`

- [ ] **Step 1: Write the failing test**

编写 `user` CRUD 测试，覆盖：
- create/get/update/delete
- duplicate create
- get missing

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wkdb -run TestUserCRUD -count=1`
Expected: FAIL with missing methods or wrong behavior

- [ ] **Step 3: Write minimal implementation**

实现：
- `User` 类型
- `CreateUser/GetUser/UpdateUser/DeleteUser`
- 主键存在性检查
- family 0 编码和解码

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wkdb -run TestUserCRUD -count=1`
Expected: PASS

### Task 4: 实现 channel CRUD 和索引查询

**Files:**
- Create: `wkdb/channel.go`
- Modify: `wkdb/db.go`
- Test: `wkdb/channel_test.go`

- [ ] **Step 1: Write the failing test**

编写 `channel` 测试，覆盖：
- create/get/update/delete
- duplicate create
- `ListChannelsByChannelID`
- delete 后索引被移除

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./wkdb -run TestChannelCRUD -count=1`
Expected: FAIL with missing methods or missing index maintenance

- [ ] **Step 3: Write minimal implementation**

实现：
- `Channel` 类型
- `CreateChannel/GetChannel/ListChannelsByChannelID/UpdateChannel/DeleteChannel`
- 二级索引写入、扫描、删除
- 回表读取

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./wkdb -run TestChannelCRUD -count=1`
Expected: PASS

### Task 5: 全量回归和清理

**Files:**
- Modify: `wkdb/*.go`
- Test: `wkdb/*.go`

- [ ] **Step 1: Run focused package tests**

Run: `go test ./wkdb -count=1`
Expected: PASS

- [ ] **Step 2: Refactor minimally if needed**

只做不改变行为的命名、辅助函数抽取、错误路径清理。

- [ ] **Step 3: Run full verification again**

Run: `go test ./... -count=1`
Expected: PASS or only existing unrelated failures surfaced clearly

- [ ] **Step 4: Re-check plan requirements**

逐项确认：
- `user/channel` CRUD 已实现
- `ListChannelsByChannelID` 已实现
- 文档 key/value 编码样例已覆盖
- 错误契约已满足

- [ ] **Step 5: Commit**

```bash
git add wkdb docs/superpowers/specs/2026-03-26-wkdb-pebble-mvp-design.md docs/superpowers/plans/2026-03-26-wkdb-pebble-mvp.md
git commit -m "feat: add wkdb pebble mvp"
```
