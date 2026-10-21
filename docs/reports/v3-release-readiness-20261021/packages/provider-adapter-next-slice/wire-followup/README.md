# CDN wire 与 SDK CI 接线补查

只读补充，基线仍为 `eac9d252f13ea201b405825e6303de703af08c16`。原目录5份payload及3份清单保持原字节；本代独立封存。没有访问provider/account、Chrome登录页或秘密，没有实现/测试/云操作。

## 官方 wire 缺口已补齐

阿里云 **CDN** 英文官方功能表（页面标注更新 **2026-07-28**，本次2026-10-21 UTC读取）明确列出 Function ID **225**：

| 项 | 已核合同 |
|---|---|
| `functionName` | `back_to_origin_url_rewrite` |
| `source_url` | 必填String：待匹配的URI |
| `target_url` | 必填String：回源目标URI |
| `flag` | 可选；本方案固定`break`，避免继续改写；空值会继续匹配；`enhance_break`还会处理参数，不采用 |
| `sequence` | 可选Integer，1–99，小值先执行；省略会自动从1递增。本方案应显式给出唯一顺序，不能依赖数组顺序/自动分配 |

依据是[官方 CDN 参数表](https://www.alibabacloud.com/help/en/cdn/developer-reference/parameters-for-configuring-features-for-domain-names)，读取定位390–419行；页面开头明确适用于BatchSetCdnDomainConfig与SetCdnDomainStagingConfig。此前大陆中文页/搜索未命中不代表功能不存在，本补充关闭“字段名未知”项。未用第三方、VOD产品或SDK通用string猜字段。

最小**未执行示例**见 `wire-example.json`：`DomainNames=example.com`，`Functions` 是JSON编码字符串，包含这一function及4个functionArgs；source为`^/repo$`，target为演示对象`/fixture/site/repo`。语义sequence=1在通用argValue容器中编码成十进制字符串`"1"`；这沿用 API 的字符串参数容器，不把整个Functions改为SDK对象数组。更新既有规则时另加真实读回的`configId`，示例没有伪造任何生产ID。[BatchSet请求合同](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-batchsetcdndomainconfig)、[官方Python SDK](https://raw.githubusercontent.com/aliyun/alibabacloud-python-sdk/master/cdn-20180510/alibabacloud_cdn20180510/client.py)的507–540行确认Action/Version `BatchSetCdnDomainConfig` / **2018-05-10** 和Functions字符串。

**仍未关闭的限制**：单域回源rewrite最多50条、PCRE及非递归break、cache key不变；单次域名数×Functions≤20。sequence范围99不是可绕过50条的配额。现有10k精确URI表必须等价编译到这个边界；已存在规则/相同priority冲突、真实PCRE/参数长度上限、跨批次短暂状态与读回仍须验证，不能据文档称云已跑通。原生或测试替身通过也不能替代真实域名功能是否已开通的观察。[重写限制](https://help.aliyun.com/zh/cdn/user-guide/rewrite-urls-in-back-to-origin-requests)

最窄实现约束：只输出这一函数的封闭规则集，显式sequence与flag；按整份真实相关配置检查priority及管理边界；任何无法等价表达、未知配置冲突或限额超出都不apply。原方案的无EdgeScript/无新平台/不盲写/public映射要求保持。文档没有赋予新增删除权限、CAS或全POP原子性。

## 原7文件 scope 确实漏了必需的 CI 接线

基线 `.github/workflows/pages.yml:45–49` 只执行`tests/contract.sh`和系统Python的`unittest discover tests/test_*.py`，没有安装任何新SDK。native workflow也没有这套SDK依赖；不能因为ubuntu宿主偶然预装而算验证。若将新test放默认发现路径并`skipUnless(SDK)`，Pages可全绿却根本没运行adapter；若直接import，则会使既有默认套件缺依赖报错。

建议只补 **1个独立无凭据CI workflow**，scope从7改为 **8文件**，保留Pages/生产publisher和native signing image不变：

- 新 `.github/workflows/object-storage-provider-contract.yml`，自然PR及main push，paths含自身、两个新helper、对应tests和`requirements-object-storage-provider.lock`。不得漏掉lock-only修改；不加dispatch、Environment、secret、OIDC或任何云权限。`contents: read`，checkout复用已核40hex pin，persist-credentials=false，有界job。
- 将原规划SDK测试文件的位置改为 `tests/provider_sdk/test_aliyun_object_storage_provider.py`（目录不加`__init__.py`），通过专用job显式discover；原Pages顶层默认发现不会递归收它。**这不是skip成功**：SDK测试属于公开命名的另一个必须通过的job，报告分开列默认/SDK测试数。原规划 `tests/test_object_storage_delivery_inputs.py` 留默认tier，并在其中先写轻量workflow静态合同（触发路径/安装锁/显式测试/零skip/只读权限）；无需第9个测试文件。
- job先`python3 -m venv`，再由该venv `pip install --require-hashes -r requirements-object-storage-provider.lock`；所有传递依赖固定，导入失败、安装失败、无匹配测试都必须非0。系统Python无需装包，新SDK只是项目可选依赖，**对这个job不是可选**。输出实际Python与锁定SDK版本。
- 同一个venv显式加载SDK测试目录并执行；runner检查`testsRun>0`、成功且`skipped=[]`，不使用缺SDK时skip/continue-on-error。测试通过官方SDK真正序列化/解析路径，注入无网络transport；意外socket/真实provider调用立即失败。安装依赖所需公开包下载与测试的零provider访问分开说明。
- 保存小型receipt：checkout SHA/tree、lock SHA、SDK版本、测试名/数量/errors/failures/skips及无provider调用断言。失败也上传；不把SDK日志里的凭据/签名URL外传。原普通Pages成功不能顶替该job；合并审查必须读取其终态和实际测试收据。

此补充只纠正规划，没有创建workflow、修改测试布局或安装SDK。SDK精确版本/哈希与transport seam仍需实现前核定并先行写完整失败例。实际云资源/成本/生产authority的批准边界不变。
