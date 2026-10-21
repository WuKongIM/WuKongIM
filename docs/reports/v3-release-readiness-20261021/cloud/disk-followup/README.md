# 本任务磁盘只读复核

结论：本任务可识别的可重建文件不足以解除本地 30 分钟 shakeout 阻塞；没有删除文件、迁移 NAS、启动负载或修改 5% 门槛。

测量时间：2026-10-21T06:06:17.392310+00:00。`statvfs(/tmp)` 容量 994,662,584,320 bytes，可用 8,742,866,944 bytes（8.743 GB，0.8790%）。达到 5% 本身还需 40,990,262,272 bytes（40.990 GB），尚未包含测试运行余量。

仅统计六个此前收据明确记录的本任务输出目录，总占用 3,833,376,768 bytes（3.833 GB）。即使把其中全部验证证据也错误地算成可回收，仍差 37.157 GB；因此无需扩大扫描或试跑来判断是否足够。

下表只是未来清理的候选，不是删除授权。缓存可重建；两个 `download` 已逐文件核对保留 ZIP 的大小和 CRC，ZIP 的 SHA256 与既有 API 收据相符且 ZIP CRC 完整。必须保留 ZIP 和验证收据，任何实际清理前还须排除活动读者。

| 精确候选路径 | 当前分配占用 |
| --- | ---: |
| `/tmp/wukongim-v3-readiness-20261021/local/go-cache` | 1.755 GB |
| `/tmp/wukongim-v3-readiness-20261021/replication-diagnosis/gocache` | 0.738 GB |
| `/tmp/wukongim-performance-fixed-20261020/download` | 0.385 GB |
| `/tmp/wukongim-cpu-causal-20261021/download` | 0.419 GB |

上述候选合计 3.297 GB。缓存无已验证逐字副本，但构建命令及测试输出保留在缓存目录外；重建会再次耗用空间与时间。远程 artifact 的既有记录未过期（到 2027-01-18），本轮未联网确认可再下载，结论依赖已校验本地 ZIP。其余报告、原始日志、失败窗口和未确认副本的二进制保留，不列为可删除项。

历史 `owned-temp-cleanup.json` 六个路径及 mqtt 独立缓存现均不存在，不重复计算历史已释放字节。未查看或触碰共享 Go 缓存、其他任务 worktree、其他用户目录；未扫描全盘。

门槛来源：当前源码 `ed705c0fbffdea485bbb65608d6e340bb049d97e`，`configs/wkbench/chat-lifecycle/local-shakeout.yaml:71` 固定 `disk_safe_stop_free_percent: 5`；runbook 第 130–132 行规定低于 5% 的 `disk_free` 是前置失败，第 274–277 行禁止降低门槛强行准入。既有 `cloud/local-shakeout-preconditions.json` 已记录同一阻塞。本次是主机前置检查，不能报告为 shakeout 完成或通过。

`du` 是分配块统计，APFS 快照、克隆或硬链接可能让实际回收量更小；可用空间也可能被并发任务改变。未估计运行余量，因为连 5% 基线都无法由此范围满足。详细文件副本核验、路径来源、数量和限制见 `audit.json`；复核脚本 `audit.py` 仅检查固定任务路径。
