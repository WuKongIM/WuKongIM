# 固定 Linux 环境的性能证据

运行：[39453556254，attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39453556254)。
候选：`ef798352c40a94e57f768f76b2f4713712f827fe`。
结论：候选完整 20 项门禁通过；A/A 重复性失败；历史根因未完全确认。
详见[主报告](../2026-10-20-v3-fixed-host-performance.md)。

## 保存内容

- `dispatch-plan.json`：运行前绑定精确候选的计划；`experiment-plan.json` 是此前的原始计划。
- `source-context.json`、`candidate-context.json`：适用规则、配置及源码的冻结摘要。
- `run-final.json`、`artifacts.json`、`download-receipt.json`、`run.log.gz`：最终运行状态、
  GitHub artifact 元信息、实际完整下载和日志。`run-final.json` 是终态，旧准备记录中的
  “Linux 未验证”等描述保留其当时语义。
- `retained-reports.zip`：从完整下载逐项读取并重打包的 **137 个非可执行文件**，
  保留原目录和原始文件字节。包括全部 7 轮报告、资源切片、70 个 pprof、
  门禁报告、Linux cleanup preflight、命令/构建/清理收据。不是原 GitHub ZIP。
- `artifact-entry-manifest.json`：原完整 artifact 的 **141 项**文件大小及 SHA-256；
  四个产品/driver 可执行文件未提交到 Git，但已下载并核验实际字节，明确标为未归档。
- `independent-audit.json`：基于完整下载和冻结源码的独立验收。
  `independent-supplement.json` 保存 20 项明细、资源控制和计数汇总；
  `analysis.json` 仅为描述性分析，不替代验收。
- `historical-causal-boundary.*`、`resource-analysis.*`、`profile-analysis/`：
  历史因果审计、本轮资源分析及单独短 profile 分析的离线脚本、结果和解释。
  较大的分析 JSON 使用 `.json.gz`，原始内容可用 gzip 解压。
- `fixed-host-failure-design.md`、`fixed-host-red.json`、`fixed-host-validation.json`、
  `independent-check-design.json` 及压缩的本地测试日志：先行失败设计和工具验证记录。
- `SHA256SUMS`：此目录其余所有归档文件的 SHA-256；不包含自身。

完整原 artifact 的 ID 为 `12282956979`，大小 165,242,098 bytes，SHA-256：
`1adc8cfd1fa800cd1372f68f872b3ee395712b1da97eece073d8c63a99cf1718`。
GitHub 当前记录的过期时间为 2027-01-18；二进制完整复验依赖其仍可下载或已有完整副本。
本地完整下载曾验证全部 141 项 CRC，逐项 SHA 在本目录保存。

## 仅用仓库证据复算

在仓库根执行（Python 3、jq、Git；不启动服务，不运行负载）：

```bash
(cd docs/reports/v3-fixed-host-20261020 && shasum -a 256 -c SHA256SUMS)
python3 -m zipfile -e docs/reports/v3-fixed-host-20261020/retained-reports.zip /tmp/wk-fixed-retained
python3 -B docs/reports/v3-fixed-host-20261020/analyze_fixed_host.py \
  /tmp/wk-fixed-retained/conversation-fixed-host --output /tmp/wk-fixed-analysis.json
python3 -B docs/reports/v3-fixed-host-20261020/resource-analysis.py \
  --artifact docs/reports/v3-fixed-host-20261020/retained-reports.zip \
  --output /tmp/wk-fixed-resources.json
python3 -B docs/reports/v3-fixed-host-20261020/historical-causal-boundary.py \
  --repo "$PWD" --output /tmp/wk-historical-causal-boundary.json
```

`analyze_fixed_host.py` 输出 A/A 未通过和 C 三窗通过；它不检查完整门禁及实际可执行字节。
`resource-analysis.py` 的输入路径会随输入封装不同而变化，归档的各 block
原始字节相同，派生数值应一致。历史审计读取本仓库较早的归档，不访问原已消失的 VM。

完整门禁可单独用候选提交的原 validator 复算；二进制身份验收见下一节：

```bash
git show ef798352c40a94e57f768f76b2f4713712f827fe:scripts/validate-conversation-qps-report.jq > /tmp/wk-fixed-validator.jq
WK_FIXED_PROFILE_SHA=$(shasum -a 256 /tmp/wk-fixed-retained/conversation-fixed-host/harness/profile.json | cut -d ' ' -f 1)
jq -e --arg sha ef798352c40a94e57f768f76b2f4713712f827fe \
  --arg profile_sha "$WK_FIXED_PROFILE_SHA" \
  --slurpfile profile /tmp/wk-fixed-retained/conversation-fixed-host/harness/profile.json \
  -f /tmp/wk-fixed-validator.jq /tmp/wk-fixed-retained/conversation-fixed-host/gate/report.json
```

## 完整字节与身份验收

下载原 artifact 并校验上面的整体 SHA；解压后应同时有 `conversation-fixed-host/`
和其旁的 `conversation-fixed-host-preflight.jsonl`。从仓库根执行：

```bash
python3 -B docs/reports/v3-fixed-host-20261020/check_fixed_host_independent.py \
  /absolute/full-download/conversation-fixed-host --repo "$PWD" \
  --run-json docs/reports/v3-fixed-host-20261020/run-final.json \
  --output /tmp/wk-fixed-audit.json
```

默认候选和 run ID 已绑定上述精确值。检查器读取 Git 中的冻结 profile/validator，
核验二进制、driver 字节、构建来源、Linux preflight、配置和进程控制、所有窗口及完整门禁。
只用 `retained-reports.zip` 跑它会因缺少四个可执行文件而失败；不要跳过该检查伪造通过。
检查器退出 0 表示证据有效，**不表示 A/A 通过**，必须读取 `all_aa_criteria_pass=false`。

`profile-analysis/` 内脚本的具体调用及归一化口径见 [profile-analysis.md](profile-analysis.md)；
它使用真实 Linux 可执行文件做符号化，需要完整下载和 Go 的 `pprof` 工具。

统计边界：窗口 P99 的中位数不是合并请求的 P99；资源切片含 warmup/观测开销；
选择性 metrics 加 body hash 不等同于保存完整 HTTP body。所有短 profile 都是另外的采样窗口。
这些证据既不证明独占物理宿主，也不完成正式发布或历史物理根因确认。
