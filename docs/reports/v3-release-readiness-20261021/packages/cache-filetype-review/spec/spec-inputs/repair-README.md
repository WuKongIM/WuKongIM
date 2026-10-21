# TEST ONLY DNF cache IPC 窄修（未提交）

基线 `de43579a9fa88bf70f64e6dcd926a2ddf6057496`，仅 driver/tests/docs/CHANGELOG 四文件。当前代码已停止编辑；`final-source/`、`source-freeze.json` 和 `implementation.patch` 绑定最终字节。

`cache_inventory` 仅豁免精确 `dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring/` 下的五个已观察名字，且 lstat 必须是 Unix socket、nlink=1、size=0。inventory 不读取、连接、删除或时间戳修改这些节点。其他路径、名字和文件类型保留原拒绝；普通文件同名仍按原规则哈希，metadata 同 inode/bytes 与真实读后的 atime 要求不变。拒绝信息提供 relative path 的 bounded repr（240字符）及 mode/nlink/size。

完整失败矩阵和全部10个新测试先写先冻结；`tests-first/` 的 driver 与 de435 原字节相同，测试文件从首红到最终验证完全未变。首红4 fail +4 error，修后相同10测试全部通过，0 skip。真实 Unix socket 覆盖 live/closed、精确五名、错误位置与名字；Mac 真实 socket 硬链接创建成功，nlink=2 被拒绝。FIFO/软链/regular hardlink/超限不读内容，socket-only 不充当 metadata，真实 OS read 证明仍有效。非零 socket size/device 与数量/总量预算使用明确文件系统边界替身，不冒充原生兼容。

验证：相关41测试通过；最终完整 Python 528测试通过（1个既有本机RPM工具不足 skip），4合同通过，diff-check通过。各运行收据冻结前后四文件hash一致。40保护文件（含12producer）与基线字节相同；除 inventory 外所有 driver function/class AST不变，既有49案例计划、native client/body、cache_evidence 和 cleanup均未更改。

本轮没有执行新的 Docker/49 native、没有提交/push/CI重跑或使用生产秘密。原第5代具体被拒 stat 不可追溯；独立 pinned Repo.load 机制诊断保留在 sibling `rpm-cache-filetype-diagnosis/`，前五代 CI 失败未覆盖。完整 Linux 49 验收仍需独立审查后的新 head CI，不能由本地 Python 结果替代。

- `before.json` / `before/`：基线四文件和规则摘要。
- `failure-matrix.md` / `tests-first.json` / `tests-first/`：代码前失败矩阵和原始测试冻结。
- `01-red.*`、`02-green.*`：同一组测试先红后绿。
- `03-related.*` 至 `06-diff-check.*`：相关/完整/合同及最终diff验证。
- `validation-summary.json`：准确边界与完整结果；`run-validation.py` 可重复命令。
- `ARCHIVE_ALLOWLIST.txt` / `archive-sha256.json`：仅显式公开源码和日志，不含缓存、密钥或构建包。
