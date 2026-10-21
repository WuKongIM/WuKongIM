# 官方公开资料核对

2026-10-21 UTC 通过网页读取。没有调用 API Explorer、provider API 或控制台，也未安装/执行 SDK。下列为短摘要；可变文档不是已选定 SDK pin 或账号实测。

| 来源 | 支持的事实 / 本次边界 |
|---|---|
| [OSS PutObject](https://help.aliyun.com/zh/oss/developer-reference/putobject/) | 单 Put≤5GB；默认覆盖；forbid-overwrite 在版本控制 Enabled/Suspended 下无效；精确冲突码 FileAlreadyExists 与其它409不同；Cache-Control可写。所有真实字节仍需GET核验。 |
| [OSS GetBucketVersioning](https://help.aliyun.com/zh/oss/developer-reference/getbucketversioning) | 合法从未启用响应不含Status；Enabled/Suspended拒绝；需专门读权限。空错误响应不是无Status成功响应。 |
| [OSS GetBucketInfo](https://help.aliyun.com/zh/oss/developer-reference/getbucketinfo) | 提供Owner.ID、Name、Location、端点、ACL等事实，需oss:GetBucketInfo；其观察用于资源绑定，不代替具体对象GET。 |
| [OSS Python SDK V2 对应入口](https://help.aliyun.com/zh/oss/developer-reference/obtain-bucket-information-using-oss-sdk-for-python-v2) | 官方V2可复用；本次未安装，未选版本及依赖digest，真实transport注入方法须实现前核定。 |
| [STS GetCallerIdentity](https://help.aliyun.com/zh/ram/developer-reference/api-sts-2015-04-01-getcalleridentity) | 无请求参数，返回AccountId、Arn、IdentityType等；官方页面没有透出权限表，不据此声称任何身份必可调用。 |
| [CDN DescribeCdnDomainDetail](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describecdndomaindetail) | 指定单域，返回CNAME、Scope、状态、HTTPS状态、源站类型/地址/端口；支持domain级读权限。TLS证书链与真实回源仍需实际验收。 |
| [CDN DescribeCdnDomainConfigs](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describecdndomainconfigs) | 按域查询function/config ID/参数；不能从simulation observation_id构造真实revision。 |
| [CDN BatchSetCdnDomainConfig](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-batchsetcdndomainconfig) | 域名数×Functions≤20；ConfigId=0表示该项未成功；部分功能有service-linked-role前提。请求无已证CAS参数；响应不代表所有配置/POP原子切换。不得让日常adapter自行创建RAM角色。 |
| [ConfigId 的更新与删除](https://help.aliyun.com/zh/cdn/developer-reference/usage-notes-on-configid) | BatchSet按config ID更新；删除为DeleteSpecificConfig，不能用遗漏旧项当已删除。旧方案只有BatchSet权限，新增删除要独立审阅。 |
| [回源重写](https://help.aliyun.com/zh/cdn/user-guide/rewrite-urls-in-back-to-origin-requests) | PCRE/有序/break、最多50条，cache key不变；与忽略参数或条件回源可冲突。这支持能力上限，不提供本次尚缺的完整wire function定义。 |
| [CDN Functions 参数总表](https://help.aliyun.com/zh/cdn/developer-reference/parameters-for-configuring-features-for-domain-names) | 本次读取/search没有确认回源重写的完整functionName、顺序和执行flag编码；不由第三方示例补猜。通用Function字符串可序列化不等于目标功能参数已验证。 |
| [RefreshObjectCaches](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-refreshobjectcaches) | 提交后需依据返回task ID查状态；单次HTTP成功不代替公开目标内容校验。 |
| [DescribeRefreshTaskById](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtaskbyid) | 最多10 IDs，返回逐任务URL/type/status；Complete/Refreshing/Timeout/Canceled/Failed；官方权限为全部资源*，不是domain限定。 |
| [DescribeRefreshTasks](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtasks) | 不传精确过滤会读默认最近列表；实现宜用上面的ById，避免误把别人的成功任务当本次。 |
| [阿里云官方 Python CDN SDK 源码](https://raw.githubusercontent.com/aliyun/alibabacloud-python-sdk/master/cdn-20180510/alibabacloud_cdn20180510/client.py) | `batch_set_cdn_domain_config_with_options` 507–540使用HTTPS/RPC/action/version及Functions字符串；可复用官方序列化，不重写签名。阅读的是可变master，仅证明接口存在，不是最终版本锁。 |

检索局限：GitHub目录页面一次restricted URL；随后官方raw源码可读。GetBucketInfo的SDK链接首次工具引用错误，使用实际已读页面ref重取成功。搜索返回过第三方SDK例子，本报告未引用其字段作为依据。没有由无结果推导服务不支持；CDN wire细节保留未知。没有重新核实历史资源提案中的账号实值、备案、CNAME、证书、费用或权限。
