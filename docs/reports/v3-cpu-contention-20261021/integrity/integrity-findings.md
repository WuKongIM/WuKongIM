# Run 39467558163：独立证据完整性核验

严格核验通过：主核验 293,663 项、补充 cgroup 原始收据核验 6,552 项，均无失败。六轮 S/O/D/D/O/S 和全部 18 个原始窗口齐全；重新计算后，36 个端点阶段及逐窗分配上限全部满足原数值条件。`release_qualification=false`：本轮是诊断采集，不是新发布验收。

实际二进制逐字节 SHA-256 为 `8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d`，绑定产品 C `ef798352c40a94e57f768f76b2f4713712f827fe`；新 harness 为 `07ed85a22bdd2ebf75b776a92e71f21c02d2623f`；实际 profile 摘要为 `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`，与冻结 C/H 文件完全一致。检查了 Linux AMD64 ELF 格式、Go 1.25.11 clean VCS build receipt、独立 driver 摘要与所有报告身份。没有执行 artifact 中的二进制或代码。

| 项目 | 已核实结果 |
| --- | --- |
| Linux 预检 | cleanup、all-thread placement、sampler 三项各一次明确 pass，无 skip/fail |
| 命令与清理 | 11 份命令收据；产品/driver 各构建一次，六块各运行一次；全部退出 0、无 timeout、owned-session cleanup 完成、无剩余进程 |
| S | driver/node 均使用 CPU 0–3；driver GOMAXPROCS 4，node 2 |
| O | driver CPU 0；三个 node 共用 CPU 0、2、3；driver GOMAXPROCS 1，node 2 |
| D | driver CPU 1；三个 node 仍共用 CPU 0、2、3；driver GOMAXPROCS 1，node 2 |
| 准备年龄 | 六块准备完成年龄 37.026–53.566 秒；统一释放年龄 180.013–180.016 秒，随后 4.635–5.369 ms 内应用 placement |
| 窗口采样 | 每窗 61 点，共 1,098 点；连续覆盖 slot 0–60，零 missed slots；全部原始进程与线程 stat/status 身份、目标掩码一致 |
| 生命周期覆盖 | 0 次确认退出观察；首点之后出现 4 次新线程观察，首次被看见前的区间不能当作已测量等待量 |
| 文件清单 | 111 个证据文件，合计 418,699,385 字节；逐文件 SHA-256 已保留 |

本机为 AMD EPYC 9V74、4 logical CPUs、guest-visible 2 cores × SMT2。所有观察到的四进程 cgroup membership 始终一致；36 个窗口边界 cut 中的可见祖先 CPU/cpuset/memory 静态控制均匹配初始 host receipt。非根可见层 `cpu.max=max 100000`；根层缺少 cpu.max/memory 控制仍按未知处理。没有证明物理主机独占或隐藏父级约束。

采样首点在窗口开始后 0.229–1.123 ms，末点在 60.000060–60.003955 秒，采样结束在 60.007920–60.034144 秒。每窗 collector OS-thread CPU 为 259.417–355.631 ms（约单 logical CPU 的 0.43–0.59%，不是完整观测开销上界）；一次读取最长 21.080 ms、定时延迟最长 11.129 ms。因此该诊断包含已量化但非零的采样扰动；每秒采样也不是连续调度追踪。

O/D 固定了各角色 mask 宽度，但总可用 CPU 并集从 3 变为 4；D 仍与 node 共享 SMT 物理核及其他宿主资源。S/D 同时改变了 driver 并行度，不能作为单变量因果对照。本轮固定 C，只能支撑本次 placement 干预的分析；不能据此证明旧版本差异或历史 runner 上故障的根因。短 profiles 仍不覆盖原始 60 秒窗口。数值通过和完整性通过均不能替代对预声明对照、重复波动及机制信号的分析。

完整结果：`verification.json`、`file-digests.json`、`coverage-summary.json`、`coverage-ranges.json`、`cgroup-cuts.json`。独立主核验脚本保存在 `/tmp/wukongim-cpu-contention-20261021/integrity/verify_artifact.py`；补充脚本为本目录 `check_cgroup_cuts.py`。API artifact 大小/SHA 与 ZIP CRC 由主代理另行核验，本核验从已解压文件重新计算身份和完整性。
