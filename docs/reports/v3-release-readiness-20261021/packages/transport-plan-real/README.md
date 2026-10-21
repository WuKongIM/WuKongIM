# 公开 beta.24 audit 的离线真实输入验证

结论：输入 bytes/身份锚核对通过；冻结 planner 的首次真实兼容验证被明确拒绝，没有产出 plan。错误为 `only digest-named RPM metadata can be immutable: repodata/filelists.xml.gz`。这是现有正式 preview 快照与新 planner digest-only RPM 元数据范围的兼容性缺口，不能称为真实输入验收通过。没有修改规则或重跑。

公开 immutable audit Release `402342684` 的 canonical tar（asset `607274043`）为 308,536,832 bytes，完整 SHA256 `71eb2b1b338742b9669ebfc7135ede8a12f73f395b3171e6375f18240d5c3a07`。receipt（asset `607274289`）为 3,743 bytes，完整 SHA256 `c156796d4559f93c600ac3196537fc0a858b9de1ae0d233bfe94fd333d7617bb`。本次重新读取 API，资产集合、数字 ID、大小、摘要均与旧收据一致，下载后完整重新散列。

六个公开控制文件从 receipt 指定的历史 packages commit `feb7f21503d35133cf23df7d7ab250c5d4129e71` 导出；每个文件的 Git blob SHA1、size、SHA256 和本地 exact commit blob 均核对。未使用当前 main 控制替代历史。audit tag 指向该控制 SHA；source Release `402354460` 的 `v3.0.0-beta.24` 注释 tag 解引用到 `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`，与 receipt 和 DEB/RPM API digest 匹配。仅读取公开元数据和公钥。

planner SHA256 `27ddc3fea6217bdb978e6e62b89f7c1647051b168aa6c1da8ef3e7ee2790acc6`，Python 3.13，环境只保留 PATH/HOME/TMPDIR/LC_ALL/PYTHONDONTWRITEBYTECODE 五项，HOME 与临时展开目录属于本收据目录。执行 exit 1、源码运行前后摘要不变、临时展开已清理，stderr 完整保留。未运行包 payload、未验签、未宣称完整 source provenance、未访问云/私钥、未生成签名或上传；已有签名 bytes 仅作为数据读取。

`input-verification.json`、`control-file-anchors.json`、公共 API 记录、`inputs.json`、`planner-command.json` 与 stdout/stderr 是复核入口。`public-audit-rpm-signing.json` 是 canonical tar 中的原始小收据；`summary.json` 列出全部非 digest 命名元数据。

大 tar 保留在本地但**禁止复制进仓库证据**。归档只复制 `ARCHIVE_ALLOWLIST.json` 内的小文件；需复算时按 `commands.json` 下载精确 asset，重新验证 size/SHA，并将 tar 放回 `inputs.json` 所在目录。`prepare-inputs.py` 的 output 必须是新文件；它核对公开锚及数据闭包，不替代官方密码学/权限/发布验收。
