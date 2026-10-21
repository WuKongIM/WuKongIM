# Signal-join fixture 修复独立复核

结论：**无阻塞发现，可交远端 Linux 完整门禁验证。** 这是只读源码和既有测试收据复核，未由 reviewer 重跑测试。未把本机绿色解释为 Linux 完整门禁通过。

范围固定：base `76a3ba23303e288345dbd37bc354b41d81b6f070`，作者最终补丁 SHA-256 `a9f82b05e0aedd40e0fce3e1ae0aeaf93cd92cbfbb16e653654b2c4ca8a07f58`，仅两个 integration test 文件。固定文件 SHA、测试日志摘要和受保护源码逐字对比见 `review.json`。复核者逐项核对最终 250ms→1s 差分，其余代码与初次审查冻结一致。

## Standards

0 项发现。真实进程、计时、信号控制留在 `integration` tier；无生产逻辑/门禁参数改动。先有失败库存及 7 个 helper control 红例，再实现 operator，作者保留红/初绿/最终小幅期限调整后的两版本绿证据。新 helper 注释说明进程所有权和回收责任；两个 fixture 共用该 helper。根 AGENTS 与 FLOW 发现已核对；scripts 根无 applicable FLOW，cloud-deployment 的 package/subtree 导航不适用这两个文件。

code-review skill 的标准/规格两轴由本独立 reviewer 直接完成；团队四个 slots 已用于实现/独审/主任务，未另开重复审查代理。此安排不改变审核或保护范围。

## Spec

0 项发现。此前 Linux FD3 证据准确定位为：production join 先收到 TERM 返回 143，继续 wait 后为 0，PID 清空；其后 fixture 自己的第二个 `wait signaler` 得到 -1 并表现为 exit 255。修复删除这个 fixture-only background job，改由 Go 在观察到 parent/child ready 后等待原 50ms，再向同一个 parent 发送一次 TERM；不修改、归一化或吞掉 production wait 的返回码。原 observer 255 是否同因仍未经独立定位。

检查点：

- `Setpgid` 创建独立 fixture 组；ready 要求 parent 等于本次 Start 的 PID，child 为合法不同 PID，发信号前 Getpgid 必须属于本次组。trap 额外证明收到 TERM 时 child 尚活着。
- 成功、缺失/坏 ready、早退、deadline、发信号失败各路径在 Start 后均经 defer：只杀本次组，再等待本次 shell。默认取消也杀同一组；500ms WaitDelay 避免退出后管道长期占用。成功 shell 若仍留组内后代，cleanup 使结果失败。
- Wait 只由一个 goroutine 调用；defer 等其结束后才读取输出。保留 waitErr 并 errors.Join，原 exit 17/255 仍可通过 errors.As 读取。早退 0 不是成功。原 child-dead、PID-clear、status 和 signal-seen 断言保留；TERM 与 signal-child-alive 增强了覆盖。
- trace 用 FD3，不混入用于断言的 stdout；仅失败读取且限 64KiB。两个真实 fixture 各 3 次，首次失败停止；每次仍 5s，原 target 0.4s / observer 0.35s 等待保持。调整仅把 7 个新负向 helper controls 的启动+执行总限额由 250ms 设为 1s，给共享 runner 调度留余量；完整门禁和真实 fixture 的时序不变。
- 作者负向 controls 覆盖 early 0、17、255、missing/malformed/wrong-parent ready、ready 后 deadline，并检查 child 不继续运行及 unrelated sentinel 存活。清理测试允许 OS 尚未回收的 zombie，这不被描述为已从进程表消失。

已读收据：初版 helper 7/7 红；初版 actual Bash 3.2 和自建 Bash 5.2.21 聚焦各 3 顶层/13 子例通过，Bash5 count=5 额外 15 顶层/65 子例通过。最终仅 1s deadline/comment 调整后，Bash3 和 Bash5 各 3 顶层/13 子例再次通过，无 skip/fail，wall 分别 5.965s / 4.904s。原始 command JSON 绑定 GOWORK/GOENV/GOTOOLCHAIN/GOFLAGS/PATH，最终源码 hash 与作者 freeze 一致。

未声称：Ubuntu 的具体 Bash patch 根因已证明、历史 observer 255 与本次必为同因、完整 Linux scripts gate 已通过、v3 已可正式发布。本修复可以进入新提交的正常 CI；不能重跑旧失败 head 取代证据。
