# #1020 合并后的一次性续跑计划（仅准备，未启动）

控制代码固定 `e30299fa59ea6669ea71024eabe209840f241f19`，产品固定 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`；同一 request `chat-20261021T095421Z-cf11d65c`、operator `tangtaoit`、原诊断 Ed25519 **公钥**不变。最近人类仅批准合并 #1020；此次未获得新的付费启动决定，没有调用 Actions、Quote、Acquire 或云 API，也未访问私钥/Secret 值。

12:54:54Z 的只读快照：远端 main 为 e30299fa；正式 rehearsal workflow329938003 active，最新仍是原 run39581355444 / number32 / attempt1。下一次被现行合同允许的唯一 continuation 必须是真实 number33、attempt1（代码检查 predecessor+1，不硬编码32/33）。Issue #1018 标题精确、open/unlocked；本次只读了该 Issue，不把它等同于启动时完整唯一性 preflight。

`planned-workflow-inputs.json` 是五项公开输入。只有另获明确启动决定、并重新核对 main/source/run 次序后，才可执行以下**未执行**命令：

```sh
gh workflow run chat-lifecycle-rehearsal.yml --repo WuKongIM/WuKongIM --ref main --json < planned-workflow-inputs.json
```

`planned-dispatch.json` 是等价 REST body（ref+inputs），不是 `gh --json` 的 stdin。当前入口要求 ref main；API 没有 compare-and-swap 控制SHA参数，当前快照不等于未来原子冻结。若main或最新run变化，重新审阅对应计划；不得虚构运行ID、改run number、换request或省略 predecessor 规避一次性边界。

启动采用现有流程：完整active/stop/inventory检查 → Issue只读preflight → continuation完整认证 → 构建fc182新bundle → 新Quote/费用与库存准入 → 一次rehearsal-2 Acquire/部署 → 2h预演与释放清零 → 重新认证并带费用晋级全新formal Lease → 72h、同数据容量≤8h、恢复30m → Release及精确zero。未改工作负载、阈值、重试、源码或权限。旧bundle只作历史证据；新bundle的run/digest尚不存在，不用旧ed705或旧digest冒充。

四台杭州Ubuntu x86_64主机，每台4核8GiB；3台service各40GiB系统盘+500GiB数据盘，1台load40+200GiB，ESSD PL0；公网仅load一EIP，20Mbps。rehearsal新Lease12h、就绪窗口2h、测量2h；formal新Lease96h、soak72h、容量≤8h、恢复30m。具体来源为本目录冻结template与当前workflow，无新的规格override。

总预算仍为原请求累计 **¥1500 / ¥1350 operational stop**。原失败Lease必须继承完整 **¥74.4776** quote reserve（74,477,600 micros），不能归零、改成估算经过时间费用或称实际账单。未开始新资源前剩余硬预算1425.5224、stop余量1275.5224；formal prior=旧reserve+本次新预演的既定保守成本。费用由认证helper/原流程推导，没有操作人可覆盖的金额输入。旧实际账单仍未知；旧初始readiness原因仍unknown，不能写成已修复preflight或可重试runtime失败。

本轮本地做了有界实证：导入精确e302 helper，将网络seam强制禁止，对旧冻结8份公开JSON逐一校历史SHA，再执行 `authenticate_run` 结构校验和 `validate_prior_documents`（内部包含 `validate_cost`）。结果通过、reserve=74,477,600；zero为10:19:19.689029905Z，quote→zero956.923479秒。收据 `offline-public-documents-check.json`。这只证明这些公开文档满足当前本地合同，不是完整live authority，不是新provider库存或真实账单。

完整 `prepare` 必须有尚未存在的真实successor GitHub run、受保护身份和完整inventory；attempt2 `validate` 也必须有已认证sidecar并实时重新GET。CLI没有完整offline/dry-run模式。本次没有伪造环境或调用它们。首次attempt1/prior0的本地早返不适用于本request。helper的部分函数可安全离线调用，与完整启动资格明确区分。

旧top/Acquire/Release精确artifact metadata当前均未过期；Acquire authority expires 2026-10-28T10:11:53Z，Release expires 10:19:19Z；top90天留存不延长子产物authority。完整ZIP本轮未重下，使用此前通过全SHA/size/CRC的公开留存文档并核其清单；启动时仍须按现有helper重新核完整字节、关联、12scopes、零库存时序、成本覆盖、stop与inventory。已有Lease的Release不被sidecar失效阻断；资格或新采购则拒绝。

固定源码参考：`source/.github/workflows/README.md:1016` 起（授权/续跑/费用/留存）；`source/scripts/chat-lifecycle/authenticate-released-preclock.py:130` 相邻producer、325旧文档、383成本、517prepare、534validate；`source/scripts/chat-lifecycle/stage-orchestrate.sh:51` 采购前preflight和proof、332新bundle、555后新Lease材料。正式finalizer与starter继续继承并重算prior，cleanup独立，未新增额外强制gate。

唯一只读GET曾遇一次获取Acquire run失败；原观察保留，后续一次同endpoint有界GET成功。没有重跑workflow。所有证据为可review的公开数据；`ARCHIVE_ALLOWLIST.json`/`SHA256SUMS`列出本目录文件，source快照不是仓库改动。
