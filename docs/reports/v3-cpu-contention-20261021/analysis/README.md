# Run 39467558163 独立分析与便携复验

本目录结果已经冻结；不覆盖旧输出，不调整预先冻结的筛选条件。`manifest.json`
记录结果文件摘要，并证明实际使用的分析器与实验前保存的摘要一致。

完整输入是 GitHub run **39467558163 / attempt 1** 下载后解压出的
`conversation-cpu-contention` 根目录。驱动提交为
`07ed85a22bdd2ebf75b776a92e71f21c02d2623f`，产品提交为
`ef798352c40a94e57f768f76b2f4713712f827fe`。

分析器使用 Python 3.9+ 标准库，不需要 Go、Linux、产品进程或网络；可以在 macOS
对完整解压 artifact 离线运行。将以下三个路径换成自己的绝对路径即可：

```sh
ANALYSIS_SCRIPT_DIR=/absolute/path/to/analysis
ARTIFACT_ROOT=/absolute/path/to/conversation-cpu-contention
RECHECK_OUTPUT=/absolute/path/to/new-independent-recheck

python3 "$ANALYSIS_SCRIPT_DIR/analyze_cpu_contention.py" \
  "$ARTIFACT_ROOT" \
  --expected-harness-sha 07ed85a22bdd2ebf75b776a92e71f21c02d2623f \
  --output-dir "$RECHECK_OUTPUT"

python3 "$ANALYSIS_SCRIPT_DIR/summarize_observed_results.py" \
  "$RECHECK_OUTPUT/audit.json" \
  --artifact "$ARTIFACT_ROOT"
```

`RECHECK_OUTPUT` 必须尚不存在。第一个命令独立重算冻结条件、身份和全部数值；第二个
仅汇总相同结果，并添加事后要求的 S1 第三窗局部时间对齐，不改变预筛选。
最终 JSON 的输入根目录会随本地路径变化，因此不应要求重跑输出的整文件 SHA 相同；
核对冻结身份、各数值、issue 列表和判定即可。

完整复验要求保留以下两个文件：

- `binaries/C/wukongim-e2e`
- `harness/conversation_qps.test`

脚本只读取它们的字节计算摘要，绝不执行。若仓库证据归档为节省体积去掉这两个
可执行文件，归档仍可用于审阅报告、原始采样和既有摘要；**不能单独完成实际二进制
字节身份复验**。必须另行下载原始完整 artifact 并核对下载摘要/ZIP CRC。禁止伪造
占位文件、以摘要文本代替实际文件，或删除脚本中的字节校验让结果通过。
若归档将原文改为 `.gz` 保存，应先解压到新的临时目录并恢复原 artifact 路径布局；
不要改冻结归档或原始结果。

主要结果入口：

- `independent-summary.md`：预筛选失败、局部支持证据及归因限制。
- `audit.json`：独立验证 0 issue，以及逐点任务/cgroup 原文重算结果。
- `endpoints.csv` / `windows.csv` / `roles.csv`：36 端点、18 窗口、72 角色窗口。
- `independent-summary.json`：机器可读汇总及完整三个 S1 窗口的逐秒对齐。
- `01-S-window-*-timeline-alignment.json`：S1 三窗的额外粗粒度时间对齐。

结果区分：全部原数值门禁通过；双端点预声明 CPU 竞争筛选没有通过；不存在自动
根因确认、旧失败唯一归因、无性能回退或新版本正式发布结论。Linux preflight 与
GitHub 下载完整性属于另外保留的证据，不由离线分析器返回零替代证明。
