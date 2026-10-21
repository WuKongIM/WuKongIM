# Native cache IPC repair

Standards: 0 findings. Spec: 0 open findings. Prior failures/reviews remain unchanged. Full native/Linux/cloud qualification is separate evidence.

## Standards

B — Native cache IPC 窄修 Standards 审查：未发现可操作的 P1/P2。

冻结 base `de43579a9fa88bf70f64e6dcd926a2ddf6057496`；source-freeze SHA `551a8954bdaabc7817a8cd41f6a3f0658dc85e9c89c79b81488bcaaea2a11877`；patch SHA `d46227c5c27e57f4588704e4d9114609881a36f2f29e4fc37fcf9fef6ff43ec6`。四文件、Git diff、40 个保护文件均独立核验一致；保护集合包含作者列明的 12 个 producer。除 `cache_inventory` 外 driver 的函数/类 AST 全部与 base 相同，native 调用、49 案例计划、cache_evidence/read-reuse、cleanup 未改。

已读上级 AGENTS、packages README、release-contract 相关原生/TEST ONLY 合同、stable-audit-transport；此 packages 工作树没有更细 AGENTS 或 FLOW。改动只有 inventory、测试、文档与 Unreleased，无签名/发布控制或生产权限变化。

`tests/native_stable_clients_drill.py:470` 的豁免同时要求精确 canonical pubring 父路径、五个精确名字、lstat Unix socket、nlink=1、size=0；最多五个节点，不增加 regex、file:// 诊断目录或普通文件豁免。跳过在哈希/arm 前，不读、不连、不删这些 socket，也不把它们计作 metadata。其它特殊类型仍拒绝；普通文件的大小、数量、总量及真实读证明保留。拒绝诊断只含截断转义 relative path（240 字符）和 mode/nlink/size，不读取内容或输出 cache 绝对根。

tests-first：独立核验 tests-first 的 driver 等于 base；最终测试 SHA 与首红、绿测完全相同，冻结→红→绿时间有序，日志哈希吻合。10 新测试先红（4 fail+4 error）后绿，0 skip；包括真实 live/closed socket、真实硬链接及真实 OS metadata read。统计边界替身明确标注，未当作原生证明。作者 528 Python/1 既有 RPM 工具 skip、4 contract 的保留结果仅作其本地验证依据，本审查未重跑。

原 CI 没有被拒节点 stat，不能确认该次唯一根因；pinned file:// Repo.load 只证明匹配机制。此补丁尚未证明完整 Linux native 49 案例通过，更不代表生产 stable 发布或公开 APT/RPM 精确版本验收。没有重复 Spec probe，没有源码、CI、API、云或发布动作。


## Spec

Spec 轴：0 项发现，未见遗漏、错误实施或范围扩大。例外严格匹配 canonical 父目录、五名、lstat Unix socket、单链接与零字节（规格 README:13、failure-matrix:21）。普通同名文件仍哈希；socket 不能充当 metadata 或复用证明；原生 body、49 案例、签名、cleanup 和其他 23 个函数未变。独立轻量探针 95 断言通过，含真实 live/closed socket、硬链接拒绝、路径边界、读前后 atime 与 inode/SHA/size 校验；四文件及 40 保护文件核验一致。原 CI 被拒文件 stat 仍未知；签名 file:// 重放仅证机制，完整 Linux 49 场景仍未证实（规格 README:17）。

