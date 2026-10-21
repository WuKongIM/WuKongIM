# #74 Rocky 镜像 pin 最小差量独审

结论：**通过，可以在这个修复提交上运行新的 CI。** 先前的首轮 CI 在镜像准备阶段失败，49 项 native cases 尚未执行，不能计为原生验收通过。

独立核对 working diff 只有 `scripts/validate-production-package-clients.py` 一处 Rocky digest 替换：`45e07bfa...cd3ade4` → `aa1303868c5fad2c780735de874d9dafefe6743b701579eb7acd9bd8bdc523b5`。整个文件精确等于 HEAD 原字节做这一次替换，Alma、APT、选项与客户端逻辑均不变；无其他 tracked/untracked 改动。

- 原 pin 的 source-driven 首红是 Quay HTTP 404 / MANIFEST_UNKNOWN，新 pin 的同一探针为 HTTP 200 / 593 bytes，正文 SHA256、声明摘要均等于新 pin。首失败已保留，没有将历史状态解释为已证实删除或其他原因。
- 官方 Quay `9-ubi-init` index 仅有一个 linux/amd64 descriptor，其 digest/size 与新 pin 和保存的原始 manifest 完全一致；运行仍固定 child digest，不依赖 mutable tag。
- 本审另外只读获取该 child manifest 精确绑定的 3,476-byte config JSON，并核对其 SHA256 `e42ec5c806749305fa130168eacda5378b23ba608bdd904a3e79539f3f782bcd`。其中 os/arch 为 linux/amd64，版本 label 为 9-ubi-init，默认命令为 /sbin/init，构建 history 包含安装 systemd。官方保存的 init recipe 摘要一致，也列有该依赖安装步骤；这支持保留 init/systemd 测试意图。[精确 public config](https://quay.io/v2/rockylinux/rockylinux/blobs/sha256:e42ec5c806749305fa130168eacda5378b23ba608bdd904a3e79539f3f782bcd)
- 客户端仍显式执行 bash/timeout，不会因 image 默认 /sbin/init 而启动 systemd。实际系统依赖、DNF 事务、签名、cache 和 CLI 行为仍由后续 Linux 原生运行核验，metadata/history 不替代这些测试。

已有修复日志记录 34 client、17 driver tests 和 4 contract tests 通过。本轮只核对这些日志及唯一差量，未重复完整测试；没有调用 Docker、拉取 layer、运行镜像、编译、修改源码或变更云/发布配置。
