# 复制空批次调度与热点分配

基于 `f35c2a2ca`。上轮 25 频道、4500 SEND/s、60 秒、128 Store worker
完成 270,000 条消息但分配量超标；profile 的 70.89% 累计采样分配归于
`peerBatcher.takeBatch`。5000 频道短诊断通过，持续资格尚未通过。

## 修改前失败清单

1. 在途 Channel 后续工作不能启动无效 owner；没有新可执行项时，空 owner 退出不能重新排自己。
2. 暂时不可执行的同频道队列不能阻止另一个独立 Channel 立即取得空闲 flight。
3. 当前交换结束后，须唤醒被同 Channel 阻挡的其他工作类别；不能丢失唤醒。
4. queue head 的 exchange kind 及同频道先前其他 kind 仍构成顺序屏障。
5. 全部工作已在途时，takeBatch 返回空且不分配 batch 切片。
6. 全局/每目标数量与字节、执行并发、前台/后台预留、取消后的持久化归属不变。
7. callback、ownership、批量/请求字节守恒及独立频道正常完成仍由既有回归覆盖。

## 假设与验证边界

- 空批次自重排导致分配：阻止无可执行项的调度应消除确定性重排和实际分配热点。
- mixed-kind 屏障触发同类空转：readiness 必须与 takeBatch 选择语义一致。
- 正常复制分配才是主因：若修复上述空调度后实际分配不降低，则不能宣称解决。

使用手动 executor 和真实 batcher 建立无睡眠、确定性的失败测试；实际分配
是否改善仍须重放原 25/5000 频道进程场景，不以隔离测试替代持续资格。

## 失败复现与修复

四个测试修改前全部失败：空 owner 退出后 executor 仍有一个任务；被在途
前台频道阻挡的后台工作和 mixed-kind 屏障后工作均被错误调度；全阻挡的
空 batch 每次分配 3 次。修复后全部通过（0.91 秒），复制模块完整单元
套件通过（1.74 秒）、完整 race 通过（2.58 秒）、vet 通过。

调度和 takeBatch 共用 readiness 判定：遵循首个 exchange kind，同频道
较早的其他 kind 仍阻挡后项；全部在途时不建立 batch。释放交换占用后
重新调度被阻挡的类别，避免只停止空转却遗漏唤醒。并发、队列、字节上限、
副本、持久化与回调归属均保持不变。

## 热点重放

原 25 频道、4500 SEND/s、60 秒、128 Store worker 场景通过，270,000 条
消息全部完成、permission busy=0、pending=0。总分配从上一轮无 profile
重放的 231,740,770,200 降到 25,868,458,168 字节（减少 88.84%），
GC 从 3112 降到 378，三节点平均 CPU 合计从 652.98% 降到 269.45%。
这是一轮同形状诊断对比，不是三次重复的性能资格。

SENDACK/RECV P99 从 118/132 ms 升至 291/295 ms，仍满足原 1/2 秒门槛。
本次物理存储提交 P99 为 93.66 ms，leader commit request 为 211.51 ms；
只记录观测，不把尾延迟上升未经验证地归因于调度或宿主机。未调整门槛。

## 完整规模与消息顺序

5000 频道、4500 SEND/s、60 秒、128 Store worker 场景通过（总 294.68 秒，
含准备）。270,000 条消息全部完成，permission busy=0、pending=0；
SENDACK/RECV P99 为 206/192 ms，分配 51,561,385,560 字节，GC 363 次，
三节点平均 CPU 合计 407.08%。相较上一轮 189/175 ms，尾延迟仍有上升，
不据此声称 P99 改善或三次对照已满足 5% 门槛。

同一二进制的三节点私聊和群聊各 1200 条并发消息通过发送者顺序、接收序列
和 TCP 线上 ACK 顺序验证（27.91/27.18 秒）。全部复制模块单元/race/vet
与命名 FLOW 检查通过。所有临时 Store worker 设置恢复为 8；本次没有
改变正式 fixture、队列、worker 上限、复制数、存储语义或验收门槛。

## 重放与产物

```sh
GOWORK=off go test ./pkg/channel/replication -count=1
GOWORK=off go test -race ./pkg/channel/replication -count=1
WK_E2E_BINARY=/path/to/recorded/wukongim GOWORK=off go test -tags=e2e \
  ./test/e2e/message/chat_lifecycle \
  -run '^Test(Person|Group)ChannelCrossIngressBurstPreservesReceiveSequence$' \
  -count=1 -timeout=9m -p=1 -v
```

压力仍沿用上一轮同配置重放脚本。失败/成功日志、JSON、源码和二进制 SHA、
冻结上下文及自动恢复记录见[产物清单](assets/send-ban-peer-readiness-20260924/manifest.json)。
旧 8-worker 配置、30 分钟持续资格、三组各三次性能对照及原功能故障缺口
继续保留，不能把本次两个短窗口通过称为完整验收。
