# 阿里云对象存储接入只读补查

15:30–15:40Z 利用现有Chrome登录态读取CDN概览/域名清单。当前2个域名中没有 packages.githubim.com；CDN按流量计费，各区域表显示后付费、无可抵扣资源包；静态HTTPS按量小时出账，页面显示当月免费额度仍在抵扣。仅保存归一化相关事实，未归档账户截图或无关资源数据；这不是实际报价或未来费用承诺。

15:34:46Z本机DNS解析返回 packages.githubim.com CNAME → wukongim.github.io，TTL 600；权威NS为dns7/dns8.hichina.com。packages CAA查询跟随CNAME，目标CAA包含letsencrypt.org；apex CAA查询无answer。这不是DNS控制台record ID、所有resolver收敛、未来CDN CAA或已签发证书证明。

RAM角色页再次只显示骨架/空主体，导航和一次reload后仍未取得角色清单，不能断言回源角色不存在或已有权限。资源提案的IAM、备案/归属、真实OSS API可用性及报价缺口仍未关闭。

没有写云配置、DNS/TLS、权限或付款；没有读取AccessKey、生产私钥、Keychain或NAS。仅关闭本次创建的两张console页，原用户页保留。原¥100/7天资源提案仍未获采购批准。
