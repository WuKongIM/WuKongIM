# 反序诊断来源核验

结论：留存的工作流证据内部一致，支持将 `39407349500` 作为 **b469 先、beta.24 后** 的一次反序诊断对照。28 项核验全部满足；未发现版本、harness、profile 或负载配置串用。需要准确披露构建角色修正和硬件变化，不能据此声称二进制逐字节等价或正式发布 gate 通过。

结构化结果见 [reverse-provenance-review.json](reverse-provenance-review.json)，可复核脚本见 [review_reverse_provenance.py](review_reverse_provenance.py)。脚本仅读取本地留存证据与 Git 对象；未 dispatch、修改仓库、运行产品测试或负载。

## 运行顺序与身份

新运行的 `run.json` 显示 workflow head 为 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。两个 report 均记录相同精确 harness SHA、`harness_dirty: false`、`complete: true`。

| 产品 | 新运行的目录 / 顺序 | 三个固定诊断窗口范围（UTC） | 新工作流记录的 binary SHA-256 |
| --- | --- | --- | --- |
| b469 | `reverse/comparison/`；先运行 | 10:24:29.928887087 至 10:27:31.365020066 | `0d25039bfc518b166eded2871a365231a041d710c52636a2154eda27179a3825` |
| beta.24，`4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` | `reverse/`；后运行 | 10:29:28.313097913 至 10:32:29.765042603 | `e327d0252e814b47efdc06ff9c6b80d847af29e30c20a1b22ba1dee98cd45a9a` |

各目录的 `build.txt`、`binary.sha256`、`report.json` 三者的版本与摘要相符。`build.txt` 显示 Go 1.25.11、Linux AMD64、CGO=1、`-tags=e2e`、精确 `vcs.revision`、`vcs.modified=false`。工作流先在根目录构建 beta，再在 `comparison-product` 构建 b469；实际测量顺序是 comparison 在前、root 在后，不能把构建先后误写为测试先后。

依据：固定 b469 的 `.github/workflows/conversation-qps-diagnose.yml:35`–`:91` 为两种构建角色，`:98`–`:123` 为两种测量角色；新运行相应 steps 的时间及 report 内窗口时间相互吻合。

## 旧 / 新同版本构建比对

对每个版本分别比较旧、新 `build.txt`：去掉首行之后，其余 **109 行逐字节一致**。首行中的工具链版本也单独验证为 `go1.25.11`；未把工具链差异隐藏在正规化操作中。

| 产品 | 旧角色 / 摘要 | 新角色 / 摘要 | 正规化 build metadata SHA-256 |
| --- | --- | --- | --- |
| beta.24 | comparison；`2aba832435ad303bd70e47a79b618114d5522aca70109b6a0d7f45affc652853` | root；`e327d0252e814b47efdc06ff9c6b80d847af29e30c20a1b22ba1dee98cd45a9a` | `3bb5f493685d7971f787e2b69c1e3d4c701cfd1fb4f4c63b39e72eeee20b97e9` |
| b469 | root；`963bdb3d10489dea79550de3a18b4e6d7ffb903d18eedf5ebaa6c2ba516f7ecf` | comparison；`0d25039bfc518b166eded2871a365231a041d710c52636a2154eda27179a3825` | `a2bfe55eac50f66a0d6d6b8ae7458fee023adfa95cf46c7fe5bda456fb76cafc` |

固定工作流的两条 `go build` 均没有 `-trimpath`，源 checkout 位置随角色变化；摘要差异与这一变化相容。**不能进一步断言全部字节差异仅由路径造成。** Go build metadata 一致只能证明已记录的构建字段一致，不能替代完整二进制比对。

新旧 selective-download 清单均明确 `binary_downloaded: false`、`archive_digest_recomputed: false`。本次核对的是工作流生成的摘要；另由 fixture 在启动后读取所用 binary 并计算 report 摘要（`mixed_attribution_test.go:75`–`:78`）提供同一运行内的交叉印证。已校验实际下载的所有清单文件大小与 ZIP CRC32；未将此称为整个 archive 或 binary 的本地 SHA-256 验证。

## Harness、profile 与负载配置

旧运行 `39369947381` 和新运行的四份 report 均满足：

- harness 精确 b469 且 clean；source-context 与 corrected plan 内冻结的全部文件摘要均对应本地 Git 的精确 b469 对象。
- profile 摘要均为 `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`；与 b469 中原始 `profile.json` 及旧下载文件一致。
- `config` 对象完全相同；3 × 60 秒，list 200 QPS、sync 60 QPS、每 endpoint 8 workers，3 节点、driver GOMAXPROCS=4、node=2。report 中每个窗口的 endpoint case、时长与 worker 数也一致。
- CPU profile 的 10 秒独立窗口在三个诊断窗口之后，未混入三窗口对比。

这证明所核对的 profile、stress 配置和固定 harness 相同；report 并未包含全部有效进程环境的独立导出，不扩展为“所有宿主状态完全相同”。新旧每次内部均为同一 host 上顺序测量，但旧主机为 AMD EPYC 9V45，新主机为 Intel Xeon Platinum 8573C，不能把两个 run 当成相同硬件。

## 预声明与预检修正

按留存计划的 `declared_at` 与 job `startedAt`：

- 原计划：10:16:21.906725，早于预检运行 `39406775641` 的 10:16:37。
- 修正计划：10:21:20.469504，早于成功运行 `39407349500` 的 10:21:27。

预检在 root beta build 的预期摘要检查处退出：旧比较目录摘要为 `2aba…2853`，本次 root 构建摘要为 `e327…5a9a`。其两项测量步骤及 release matrix 步骤均 skipped，执行诊断窗口数为 0；不是观察到性能结果后重选窗口。预检和成功运行的 root beta 摘要相同。

**需修正的表述：不能说计划“只修改了 beta 哈希字段”。** JSON diff 显示，除 beta root 预期摘要外，还把候选的 `first_expected_binary_sha256` 从旧 root 摘要改成 `null`，并将旧值保存为 `first_prior_role_binary_sha256`；另更新声明时间、预检 run ID 和 correction 说明。它们共同属于构建角色身份修正。新 comparison 候选摘要是本次记录并交叉核对的值，不是预先已知并强制相等的旧角色摘要。

产品 SHA、harness、profile/context 摘要、假设及其排序、所有阈值/负载不变量、1 次有界实验、禁止 release matrix 和发布的范围均未改变。workflow 本来也只对 root 产品提供 `expected_binary_sha256` 输入，comparison 产品没有同名预期摘要 gate。

## 验收边界

新 run 的完整 release matrix 步骤明确 skipped。此结论是来源与配置一致性核验，不能把 workflow success 或三个 diagnostic 窗口的 verdict 当成完整 QPS 发布 gate，更不能据此验收后续迁移修复版本。一次跨主机的反序顺序对照也不能独立消除版本、时间、准备状态与硬件因素。
