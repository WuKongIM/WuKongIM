# 权限读取有界等待诊断

基于 `2996d4bf3`。真实三节点、25 频道、4500 SEND/s、128 个 Store append
worker 的 60 秒目标在 7.79 秒失败；3 次 permission admission busy，而
transport RPC 错误为零。完整 5000 频道同样失败，原始证据不替换。

## 修改前失败清单

- 16 个执行者已占满时，第 17 个短暂突发请求被立即拒绝。
- 等待期间的新禁令必须在取得许可后通过 fresh barrier/snapshot 观察到。
- 最多 16 个执行者及 16 个等待者；第 33 个请求在解码前返回 typed busy。
- 等待最多 100 ms，或更早的调用方取消/截止时间；结束后不得泄漏等待位置。
- 同一预算适用于本机/远端；取消一个等待者不得取消其他等待者。
- 即使释放许可与取消并发，也不能泄漏许可、执行超过 16 或返回旧权限。
- 未进入执行者预算前，远端原始报文不能解码；超长报文仍在等待前拒绝。
- 正常无竞争读取不创建计时器；请求大小、四 worker、路由重试边界不变。

## 候选与可证伪假设

1. 如果主要是瞬时尖峰，16 个有界等待位置应吸收突发，原失败重放可通过。
2. 如果服务持续供不应求，等待只会推迟 busy 并提高 P99，不能算容量修复。
3. 如果 GC/调度暂停主导，CPU/GC/尾延迟仍需独立评估，不能仅看零拒绝。

执行并发保持 16。增加的等待预算是 16 个信封、每个最多 1 MiB 线上报文；
这不是完整 Go heap 或整个 transport 队列的内存承诺。等待上限 100 ms
占 SENDACK 1 秒 P99 目标的十分之一，超时仍 busy，不增加重试或租约。
此变更是显式容量设计修订，必须重新验证原压力场景，不能通过扩大 fixture
队列/更换配置把旧失败冒充通过。128 个 Store worker 仍仅用于原已记录
诊断对照；正式 fixture 保留 8，正式 5000/4500/30 分钟门槛不变。

## 实现与边界验证

Store 共享有界执行信号量，只有发生竞争才建立等待计时器；等待人数用独立
计数限制。成功取得执行许可后再次检查调用方取消，取消竞争归还许可。
远端先校验原始字节上限、再等待、最后解码；本机使用同一 gate。所有权
fence、fresh ReadIndex、维护准入、snapshot 和局部 stale-route 重试不变。

新增 integration 失败用例先因缺少等待契约编译失败，随后实现；原真实
进程 busy 重放是行为失败基线。新的等待期间写禁令、16 执行/16 等待满载、
解码前拒绝、独立取消、等待位置/许可复用及 100 ms/调用方超时测试通过。
既有完整边界套件一起通过（1.99 秒）；定向 race 通过（3.61 秒），完整
proxy 单元套件通过（8.02 秒），vet 通过。Mac race 链接器仍有既有
LC_DYSYMTAB 警告，测试成功。

## 25 频道重放

同配置、同目标 60 秒重放完成 270,000 条消息并全部排空，busy 为 0，
SENDACK/RECV P99 为 118/132 ms。原场景 7.79 秒 busy 失败已不再复现于
该次窗口，但**该次测试仍失败**：分配总量 231,740,770,200 字节，超过
原 99,600,000,000 字节上限；GC 3112 次。没有改变分配/GC 门槛，没有
把收发成功称为性能资格通过。后续 profile 用于归因，不作为资格成绩。

## 完整规模短诊断与功能回归

5000 频道、4500 SEND/s、60 秒、128 个 Store append worker 的原诊断配置
重放通过（总 291.80 秒，包含准备）。270,000 条消息全部完成、pending=0、
permission admission busy=0；SENDACK/RECV P99 为 189/175 ms。分配总量
51,968,589,152 字节、GC 369 次、三节点平均 CPU 合计 414.11%（100% 为
一核），均保留完整原门槛。该结果与修改前同配置的亚秒 busy 失败对应，
支持短时尖峰假设。它不代表原 8-worker 配置或 30 分钟资格已经通过，也
不替代三组各三次的 P99/CPU 5% 对照。

同一二进制的单节点/三节点禁发主流程通过，总 84.54 秒（20.33/64.21 秒），
含跨入口、两个既有设备、类型 3–12、立即解禁、精确历史和 RPC 聚合证据。

## 热点频道分配归因

25 频道的独立 profile 重放再次完成 270,000 条消息、零 busy/pending，
但分配量 238,334,865,256 字节仍超标。三个进程在负载开始约 7 秒时获取
heap profile，合并 alloc_space 按工具显示为 11.58 GB，其中 8.21 GB（70.89%）
归于复制层 `peerBatcher.takeBatch`。这是各进程累计采样，不等于整个
60 秒测量窗口的精确归因，不能与 counter 差值混用。

源码中 `takeBatch` 在发现所有频道已有 in-flight 交换之前分配切片/阻塞
集合；空 batch 返回后 `finishTargetWorker` 仍可能按非空队列重新调度。
这构成空转分配的具体假设，尚须先建立确定性失败测试再修改复制层。
本次不修改复制协议、持久化或交换调度；保留热点失败及原始 profiles。

## 重放与证据

```sh
GOWORK=off go test ./pkg/slot/proxy -count=1
GOWORK=off go test -race -tags=integration ./pkg/slot/proxy \
  -run '^TestSendPermission(BoundedWait|RPCBoundaries|AdmissionPrecedesDecode)$' -count=1
WK_E2E_BINARY=/path/to/recorded/wukongim GOWORK=off go test -tags=e2e \
  ./test/e2e/message/send_ban -run '^TestUserAndChannelSendBan$' \
  -count=1 -timeout=5m -p=1 -v
```

压力复放脚本、临时配置差异及自动恢复、JSON、日志、profiles、源码和二进制
SHA 见[产物清单](assets/send-ban-admission-wait-20260924/manifest.json)。所有
临时 Store worker 修改均已恢复为 8；无权限 TTL、跨请求旧事实复用或重试到
成功。命名 `flow-doc-contracts` 首次因索引过期失败，重新生成索引后通过；
81 个 FLOW 合规、0 invalid，保留 10 个既有建议行数警告。
