# SDK selection

Public PyPI and installed release sources were inspected, without provider calls. Selected OSS V2 1.4.1, official Core 2.16.1, CDN 3.8.8 and STS 3.1.3. All 15 transitive packages have exact versions and published SHA-256 hashes. A clean --only-binary --require-hashes installation succeeded.

The initially inspected CDN 9.2.0 / STS 1.2.0 with Tea OpenAPI 0.4.6 use Darabonba global sessions; that selection was rejected for instance isolation. Core 2.16.1 exposes an AcsClient-owned session, auto_retry=False, explicit proxy and native allow_redirects=False. The library mounts only an instance transport and retains official signing/response handling. It never patches process globals or discovers credentials.

Sources: https://pypi.org/project/alibabacloud-oss-v2/1.4.1/ ; https://pypi.org/project/aliyun-python-sdk-core/2.16.1/ ; https://pypi.org/project/aliyun-python-sdk-cdn/3.8.8/ ; https://pypi.org/project/aliyun-python-sdk-sts/3.1.3/ .

CDN function fields verified against the official 2026-07-28 function catalog: https://www.alibabacloud.com/help/en/cdn/developer-reference/parameters-for-configuring-features-for-domain-names . Literal PCRE, encoding/query interpretation and provider string limits are not live-verified. Static candidates and SDK readback are never production activation evidence.
