# 第二代候选 CI 冻结审计（2026-10-21 UTC）

七条运行均为 attempt 1 且成功；没有取消、重试或自动 rerun。第一代原始失败保持在 `../final/`。本轮消除了第一代默认测试红灯，并补齐此前 skipped 的真实 Linux 迁移/消息 E2E；它仍是候选验证，不代表正式发布已经完成。

`head=7e9da7df100d193330b3d509cfdbe079ea32a6f8`；PR synthetic merge 为 `305bbdc63fcdb210c3a8bd0670147fb6c6d2c913`，父提交精确为 b469 + 7e9，两者 tree 都是 `753ddfc8c7813bf0b7a7ec325cb4f81c5a36d4db`。PR 运行测试 merge，手动资格/矩阵测试 head；源码树相同。

| 范围 | 精确运行 | 真实结果 |
| --- | --- | --- |
| 完整默认与迁移/消息 E2E | [39548558671](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558671) | go-unit 与后续 E2E 两 job 均通过；219 个包 PASS + 15 no-test |
| PR 三节点/单元/race/性能 | [39548558799](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558799) | unit/race、correctness、三个 fresh 500-QPS seam、aggregate 全通过；未复用 baseline |
| 已发布 EasySDK | [39548558085](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558085) | Android Maven 1.0.5 / iOS CocoaPods 1.1.1 / Flutter pub 1.1.0 实际双向消息全部 PASS |
| Manager Chromium | [39548558122](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558122) | 生产 bundle Chromium 19.39s；TERM-ignore descendant cleanup 测试也通过 |
| native preview | [39548558092](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558092) | package/CLI、真实 SEND、临时签名闭环、四发行版安装与真实 systemd lifecycle 全通过 |
| 10 分钟资格 | [39548576246](https://github.com/WuKongIM/WuKongIM/actions/runs/39548576246) | 600 秒实际测量 300,504 sent=ACK / 500.84/s；2500 连接在线 |
| 完整 conversation Linux 矩阵 | [39548584710](https://github.com/WuKongIM/WuKongIM/actions/runs/39548584710) | 三窗 attribution 与完整 20 phases 均通过，原始 jq 验证在本地只读复核 true |

## 默认与真实 E2E

默认 job 119052226823：338.632s、exit 0、22,029 bytes、无截断。命名 go-unit 使用 base b469 的 policy，digest `2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9`；五个受控根、1800s/8MiB、GOWORK=off、GOFLAGS=-p=2。MQTT session 与 replication 包通过，第一代失败日志没有被覆盖。

E2E job 119054050479 一次构建并按顺序执行三个指定 E2E package 的默认测试：冻结 binary SHA256 `18f44a05979ac388ae736ae46b406886e9b7cb38a1f460b1ff8fe8d9a0781146`，build info vcs.revision=305bbdc、vcs.modified=false，每段前后 digest 检查通过。

| E2E 段 | 结果 |
| --- | --- |
| ordinary_history | 1→1、1→3、3→1、3→3、3→5 全通过；55 次 history verified（4/13/4/13/21）；临时 read retries 共 2，均为 1→3 整群重启后 |
| cross_node_delivery | 4 个默认功能测试通过；2,000-QPS opt-in 比较测试明确 skipped，不计入通过覆盖 |
| self_chat | 1/3-node cluster 均通过；生产默认 token、256 slots、无效 token 拒绝、各 6 ACK seq1..6、各整群重启 1 次及发新消息前读历史、各 4 次 sender suppression |

自聊各拓扑记录 16 次 transport receives，其中 8 次 duplicate 标记；fixture 核验重复投递身份且重新 ACK。这不是零重复投递承诺。自聊历史 1-node 3 次/3-node 9 次全完整、temporary retries=0。原始 identity/message/history 见 self_chat/report.json。

默认 + build + ordinary_history + cross_node_delivery + self_chat 共 5 份 Linux runner 收据均 exit 0、passed、cleanup.verified=true、complete=true、initial_processes=[]、remaining_processes=[]。该证据覆盖成功退出路径；不扩展为硬 SIGKILL、硬取消或 runner 丢失保证。

## 性能与十分钟资格

| 来源 | TCP / append / mixed 最大测量窗 scheduled-to-completion p99 (ms) |
| --- | --- |
| PR merge 305bbdc | 178.174177 / 8.181383 / 14.571429 |
| 手动 head 7e9 | 178.216478 / 8.233918 / 18.580578 |

每 seam 均是正式 measurement 的 3×60s、planned=completed 90,000、errors=dropped=0，60s warmup 单独保留。PR correctness 实际 90s 为 45,498 sent=ACK（expected45,000），clean/complete。

十分钟 local-step：600s、500.84/s、300,504 sent=ACK（expected300,000）、2500/2500 在线，queues 收敛，各类证据完整。full report 窗口 05:17:56.942522298Z—05:28:58.02843838Z，warmup_end05:18:56.942522298Z，合计 330,511 sent=ACK 含 warmup，不能作纯 600s 数量。retry/terminal/loss/duplicate/corruption/seq regression/receive-ACK failure/harness failure 均 0；latency warnings0；当前 queues 全0，cleanup_error_count0。hot mean20.929374ms、p99直方图桶上界200ms、max543.587224ms。正常 bounded stop 的 final verdict 为 operator_stop；local-step 明确 clean/complete、operator_interrupted=false。

## 固定 conversation 完整矩阵

product=harness=7e9 且 clean；Linux amd64 4CPU、node GOMAXPROCS2、driver4。binary `a16484cf66130eea1fef5471229501daf89a57ce44136f480ed06a5e88ba21f4` 在两报告与 binary.sha256 一致；profile SHA256 `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`。使用精确 7e9 validator + profile 本地只读复核 exit0/true。12 基础 phases + 7 stress windows/8 phases，共20；43,800 scheduled=completed，errors/drops/runtime loads/membership writes 全0；p99 最大85.935013ms，分配预算均符合原阈值。前置三窗 attribution 的最大 p99 为111.28ms，与矩阵统计分开。

| 场景 | 节点 | endpoint/page | offered → actual QPS | p99 ms |
| --- | --- | --- | --- | --- |
| basic | 1 | /conversation/list/25 | 400 → 400.000 | 3.751919 |
| basic | 1 | /conversation/list/100 | 200 → 200.000 | 8.272501 |
| basic | 1 | /conversation/list/200 | 100 → 100.000 | 12.823213 |
| basic | 1 | /conversation/sync/25 | 80 → 80.000 | 6.526472 |
| basic | 1 | /conversation/sync/100 | 60 → 60.000 | 14.938437 |
| basic | 1 | /conversation/sync/200 | 40 → 40.000 | 20.453403 |
| basic | 3 | /conversation/list/25 | 400 → 399.933 | 9.798571 |
| basic | 3 | /conversation/list/100 | 200 → 200.000 | 13.527689 |
| basic | 3 | /conversation/list/200 | 100 → 100.000 | 17.419295 |
| basic | 3 | /conversation/sync/25 | 80 → 80.000 | 13.884533 |
| basic | 3 | /conversation/sync/100 | 60 → 60.000 | 26.475670 |
| basic | 3 | /conversation/sync/200 | 40 → 39.933 | 33.586358 |
| mixed page 0 | 3 | /conversation/list/100 | 200 → 199.983 | 32.106654 |
| mixed page 0 | 3 | /conversation/sync/100 | 60 → 59.983 | 69.539938 |
| hidden_50_percent page 1 | 3 | /conversation/sync/100 | 20 → 20.000 | 31.739066 |
| hidden_50_percent page 2 | 3 | /conversation/sync/50 | 20 → 20.000 | 27.963510 |
| hidden_90_percent page 1 | 3 | /conversation/sync/100 | 20 → 20.000 | 24.360212 |
| hidden_90_percent page 2 | 3 | /conversation/sync/50 | 20 → 20.000 | 18.782884 |
| hidden_after_99_visible page 1 | 3 | /conversation/sync/100 | 20 → 19.933 | 81.121371 |
| hidden_after_99_visible page 2 | 3 | /conversation/sync/50 | 20 → 19.933 | 85.935013 |

## 证据边界与归档

Native AlmaLinux 慢步骤由原始日志解释：dnf AppStream 元数据 30MB/3:55，BaseOS48MB/10:03；依赖解析后安装/重装/移除几秒成功。归类为环境/仓库下载延迟，未产生产品或 harness 失败。进行中日志只请求一次，404 BlobNotFound 原样保留，之后终态日志已补齐。

小 artifact 整包 SHA256 已与 GitHub API digest 独立比对；大型 correctness/nightly/native/conversation 只取受限 ZIP member，验证 CRC，并保留完整成员 inventory、API 声明 archive digest 与选取记录，没有重算整包 hash。排除 ZIP/server/DEB/RPM/原始数据库。Conversation 10 个 profiles 共891,479 bytes已保留，未扩大性能分析。每个归档文件的 SHA256 见 archive-allowlist.json 和 archive-sha256.txt。

发布尚需单独证明：既定的 2 小时预演 → 新 Lease 上 72 小时 Soak → 同数据容量测试 → 30 分钟恢复完整稳定性链路及自然冷却再热证据（本轮 lifecycle candidates=completed=0）；96 小时仅为 formal Lease 时限。还需正式版本 tag 的发布门禁、signed stable native channel 与精确目标版本公共 APT/RPM 客户端验收。当前 read-only diagnose 矩阵成功不是 publication receipt；native preview 的临时测试签名也不是 stable 签名发布。
