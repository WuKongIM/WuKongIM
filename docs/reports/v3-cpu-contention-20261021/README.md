# CPU 竞争实验的保留证据

本目录对应 [run 39467558163 / attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39467558163)；
结论见[主报告](../2026-10-21-v3-cpu-contention.md)。
固定 C=`ef798352c40a94e57f768f76b2f4713712f827fe`，
H=`07ed85a22bdd2ebf75b776a92e71f21c02d2623f`。
**数值条件通过；双端点整体筛选失败；历史唯一根因未确认。**

| 文件 | 用途 |
| --- | --- |
| `raw-evidence.zip` | 原 artifact 全部 109 个非可执行文件，未修改内容；六轮报告、所有采样和 profiles、控制/构建/清理收据、Linux 预检 |
| `artifact-file-manifest.json` | 原 111 文件各自的字节数、SHA-256，以及是否保留 |
| `artifacts.json`、`download-integrity.json`、`run.json` | GitHub artifact 元信息、完整下载 SHA/CRC、terminal run/job 结果 |
| `frozen-plan.json` | 运行前冻结的输入、顺序和筛选 |
| `source-context.json`、`harness-rule-context.json` | 修改前及实际 H 的规则摘要 |
| `analyzer/` | 预先准备的独立分析器及事后描述性汇总器 |
| `analysis/` | 独立分析结果；较大的 `audit.json` 无损保存为 `audit.json.gz` |
| `verifier/`、`integrity/` | 另一套完整性核验脚本、固定预期文件、self-test 和 300,215 项核验结果 |
| `local-validation/` | 失败设计、先红后绿、相关 Go/Workflow/编译/vet/Actionlint 收据 |
| `delivery-validation.json` | 从此归档重新复算、文档检查及原始文件对应关系 |
| `SHA256SUMS` | 本目录除该清单自身之外的最终文件摘要 |

`analyzer/prepared-manifest.json` 和 `analyzer/README.md` 保留准备时的原貌，
其中“尚未运行”的文字描述的是准备阶段。实际执行和冻结结果在
`analysis/manifest.json`、`analysis/README.md` 与 `analysis/independent-summary.md`。
原始收据包含当时的绝对路径；复算到新路径不要求含路径输出的整文件摘要相同。
CSV 保留分析器生成的 CRLF 行尾；本目录的 `.gitattributes` 禁止 Git 转换其字节，
并只对这些 CSV 将 CRLF 识别为合法行尾，以保持原摘要可验证。
本地测试日志无损压缩为 `.log.gz`；`local-validation/log-manifest.json` 保存
28 份原日志的路径、字节数和摘要，可用 `gzip -dc` 查看，不清理原始测试输出的空白。

## 校验归档

在本目录运行（Python 3.11+ 标准库；仅读取证据）：

```sh
shasum -a 256 -c SHA256SUMS
python3 - <<'PY'
import gzip, hashlib, json, zipfile
from pathlib import Path
manifest = json.loads(Path("artifact-file-manifest.json").read_text())
expected = {f["path"]: f for f in manifest["files"] if f["retained"]}
with zipfile.ZipFile("raw-evidence.zip") as z:
    assert z.testzip() is None
    assert len(z.namelist()) == len(set(z.namelist())) == 109
    assert set(z.namelist()) == set(expected)
    for name, record in expected.items():
        data = z.read(name)
        assert len(data) == record["bytes"]
        assert hashlib.sha256(data).hexdigest() == record["sha256"]
print("109 unchanged retained files; ZIP CRC and SHA-256 pass")
PY
```

`raw-evidence.zip` 为 16,374,250 bytes，SHA-256
`ce58096dc8d51f8f38b6db59df03ffaedbba3fb776d6ab2e889034fe91dd0ed7`。
两个被省略的文件仅为产品 `binaries/C/wukongim-e2e` 和驱动
`harness/conversation_qps.test`；完整原 artifact 的 111 项均已在交付时读取核验。
仓库没有用摘要文本或空文件替代真实二进制。

## 不需要二进制的描述性复算

以下命令从保留的独立 audit 和全部原始 report 重新生成配对、重复性、覆盖和
S1 逐秒对齐表；它不重新证明二进制身份或独立 audit 的每项正确性：

```sh
cpu_case_dir=$(mktemp -d)
python3 -m zipfile -e raw-evidence.zip "$cpu_case_dir/download"
gzip -dc analysis/audit.json.gz > "$cpu_case_dir/audit.json"
python3 analyzer/summarize_observed_results.py "$cpu_case_dir/audit.json" \
  --artifact "$cpu_case_dir/download/conversation-cpu-contention"
python3 integrity/check_cgroup_cuts.py \
  --artifact-root "$cpu_case_dir/download/conversation-cpu-contention" \
  --output "$cpu_case_dir/cgroup-cuts.json"
```

结果写入新临时目录。比对 `independent-summary.json` 与 `analysis/` 中同名文件
（其中 `s1_all_windows_local_alignment` 含三个 S1 窗口的全部逐秒对齐结果）；
比对 `cgroup-cuts.json` 与 `integrity/` 同名文件。不要覆盖冻结结果。

## 从完整 artifact 独立复验

完整字节身份复验必须取得原 artifact **12288855188**，不能删除检查器中的二进制校验。
原 ZIP 为 84,435,437 bytes，SHA-256
`bd6cd557046b29fa1784e6e90f705910996e17e577f8182ec24914e3e34a187c`。
保留的 `artifacts.json` 给出 GitHub 下载端点；可在原 run 的 artifacts 页面下载，
并先核对该 SHA、大小和 ZIP CRC。若上游 artifact 过期且没有完整副本，实际二进制
字节复验就是不可用，不能以仓库中的历史通过收据代替新的字节检查。

将完整 ZIP 解压到一个新目录，然后执行：

```sh
cpu_full_root=/absolute/path/to/full-download
cpu_verify_dir=/absolute/path/to/new-integrity-result
cpu_analysis_dir=/absolute/path/to/new-analysis-result

python3 verifier/verify_artifact.py \
  --artifact-root "$cpu_full_root/conversation-cpu-contention" \
  --preflight-dir "$cpu_full_root" \
  --run-json run.json --output-dir "$cpu_verify_dir"

python3 analyzer/analyze_cpu_contention.py \
  "$cpu_full_root/conversation-cpu-contention" \
  --expected-harness-sha 07ed85a22bdd2ebf75b776a92e71f21c02d2623f \
  --output-dir "$cpu_analysis_dir"

python3 analyzer/summarize_observed_results.py "$cpu_analysis_dir/audit.json" \
  --artifact "$cpu_full_root/conversation-cpu-contention"
```

输出目录必须不存在。以上脚本只读取文件，不运行 artifact 内的程序，不启动产品、
不发送负载、不操作云资源。完整性检查器的 293,663 项加上前述 cgroup 补充检查的
6,552 项组成 300,215 项；它们不是统计独立样本数或产品测试场景数。
独立分析预期 `issues=[]`，原数值条件全通过，但两个 sync 配对均不满足预筛选。
执行退出码 0 不表示历史根因确认，也不表示正式版本发布。
