# v2先行失败库存

父任务仅授权两个driver/test文件；v1和独审证据不改。本轮不运行Docker/GPG/native。

1. 实际exercise首次docker create：daemon创建成功但CLI异常，无ID返回。必须发现本UUID label、核唯一name/ID/label、删除；外部sentinel不碰；最终查询为零。
2. 同样的network create异常；Docker查询失败/无查询/坏ID/错label/name时绝不能verified；原创建失败不覆盖。
3. DNF cachedir和persistdir相对installroot：挂载必须/tmp/client-root/tmp/dnf-cache及dnf-state；真实cache第一次非空，重复轮保存pre/post文件摘要且必须有既有metadata复用证据。空cache/只有不相关file/未读取都拒绝。
4. 新实际installed旧版本：A3.0.0包真安装，expected3.0.1，原完整acceptance identity拒绝。
5. 新实际installed缺wkcli：同一真包真安装后仅在throwaway安装容器移除/usr/bin/wkcli，原acceptance拒绝缺binary。不得替换acceptance/产品binary或关闭安装验签。

原生case由47→49；workflow计数由root更新，本agent不编辑第三文件。

6. Container-root cache ownership: host cannot set old atime/traverse private metadata/rmtree. Observe and arm in an owned pinned no-network root helper mounting only this run cache; delete cache contents with that helper after stopping readers. Prior cleanup query errors remain failures after the final query. No host sudo or global chown.
7. Fixture exporter public directories must belong to the host non-root UID:GID, so host scratch removal does not depend on UID65532 ownership. Root host is explicitly rejected.
