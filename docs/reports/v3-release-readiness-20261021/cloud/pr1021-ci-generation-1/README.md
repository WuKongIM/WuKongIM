# PR #1021 — 首轮 Linux CI 归档

精确 head `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`，base `e30299fa59ea6669ea71024eabe209840f241f19`。真实 checkout 为 synthetic merge `8cdee508fd2b5b5383e64a201e033d3ffef37ba0`；API Git 对象证明两者 tree 均为 `74bf373cd2e8cbfd9c64834af3344f0fab36d05d`。9 个执行 checkout 的 job 日志均记录该精确 SHA；signal 与汇总 job 无 checkout。

3 个自然触发的 workflow、11 个执行 job 全部 success，均 attempt 1：

- [Safety Automation - Issue Agent PR Signal](https://github.com/WuKongIM/WuKongIM/actions/runs/39614359600) — success，最后更新 2026-10-21T14:09:38Z。
- [V3 Candidate Regression pull_request 1021 attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39614359934) — success，最后更新 2026-10-21T14:28:27Z。
- [Three-Node Chat Lifecycle Regression pull_request 1021 attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39614360432) — success，最后更新 2026-10-21T14:15:08Z。

## 已核实际结果

- 默认 `go-unit`：219 个有测试包通过，15 个无测试包；未截断输出。
- Linux `scripts-integration`：634 个顶层测试通过、1194 个 pass 事件、6 个既有 opt-in skip。耗时 573.654 秒，原 policy 的 600 秒外层、Go 9 分钟和输出上限未变。
- 本次 26 个 parser 子例、11 个实际 pre-clock loop 分支全部通过；本机 Darwin 的 4 个 journal summary skip 在 Linux 全部实际通过，无新增用例 skip。完整事件在 `linux-systemd-job-tests.json`。
- 同一冻结 server binary（SHA256 `4b6a048631d774b93bd8262febf376d010c9113d64b2a26a27a463ffb464f6b6`）：迁移 1→1、1→3、3→1、3→3、3→5 五拓扑通过；跨节点消息四个根测试通过；自聊 1/3 节点、生产默认认证及整群重启通过。原 2,000 QPS replica comparison 为 opt-in，未启用且明确 skip。
- default、scripts、build、migration、cross-node、self-chat 共 6 份 wrapper 收据均 exit0、cleanup verified/complete、remaining_processes=[]。
- PR messaging unit/race、baseline evidence、三节点 correctness 与三项 500QPS profile 均实际执行成功。三节点 2,500 连接的测量窗口为 90 秒，local-step clean；测量发送增量45,500，含暖机/终结的 final 发送和ACK均75,502，loss/duplicate/corruption/sequence regression/first-attempt failure/harness failure为0，终结队列和outstanding为0。此为短 PR smoke，不是2小时预演。

| 500QPS seam | 测量窗口 | 完成 / 错误 / 丢弃 | 三个分钟窗口 p99 (ms) |
| --- | --- | --- | --- |
| tcp-sendack | 180秒 | 90000 / 0 / 0 | 178.195 / 178.244 / 178.197 |
| channel-append | 180秒 | 90000 / 0 / 0 | 6.803 / 8.065 / 10.748 |
| mixed-send | 180秒 | 90000 / 0 / 0 | 15.405 / 17.510 / 18.916 |

## 未执行与读取边界

- SDK released acceptance、native preview、Manager browser workflow 的 PR path filter 不匹配本次6文件，未触发；不能将旧 head 结果算成本轮通过。Nightly direct qualification 与 fixed diagnosis 两个 job 按事件条件 skip。scripts 中 Docker/native/指定 readiness fixture 的6个既有 opt-in skip列在 `evidence-analysis.json`，未掩盖或补跑。
- E2E全部步骤和 artifact 于14:22:25Z结束，但 run/job/check-run曾短暂保持in_progress。只读 direct/no-cache GET和已可下载的完整job log均保留；API最终于14:28:27Z更新success。未用步骤完成冒充workflow终态。
- 6 个小 artifact完整下载后独立核API声明SHA256、长度与ZIP CRC。229MB三节点包只读了ZIP目录及6份关键public报告，单个成员经zipfile CRC校验；整包SHA未重算，API digest仅作为声明。两次有界Range GET出现URLError并保留，kernel/filesystem/graceful-stop附加元数据未取到；完整job log、local-step和final报告已保留。未下载其产品二进制或数据库。
- 所有11个执行job完整日志已保留公开脱敏版本与原始哈希，原始日志和可能含临时signed URL的stderr仅在相邻0700 private目录、文件0600，不进入allowlist。
- 没有CI失败、rerun、取消、手动dispatch、评论、ready、merge或云动作。最后公开PR读回为 open / draft=true；是否转ready/合并由root另行处理，当前未收到合并批准。
- 本CI不证明云2h rehearsal、72h soak/容量/恢复或正式native stable发布完成。

## 归档入口

- `terminal-ci-matrix.json`：最终run/job/step与实际checkout身份。
- `evidence-analysis.json`：数值、6份cleanup和明确skip。
- `linux-systemd-job-tests.json`：26+11+4逐项Linux证据。
- `artifacts-public/12352134732/members/self_chat/report.json`：真实自聊/重启/二进制身份。
- `ARCHIVE_ALLOWLIST.txt` / `SHA256SUMS` / `archive-sha256.json`：唯一公共归档清单。
