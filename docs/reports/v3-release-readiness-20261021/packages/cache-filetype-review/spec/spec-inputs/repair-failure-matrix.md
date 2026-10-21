# 最小 tests-first 矩阵（提案，未修改或运行 shipping tests）

基线 `de43579a9fa88bf70f64e6dcd926a2ddf6057496`；严格父路径 `dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring/`，只允许 [path-contract.json](path-contract.json) 五个名字且 `lstat` 为 Unix socket。普通文件仍走原检查。先冻结下列测试并跑红，再窄改现有 inventory；不得将本诊断当 49 案例通过。

| 先行病例 | 必须结果 |
| --- | --- |
| 精确父路径，各五名真实 AF_UNIX socket，加有效非空 metadata | inventory 接受普通 metadata；不返回 socket、不读取/连接/删除 socket、不更改 metadata bytes；五名 socket 仍存在 |
| 精确父路径只有五 socket、没有 metadata | inventory 不产生伪 metadata；`cache_evidence` 仍拒绝缺 metadata |
| 五个已知名字分别换成 FIFO/软链；外部目标含 sentinel | 拒绝且不打开/跟随/删除，sentinel 不变 |
| 未知 socket 名，包括 S.gpg-agent.foo、S.scdaemon.extra | 拒绝 |
| 五名 socket 放到根、apt、dnf-cache、dnf-cli-state、另一 repo/hash、pubring 子目录、错误 pubring 拼写 | 拒绝；不新增 regex 或 file:// 探针 hash 例外 |
| 已知名字改为普通文件 | 仍按原 regular/nlink/size 逻辑处理，不因名字丢弃；多硬链接及 >64 MiB 仍拒绝 |
| 普通 unknown FIFO、device、socket、软链、多硬链接、超限文件 | 原 fail-closed 条件保留；device 可用受控 lstat 替身，不需新权限 |
| 五个例外 socket 与正常 cache arm | socket 不被 utime；普通 metadata 原 sha/size/device/inode/mtime/atime 逻辑保留 |
| metadata 没读、改变 inode/hash、仅复制新 metadata | 原 `require_reuse` 仍拒绝；只有原实际读证据通过 |
| 拒绝路径含换行/长名字 | 诊断有有界、转义的 relative path 与 mode/nlink/size，不打印文件内容/任意绝对路径 |
| 原 inventory 字节/数量预算边界 | regular 文件预算继续有效；例外总数最多五，由精确五路径限定，不能用例外绕过成任意数量 |

本机轻量用真实 Unix socket 关闭后留下节点即可复现类型，无需再生成密钥。原生验收另用已固定镜像/签名 fixture：本诊断已证明 Repo.load 产生上述 IPC 节点；补丁后的新 head 仍须首次完整 49 案例及实际 cache read/repeat，不能用这个局部机制测试替代。生产 native client/body、DNF signature 参数、49 计数、producer/control/planner 不需更改。

Root 授权补充（2026-10-21）：跳过条件额外要求 lstat nlink=1 和 size=0；真实 live 与 closed-but-present 五 socket，Mac 短 /tmp 路径，真实 socket 硬链接能创建则实际测；不能创建则记录平台事实并留 Linux 不跳过。诊断上限固定为 relative repr 前 240 字符，mode/nlink/size 均保留；不读取 FIFO/socket。测试作用接口为已授权 cache_inventory/cache_evidence；一次性先冻全失败矩阵和测试，再改 inventory，遵守本任务显式 tests-first 要求。预算/虚拟 device/stat 是文件系统边界替身，不作为原生恢复证明。
