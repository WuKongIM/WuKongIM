# 独立短 profile 分析

完整解释见 [profile-findings.md](profile-analysis/profile-findings.md)，精确派生值见
[profile-comparison.json](profile-analysis/profile-comparison.json)，离线复算收据见
[offline-validation.json](profile-analysis/offline-validation.json)。

没有定位到候选独有且稳定的新增热点；这些 profile 不覆盖原始三个 60 秒窗口。
C 的短窗等待升高也完整保留，不能以短 CPU profile 解释 off-CPU 原因。

所有脚本、Go profile parser、pprof 文本输出、输入 SHA 和命令均在 `profile-analysis/`。
较大的 `profile-data.json.gz`、`profile-summary.json.gz` 是原 JSON 字节的无损压缩。

在仓库根复算（Python >=3.11、已安装 Go >=1.25 及缓存中的仓库锁定模块）：

```bash
python3 docs/reports/v3-fixed-host-20261020/profile-analysis/analyze.py \
  --artifact-root /absolute/full-download/conversation-fixed-host \
  --repo "$PWD" --output-dir /tmp/wk-new-profile-analysis
python3 docs/reports/v3-fixed-host-20261020/profile-analysis/summarize.py \
  --analysis-dir /tmp/wk-new-profile-analysis
```

输出目录必须不存在。离线解码不执行 artifact 中的任何二进制，使用它们只为核验与符号化。
实测 Linux 工具链为 Go 1.25.11；本机离线 parser/pprof 工具链为 Go 1.25.0 darwin/arm64，
已在收据中分别保留，不能把它当作重新执行了 Linux 性能测试。
