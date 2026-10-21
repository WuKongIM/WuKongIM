# PR #74 第五代 cache inventory 文件类型诊断

结论：在固定 Rocky/Alma 镜像内，独立 TEST ONLY 签名空 metadata 的真实 `Repo.load()` 成功，随后原版 inventory 因 pubring 中 Unix socket 而拒绝。现有三项合取中失败的是 regular-file 类型；全部 socket `mode=0140700, nlink=1, size=0`，普通文件最大 1200 B。两发行版均观察到：

- `S.gpg-agent`
- `S.gpg-agent.browser`
- `S.gpg-agent.extra`
- `S.gpg-agent.ssh`
- `S.scdaemon`

真实 file:// 重放目录为 `/tmp/dnf-cache/wukongim-stable-aaa08530d5caaef3/pubring`，保留原始路径不改写。先前同两固定镜像的 canonical mirrorlist 配置读取证明目录为 `/tmp/dnf-cache/wukongim-stable-5702a8a51b8fa885`；对应 shipping cache relative parent 是 `dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring`。路径推导与该实际配置结果吻合：libdnf 将选中的仓库 URL SHA256 前 8 bytes 加在 repo id 后，再以 `pubring` 作为 GPGME home。此上游解释不声称与发行版补丁字节相同。[官方 libdnf 0.69.0 源码](https://github.com/rpm-software-management/libdnf/blob/0.69.0/libdnf/repo/Repo.cpp#L1266)

最小建议：inventory 仅豁免 **上述 canonical 精确父路径 + 五名集合 + lstat socket**；不加入 file:// hash、不使用宽 regex、不删除 socket 或 kill agent 取绿。其余路径或类型按原 guard 拒绝；普通文件同名仍按 regular/nlink/size 检查。增加有界 escaped relative path、mode/nlink/size 的拒绝诊断。现有 metadata hash/inode/atime 读复用证明、大小总量、native signature checks、客户端命令和 49 案例保持。[精确计算收据](path-contract.json)、[先行失败矩阵](failure-matrix.md)。

v4 限定终止本任务 public-import agent 后，节点仍存在且 inventory 仍拒绝；诊断只移除已记录五 socket 后，原 inventory 接受，普通文件所记录的 type/mode/nlink/size 不变。这是机制差分，删除/终止不是建议修复。8 个 owned 容器最终零残留，无网络或卷资源创建；本次临时 TEST ONLY 私钥仅在容器 tmpfs，成功探针均在 Repo.load 前销毁，所有失败容器亦销毁。没有读取生产 private/Keychain/NAS，也没有上传或发布。

边界：原 CI run `39616623120` / job `119279318689` 在第 24 案例、command 167 报 unsupported file，23 pass、25 未达；上一步真实 DNF 下载及签名验证成功。但原 CI 没记录该文件 stat 且正常清理，因此不能把本地文件名追认为原 CI 的具体文件。当前重放采用 file:// 独立空 metadata（非原 HTTPS mirrorlist、无产品 RPM 下载），只证明同一已固定 DNF/libdnf/GPGME 路径的匹配机制；不证明完整 49 或 cache-repeat 已通过。

探针历史原样保留：旧 Python `import_repo_keys` callback 接口失败；首个 signed fixture XML namespace 失败；v2 Rosetta argv ownership 断言失败。v3 的 `after_agent_exit` 阶段未真正终止 agent，只可用作 load/stat 证据；v4 对 exact-owned pid/cmdline 逐项确认后才记录已终止状态。后续结果没有覆盖这些失败。

重放入口为 `python3 run-owned-probe.py rocky replay-signed-metadata-v4.py` 或 `alma`。只使用已在本机的固定镜像（脚本 --pull=never），每命令 45s、内存/CPU/PID/只读/无网络有界、only-owned label 清理。脚本可重放但本轮不再运行；shipping 工作树保持 de43579a 干净。原始结果和清理见 [native-filetype-proof.json](native-filetype-proof.json) 与 [cleanup-summary.json](cleanup-summary.json)。
