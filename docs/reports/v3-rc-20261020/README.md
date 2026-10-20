# v3 RC 验收证据

产品候选固定为 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。主记录见 [RC 验收报告](../2026-10-20-v3-rc-qualification.md)。这些是内部验收产物，不是 v3.0.0 发布产物或正式云上长稳证书。

## 内容与身份

- `unit/validation.json` 记录完整默认 Go 测试层及相关 integration；原始日志以 `.gz` 留存。
- `context.json` 冻结初次 E2E 的 AGENTS/FLOW 摘要；`binary.json` 保存被测 Mac 二进制的实际 build info 和 SHA-256。
- `local-e2e.json` 保存十组首次运行的完整命令、时间、测试结果、跳过项和日志摘要；失败原样保留。
- `cross-node-retest.json` 分开记录原产品 SHA、修正后 harness commit/tree、指令摘要和相关验证；`cross-node-auth.diff.gz` 是原候选到测试夹具提交的精确差异。
- `migration-diagnosis.json` 记录已确认的错误分类缺口、尚未证明的原因及最小修复/验证工作包。
- `sdk-summary.json` 保存三个 SDK 的精确 source/run/job 与双向消息断言；完整 SDK job 日志仍在链接的 GitHub run 和原本机临时证据目录，摘要保留其 hash。
- `qps-summary.json` 与 `qps/` 保留 Linux 失败窗口、主机/工具链/二进制身份、独立 profile、分析和原矩阵未启动的证据。profile 不是合格性能窗口，CPU累计百分比不可相加。
- `qps-comparison-dispatch.json`、`qps-comparison-summary.json` 与 `qps-comparison/` 保存预声明的同机 beta.24/候选比较及两版原始报告；诊断 Workflow 成功不等于性能窗口通过，也不替代精确发布标签门禁。
- `restore-diagnostic/final-diagnosis.json` 记录原始三节点备份恢复最终通过；保留的中途日志只用于解释观察过程，不能将无新日志误称为测试失败或推断具体阻塞阶段。
- `reports/` 保存本地场景原始 JSON；`.gz` 只压缩，未改动原始内容。
- `qualification-control.json` 冻结正式长稳配置、负载、工作流及 QPS profile 摘要，明确尚无付费采购或版本发布。

JSON 中保留原始临时目录路径，方便对应本次执行；本目录内的交付路径与压缩前后 hash 以 `evidence-index.json` 为准。产品二进制没有提交入库。本地 E2E 二进制已实际重算 hash；Linux QPS 二进制只记录了工作流给出的 hash，未下载整包后独立重算。

## 验证保留字节

在本目录运行，逐个验证压缩文件及其原始内容的摘要：

```sh
python3 - <<'PYVERIFY'
import gzip, hashlib, json, pathlib
root = pathlib.Path('.')
index = json.loads((root / 'evidence-index.json').read_text())
for name, entry in index['files'].items():
    payload = (root / name).read_bytes()
    assert hashlib.sha256(payload).hexdigest() == entry['sha256'], name
    original = gzip.decompress(payload) if entry['encoding'] == 'gzip' else payload
    assert hashlib.sha256(original).hexdigest() == entry['original_sha256'], name
print('All retained evidence hashes match')
PYVERIFY
```

读取原报告示例：`gzip -cd qps/report.json.gz`、`gzip -cd logs/08-ordinary-history.jsonl.gz`。不要将一次失败后的成功重跑替换掉这里的首次记录。

## 重复功能验收

在全新、干净的候选 checkout 使用记录的 Go 工具链，先固定 `GOWORK=off`，构建 `go build -tags=e2e` 的产品并保存 build info/hash，再使用 `WK_E2E_BINARY` 指向它，按 `local-e2e.json` 的 command 串行运行。将报告路径替换为新的独立输出目录；这些路径只属于测试框架，不作为产品配置传入。

嵌套 worktree 的 Go VCS 检测可能读取外层仓库。此次通过明确 `GIT_DIR=$(git rev-parse --absolute-git-dir)` 和 `GIT_WORK_TREE=$PWD` 绑定构建源码，并核对实际二进制 revision=b469、vcs.modified=false；未将最初误标外层 SHA 的试构建纳入验收。也可按 PROJECT_KNOWLEDGE 使用拥有独立 `.git` 目录的干净 checkout。

修复产品后需重新冻结 candidate、二进制与 harness 身份，并执行相关场景；不能把本次固定 b469 的结果自动继承给新的产品提交。Mac 功能验证不替代 Linux QPS、正式 72 小时 Soak、容量与精确发布标签门禁。
