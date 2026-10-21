结论：建议实施 stable-only 的跨保留快照 RPM payload 别名。它能补齐已证实的“两次 DNF 调用”路径缺口；不是元数据改写，也不构成公网切换已通过的证据。必须保留下列来源、冲突和验收约束。

审读基线 f2c37e92b09bcd11599d31449bbca5de0ac52c25，tree 11ead51354aecc30087202959eeb09028617387f。首失败保留于 ../pr74-ci-generation-3/first-failure.json 和 first-failure-requests.json：run39602187505/job119229193899，serial24先完整获得A mirrorlist、A repomd/签名/metadata，再获得B mirrorlist，最后B/Packages/wukongim_3.0.0_linux_amd64.rpm四次404。command160明确先makecache --refresh，再独立install --downloadonly；不能描述为同一DNF进程中途换镜像。该代22/49通过，第23项失败、其余26未执行，cleanup零库存。

最小实现：每份 stable 候选实际 P.verify_candidate 验签/闭包成功后（planner:344–358），提取其原始已签 product 与 bootstrap 两个RPM的canonical path、SHA256、size、原audit ID/member。对每个保留stable RPM snapshot base，加入所有保留stable payload的缺失路径到immutable_aliases。source指真实原档，不能伪装目标前缀archive ID；snapshot_objects只列原archive成员。可沿现有object_key=public/<URI>约定，materializer:542–551按source.member取原字节并比size/SHA，不需schema或producer改动。也不生成重签、索引或额外包bytes。

必要条件：

- 仅固定rpm/stable/el/9/x86_64/Packages的已验证RPM；product是amd64源产物/x86_64 RPM，bootstrap是noarch。不按任意archive basename合并，不纳入repodata、APT、preview、keys或bootstrap下载入口。原control/source、证书和实际验签继续全量检查。
- 新aliases与所有原snapshot_objects公开URI必须统一碰撞检查，不能只依靠现add_alias(:441–455)和aliases/entrypoints gate(:470–475)。materializer后写alias会覆盖snapshot route。同URI同SHA/size/cache可跳过或去重；异事实必拒。不同object_key不能掩盖同URI冲突。
- 同basename同bytes跨多份已验真archive必须合法，按确定顺序选择源（例如明确current优先，其余audit ID排序），retain重排不能影响结果。同版本重签RPM/bootstrap若bytes变化则旧canonical URI冲突仍拒绝：需版本bump或复用完全相同已签bytes。不能借此扩大到rotation/重签迁移。
- 语义身份以目标A的实际payload SHA/size及canonical成员为准。去重source locator恰好选B时，若B的已验证同名成员与A事实完全相同，应合法；driver不得硬要求source.audit_release_id=A。须先绑定A期望bytes，再核alias source在已验证保留stable集合且member事实一致，绝非任意放行B路径。
- 控制扩张：composer:2066–2070严格单active version、无retained/base；:1729–1765验证恰好product+bootstrap两RPM。按此已验证闭包取值并显式保持“两条且canonical”条件。S≤7 stable（8总快照须含preview），缺失跨代aliases≤2S(S−1)=84，总目标槽≤98。不对100,000个archive成员任意交叉展开；先检查集合/行数再构建派生列表。后续多版本能力另审，无需本次造通用预算框架。
- 所有来源/目标来自本次已验证且保留stable集合；不新增外部任意prefix/ID/文件输入。原member事实由提取、闭包及末尾重查绑定，勿另读未核验本地路径。

充分性与界限：A签名primary的相对Packages/A.rpm即使下一次DNF通过B base解析，别名仍返回A已签包；旧A缓存/current B具有同样解析条件，所以在A明确保留、其密钥仍受信范围内可闭合。对称A base/B payload也被union覆盖。不保留未知/已移除代，不解决rotation/revocation，不更改signed metadata，也不证明混合repomd/签名/plain metadata一定可用。旧docs/object-storage-planning.md的preview fixed-base安全拒绝限制仍成立。

新路径是原快照前缀下“缺失payload的运输别名”，不是该tar原成员。文档须说清namespace可追加已验真aliases而原snapshot inventory不变。未来uploader需先部署保留payload aliases再切mirrorlist，并处理已有404缓存；当前离线planner不得宣称CDN原子性或stable已发布。本切片不必实现云发布器。

先红矩阵建议（structural fixtures复用；真实native单独验收）：

| 失败场景 | 必须断言 |
| --- | --- |
| A/B两个真不同版本，B目录请求A product/bootstrap，反方向亦然 | 四缺失别名返回正确source/member/SHA/size；原archives、snapshot_objects、signatures不变 |
| 原snapshot对象同URI异SHA/size/cache；两个retained同名异bytes（包括仅签名不同） | fail closed，不可last-writer-wins |
| 同名同bytes、同事实不同locator，retain重排 | 合法、确定去重；driver按目标bytes验真，不误拒source选B |
| preview/apt/repodata/key、wrong arch/repo、嵌套/逃逸path、未知源ID/member | 不派生或拒绝；既有验证失败不能被alias层补救 |
| 缺少A、重复身份、超过代数/两RPM边界 | 无凭空A来源；重复/越界拒绝；合法排序确定 |
| materialization时原member替换/缺失/size不符 | 真实消费者拒绝，不从URI反推目标archive读包 |
| Rocky/Alma实际A→B barrier，flags/TLS/GPG/缓存保持 | metadata严格A；payload只允许目标A事实相同的已验证alias，HTTP200完整响应+最终下载hash |
| warm A缓存→B，缺失/损坏跨代payload | 正例真实缓存使用；负例非0+对应404/损坏响应+package-client诊断，不因TLS/Usage错误假通过 |

现driver:779–782把repodata与Packages混为“全A前缀”断言，必须拆开而非删除。只接受目标generation原payload或事实完全等价的显式运输alias。现cache-B-target(:794–797)会makecache --refresh B，单独不能证明旧A缓存/current B；作者需明确实际warm-cache barrier接缝，若增加cases先改计数契约红测并同步root，不无声改变49清单。完整真实Linux新head验收仍必需。

本次未执行测试/容器/云命令，未修改源码或受保护文件；这是方案审查，不是实现通过。两个历史保护集合的独立当前SHA比较见protected-check.json。
