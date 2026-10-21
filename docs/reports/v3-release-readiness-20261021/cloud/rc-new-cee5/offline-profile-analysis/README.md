这 6 份 profile 不支持“失败后仍有投递/在线查询工作滞留”的判断，但无法排除失败前的短暂阻塞。采样于 17:19:15–17:19:58Z，距 17:06:50Z 失败约 12–13 分钟。

| 服务 | goroutine 总数 | inuse_space | 累计 alloc_space |
|---|---:|---:|---:|
| 1 | 3287 | 504.64 MB | 53.84 GB |
| 2 | 3366 | 509.23 MB | 52.48 GB |
| 3 | 3230 | 500.70 MB | 53.13 GB |

每台均为 320 个 delivery worker 等待 dequeue、1 个生命周期等待者；presence 匹配为 tick/watch 两个维护循环，未见 processPlan 或 presence lookup 执行栈。源码 dequeue 无可运行项时进入 select（plan_queue.go:142–151），不是证明积压的栈。

三台最大分配点均为 Pebble manual.New：live heap 约 54–55%，累计分配约 64–65%。分布相近，缺少基线，不能称异常；累计值包括启动、预热及失败后的活动，不能除以 61.212 秒推算速率。heap 也不是 RSS。

无 CPU profile、等待时长或负载机 profile，不能唯一归因 1 条过期消息。未运行负载、访问云或修改产品。公开材料仅脱敏 top、聚合及来源 SHA；原始 profile/labels 不纳入清单。
