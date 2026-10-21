# CDN refresh 聚合任务合同：有限只读诊断

**已证实提交侧有聚合；查询侧的实际聚合明细仍未知。** packages provider 的一个 URL 对应一个 TaskId 假设不成立；当前严格拒绝不能作为可继续生产的正向通过。

## 直接证据

- WuKongIM exact `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06` 的 `scripts/docs-cdn/refresh.sh:25–35` 一次以 File 提交 docs.githubim.com 的 `/`、`/zh/`、`/en/`、`/api/search` 四条 URL。
- 原公开收据 `cloud/pr1024-main-first-ci/job-119476699413.log:202`：`2026-10-21T20:47:47.7392599Z`，`task_id=33643947491`，`urls=4`。run39672950808/attempt1、job119476699413，源79a。任务及请求ID来自真实 CLI响应的窄输出；没有新调用。这证明该批四URL的提交返回一个任务ID，不能从中推导查询返回结构。
- 该 helper只提取提交响应ID并打印 accepted，没有调用任何 Describe 接口，也没有保留原响应完整 JSON。其 workflow success 表示这一步成功受理；它没有证明 CDN 全网完成、每URL覆盖或 packages 域名生产可用。
- 对原有限 public allowlist 的35个payload逐份复验尺寸/SHA全部匹配。仅有上述 accepted 行命中采证扫描；没有聚合查询响应。未读取原私有日志、凭据或 Chrome。

官方 RefreshObjectCaches 合同明确同域同秒的 URL 会合并任务，超过2000按组拆分；请求 ObjectPath 的换行分隔规则属于提交输入，不能擅自套用到查询返回字段。[官方提交合同](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-refreshobjectcaches)

## 源码缺口

packages固定 `1fb1c6ea077fd260c373434c1a3c368a7952defd`：

- `scripts/aliyun-object-storage-provider.py:414` 强制 IDs数量等于URLs数量，会拒绝官方允许的聚合提交。
- `:421–424` 继续强制每ID一条row，并把TaskId映射到单个ObjectPath。即使只去掉第一处数量检查，这个映射仍不能证明聚合成员完整覆盖。
- `tests/provider_sdk/test_aliyun_object_storage_provider.py:121–135,385–391` 的fake响应人为给每URL分配不同ID，现有11URL正例恰好返回11ID。这些测试的通过无法排除真实聚合兼容性问题；本次未运行SDK或测试。
- `refresh()` 在413行已先提交刷新，414行才可能拒绝。因此拒绝意味着未能认定操作完整成功，**不意味着远端无副作用**。不能据此直接重提同一请求或宣称生产安全验收通过。

这是一项已证实的响应合同不兼容及其覆盖证据缺口，未执行 packages真实调用，也未实现修复。docs的四路径与packages的canonical-file输入限制不同；docs记录用于证实同一API的提交基数，不能充当packages刷新验收。

## 查询边界与可行的下一步

DescribeRefreshTaskById声明最多10个逗号分隔ID，响应有Tasks数组、TotalCount及每row的字符串TaskId/ObjectPath和状态；没有定义聚合时row数、ObjectPath内部编码或成员截断规则。不能猜测为换行、逗号列表或重复ID行。[官方按ID查询](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtaskbyid)

另一个DescribeRefreshTasks支持同时以TaskId和准确ObjectPath过滤，并有分页及总数；这提供了一个候选成员核验方式，仍缺该真实聚合任务的响应，尚未验证有效。其调用/权限与当前ById入口不同，本次没有切换或扩权。[官方分页查询](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtasks)

如后续明确授权读取现存任务，应优先绑定这次已知的ID和四条URL，保留脱敏的任务结构、字段类型、状态、总数与分页结果；只查询已有任务，无需为诊断新增刷新。两个查询接口仅保留近3天记录，是否还能读取由实际请求时点决定，本次未尝试。

实现前需要证明：提交集合与返回任务的关联；同ID多成员表达方式；完整URL覆盖且无缺项/截断；同域同秒跨请求合并时的额外成员处理；每目标URL的类型和终态；失败/取消/超时/未完成以及轮询中映射漂移。固定轮询、返回字节与页数预算也须保持有界。只接受task Complete而放弃URL覆盖，或靠睡眠拆批假设“一秒一个任务”，均不能填补此证据缺口。

## 方法与交付边界

使用 diagnosing-bugs 的有界证据路径：三个可证伪假设见 `hypotheses-and-outcome.json`。没有运行复现loop，因为本轮明确禁止SDK测试和新的云请求；不宣称已复现packages生产故障或已验证修复。源码与规则digest在 `source-and-rules-freeze.json`，公共日志证据在 `public-evidence.json`，官方页面仅作有限摘要列于 `official-contracts.json`。所有输出仅写入本新目录，无源码、历史档案、CDN、权限或CI修改。
