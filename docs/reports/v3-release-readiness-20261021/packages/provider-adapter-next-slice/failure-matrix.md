# 最小先行失败矩阵（建议，未执行）

按接口边界参数化；先一次写全、冻结 against-old 红灯，再实施。不是新增 GA 指标，不新增重跑 native49 的机制。

| 组 | 必须包含的输入/故障 | 可判定结果 |
|---|---|---|
| A 身份/模式 | v1 preview-only 正例；v2含保留代正例；TEST ONLY/伪成功resolution/错main/错误account/domain/任意endpoint/无执行授权 | 合法离线/身份摘要可生成；任何生产写入前拒绝失败项；不改生产禁用状态 |
| B 历史和来源 | 历史candidate/seal混淆、非祖先、producer/control/blob错bytes；audit tag/资产ID/digest错、draft或非immutable；source任一7资产/attestation/metadata不符 | 复用真实helper实际被调用；不得仅根据fixture布尔通过，不写云；不回填candidate |
| C 时序 | 验证后input/plan/file替换、当前main/source/audit/config在激活前漂移 | 精确旧hash不复用；无新CDN写；既存上传对象/观察保留 |
| D OSS资源 | 合法从未启用XML；Enabled/Suspended；空body/缺容器/未知Status/错误namespace/403/404；bucket owner/region/endpoint不同 | 仅受支持合法无Status被接受；无错误降格成never_enabled |
| E PUT协议 | 真实SDK请求字段含forbid-overwrite/length/cache/精确key；成功、FileAlreadyExists、其他409、timeout写前/写后、403 | 正常/精确冲突/未知只由GET判实际同bytes；其它错误不当同对象恢复；不无条件重试/覆盖/删除 |
| F GET/日志 | exact bytes正例；截断/过长/中断/错误SHA/cache/重定向；含Authorization/token/signedURL的SDK异常 | 全量流式校验且bounded，错对象不激活；公开receipt仅受控错误码/requestID与字节事实，无敏感原文 |
| G 路由语义 | root/repo仍preview；stable SHA256+SHA512；B-prefix A.rpm源为A；同URI同bytes去重；冲突、邻近路径、编码、穿越、recursive rewrite/顺序错、>50或长度超限 | 等价闭合集合才编译；原档/source不变；unsupported wire明确拒绝，不用通配替代 |
| H CDN状态 | exactconfig正例；未知/非受管配置；ConfigId0/重复/漏返回；需删除而无授权；跨配置组/partialtimeout/readback失败 | 不覆盖非受管/不自动增权限；真实已尝试与before/after/unknown保留；不是事务回滚 |
| I 刷新 | exact任务/URL Complete；Refreshing→Complete；missing/重复/别的任务/Failed/Timeout/Canceled/有界期限到 | 只有本次所有项完整完成才进入下一步；不以API200或Process100替代状态；不发无限重试 |
| J public/client | 真调用原入口的绑定正例；origin错/cache掩盖；合法错版本/source/build_source；缺一发行版、缺installedCLI、不同input/plan但同candidate | 沿原严格验收；original receipt hash与false provenance字段不变；最终状态incomplete，不弱化精确版本 |
| K 完成/权限 | SDK替身全部成功；旧journal声称完成；预算未批准；真实authority未接；stable→preview-only回退 | 最多报告adapter_contract_test通过；不能生产complete/授权/自动回滚；资源创建/权限/DNS/证书接口零调用 |

轻量SDK transport替身不是第二套 FakeCloud：必须截获官方SDK实际序列化HTTP请求并把官方 XML/JSON送回其真实解析器；断言AWS式误用字段、SDK隐式重试、流量/时间边界和敏感输出。若SDK不能安全注入，选明确受支持的transport seam后再锁版本，不手写签名引擎。真实资源/fresh网络/public四客户端的最终验收另等接入批准，不能从替身测试推出。
