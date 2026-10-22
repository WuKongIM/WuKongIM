# 本次只读技术依据

- [curl --connect-to](https://curl.se/docs/manpage.html#--connect-to)：仅更换网络连接目标，不改变 TLS SNI、证书校验或应用层使用的原主机名。因此提议 canonical URL + CNAME destination override；未运行 curl probe，也未断言当前执行镜像已通过。
- [Alibaba STS AssumeRole](https://www.alibabacloud.com/help/en/ram/developer-reference/api-sts-2015-04-01-assumerole)：RoleSessionName 由调用方指定，响应含临时身份 ARN 与凭据到期时间。这支持预先约定 session 名的设计选项，不证明当前角色已存在或已授权，也不调用 AssumeRole。
- [Alibaba GetCallerIdentity](https://www.alibabacloud.com/help/en/ram/developer-reference/api-sts-2015-04-01-getcalleridentity)：返回调用者 AccountId、Arn、IdentityType 等。当前 Provider 精确比较原字段即可；不在此次方案中扩大其角色解析或权限职责。

只读官方文档，无 provider API、console 或凭据访问。具体 ARN 字节仍待独立 reviewed 输入与真实读取对照，不能由示例猜出。
