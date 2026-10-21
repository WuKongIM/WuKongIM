# fc182 bundle 阶段证据

唯一子运行39581475829 attempt1成功，source/control均fc182。9个Linux amd64 ELF、110个常规文件、完整109项manifest inventory及ZIP/tar/bundle digest独立验证通过。没有执行payload。

第一次数据信息下载exit1/0B，原stderr仅保留长度与hash，原因未定；对同一已完成artifact的一次只读重新下载成功。这不是Actions重跑。所有原失败记录保留。

141,108,866字节ZIP仅保留在本机raw目录，不纳入本归档。此处不包含私钥、解密凭证或临时签名URL。

复核完整字节需要该原ZIP；运行verify-bundle-artifact.py并传入本目录artifact-metadata.json、expected-deployment-intent.json、source/control fc18221282f289088ec9b4282fda8e0bfc67a3b8及一个新的output-dir。该验证不等于报价、部署、长稳或发行验收。
