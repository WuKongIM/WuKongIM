# 性能排查证据

结论见[续报](../2026-10-20-v3-performance-followup.md)。本目录保留诊断材料，不是完整发布门禁收据。

- `reverse-order-plan*.json`、`dispatch*.json`：测量前声明、首次构建身份错误及修正。
- `reverse-first-run.json`、`reverse-first-failure.log.gz`、`reverse-first/`：39406775641 的预检失败；没有测量窗口。
- `reverse/`：39407349500 的最终 run、主机、两版 report/build/binary receipt、测试日志及八个 CPU profiles。`comparison/` 为先测 b469，根目录为后测 beta.24。
- `cost-*`、`latency-*`、`preparation-timing.*`、`source-delta-audit.md`：原失败与旧正序对照的分析，在新反序测量前形成。
- `reverse-cost-*`、`reverse-latency-*`、`reverse-independent-check.*`、`reverse-findings.md`：新反序定量分析及独立重算。
- `reverse-provenance-review.*`、`review_reverse_provenance.py`：28 项来源/配置核验。
- `source-context.json`：精确 b469 的 AGENTS/FLOW 与本次读取上下文摘要。
- `*profile*`、`*cpu-cum.txt`、`*servers-cum.txt`、`*tags.txt`：原 profile 元数据和新 profile 的有界 pprof 输出；不代表先前 60 秒窗口。

`manifest.json` 记录所有证据文件的保存字节与原始字节 SHA-256；自身不递归列入。两个新原始 report、日志和原始 `.txt` 输出以 gzip 保存，保留工具输出自带的空白；profile 本身为 pprof 格式。上列名称省略 `.gz` 后缀。没有保存二进制、凭据或临时签名下载 URL。

旧运行的原始报告与 profiles 继续位于相邻的 [`v3-rc-20261020/`](../v3-rc-20261020/README.md)，没有被本轮通过覆盖。下载收据里的 archive digest 来自 GitHub，未对整个 ZIP 本地重算；已下载的选择性条目核对了 ZIP CRC，并由本清单固定原始 SHA-256。

从仓库根目录运行：

```bash
python3 docs/reports/v3-performance-followup-20261020/verify_and_recompute.py
```

该命令验证清单，解压五份新旧原始 report 到独立临时目录，运行四个明确列名的本地分析脚本，并比较全部数值和输入摘要，仅正规化临时路径。它不启动产品、不访问网络、不触发 Actions，临时目录在完成后删除。输出应显示四项重算完全一致。

原分析脚本与独立审计脚本保留采集时的 `/tmp` 路径，以便追溯；跨机器数值复核优先用上述命令。独立来源审计还需要 Git 中保留精确 b469 对象和旧构建文件；其已执行结果单独保留，不会被离线重算命令冒充为再次执行。`fetch_selected_artifact.py` 仅留作原选择性下载方法的记录，不由验证命令执行。

Profile 可在解压后的目录用 `go tool pprof -symbolize=none -top -cum` 查看。三个 node profile 可以合并分析 CPU 样本，但合并 duration 是文件时长之和；此处的独立短窗不能替代发布负载或原失败时段。
