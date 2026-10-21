# systemd Job compatibility repair

本次 run33 的启动故障已明确定位为脚本兼容性错误：原始 before-kill 收据证明 start/state 命令均成功、服务 active/running，只有明确存在的空 `Job=` 被 e302 的 Bash 5 classifier 拒绝（exit 1）。观测距离 readiness_started 仅 7 秒，未到 2 小时 deadline；这是误判后停止服务，不是产品 runtime 失败或验收时长超限。该结论不外推证据不足的 run32。

原始 public diagnostic/unit 的独立 SHA 验证与重建在 `run33-original/`。104 字节原观测（无尾换行）SHA 为 `57c3e320a832417c5e803553961f261771af23813115c207218162f214488524`；实际 stage 的 printf+sed 将 classifier 输入恢复成两行、以换行结束。此前 post-kill Linux 单变量重放保留在 source-freeze 引用目录；本目录 local-state-replay 另记录本机 Bash 3/5 差异。

依据 systemd v255 的 [Unit.Job 契约](https://www.freedesktop.org/software/systemd/man/255/org.freedesktop.systemd1.html)、[show renderer](https://github.com/systemd/systemd/blob/v255/src/systemctl/systemctl-show.c#L1112-L1122) 与 [property empty handling](https://github.com/systemd/systemd/blob/v255/src/systemctl/systemctl.c#L616-L617)，无排队 job 的 ID 为 0，systemctl 可输出空字段。官方源码完整下载摘要及精确行摘录见 official-systemd.json。

修改限于六文件：两个现有 parser、一个新默认 parser CLI 测试、既有 integration 测试、CHANGELOG、PROJECT_KNOWLEDGE。明确存在的空 Job 统一转换为整数 0，保留 raw empty digest；字段存在性独立判断，显式拒绝缺失/重复/畸形，非空 Job 按有限十进制 uint32 处理。最后无换行的字段不能被静默忽略。running/pending/terminal 状态策略保持原样，未修改 stage orchestrator、重试/续跑、预算、云权限、工作流或保护控制。

所有 shipping tests 在 parser 改动前一次性写完并冻结，最终 tests 字节与红灯完全一致：

| 证据 | Bash 3.2.57 | Bash 5.2.21 |
| --- | --- | --- |
| 原代码 parser 26 子例 | 15 失败 | 17 失败 |
| 原代码 actual loop 11 分支 | 3 失败 | 3 失败 |
| 修复后 parser 26 子例 | 全通过 | 全通过 |
| 修复后 actual loop 11 分支 | 全通过 | 全通过 |
| 全部相关回归 | 9 根 / 74 pass 事件 / 4 skip | 9 根 / 74 pass 事件 / 4 skip |

4 个 skip 是现有 Darwin journal cursor 正则 `{1,512}` 不受支持，包含新增 inactive-terminal 分支的同一 summary 子检查；不得算作 Linux 覆盖。重点原观测现进入已有 deadline 分支，inactive-empty 保留 terminal，缺失/重复仍 exit 2，并核对 before-kill 收据与 cleanup。测试只使用本地假 SSH/命令适配，不连接主机或执行工作负载。

`flow-doc-contracts` 按冻结 policy 的命令、工作目录、timeout 和输出限执行通过；两 shell syntax、Python 内存 compile、改动 Go 文件格式和 diffcheck 均通过。最初 static harness 使用环境 C.UTF-8，Bash5 输出本机 locale warning 使空 stderr 断言失败；保留原错误说明与同环境逐命令重放（所有命令 exit0），改为测试一直使用的 LC_ALL=C 后通过，未改 shipping source。

最终实现：`final-freeze/manifest.json`、`final-freeze/review.patch`、`final-freeze/files/`。没有 commit/push/CI dispatch、云调用、SSH、采购、续跑或现存 request 修改。完整 repository-default/scripts-integration 与 Linux CI 由后续交付执行；本地修复不证明云 rehearsal 或 formal soak 成功。代码已冻结，等待独立审查。
