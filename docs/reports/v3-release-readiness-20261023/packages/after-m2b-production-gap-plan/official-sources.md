# 本轮核对的官方权限依据

- [GetBucketInfo](https://www.alibabacloud.com/help/en/oss/developer-reference/getbucketinfo)：页面更新2026-09-21；RAM/STS读Bucket信息需 oss:GetBucketInfo。支持本次对 uploader/activator 隐含只读权限的说明。
- [GetBucketVersioning](https://www.alibabacloud.com/help/en/oss/developer-reference/getbucketversioning)：页面更新2026-09-21；从未开启时响应没有Status；需要有读取版本状态的权限。Enabled/Suspended不等同从未开启。
- [DescribeRefreshTaskById](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtaskbyid)：页面更新2026-04-13；RAM Action为cdn:DescribeRefreshTaskById、Resource:*；一次最多10 IDs。不得把该读取声称为域名资源级限制。

本轮只读取文档，没有调用上述provider API。刷新聚合shape使用父任务已归档的真实观察及M2b进行中设计，不推断最终实现已通过。
原资源报价/备案/DNS/TLS/role未知项沿用资源提案，不重复报新的价格或宣称账户已配置。
