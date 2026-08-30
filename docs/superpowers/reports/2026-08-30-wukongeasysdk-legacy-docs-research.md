# WuKongEasySDK 旧文档研究与迁移地图

Date: 2026-08-30

Source revision of this repository: `83d6f81871c5667c8ea9bb980b094075fa015d45`

## 结论

旧站 WuKongEasySDK 只有 **5 个内容页**：1 个跨平台概览，以及 iOS、Android、Flutter、Web/JavaScript 各 1 个“5 分钟集成”页。旧站的官方文档索引没有列出 HarmonyOS、React Native、Unity、小程序或其他 EasySDK 独立教程；因此新站应保持“概览 + 四平台”的内容边界。[官方 `llms.txt` 索引](https://wukong.mintlify.app/llms.txt)

旧站最值得继承的不是版本号或原样例，而是读者路径：

1. 安装；
2. 初始化；
3. 先注册连接、断开、消息和错误监听；
4. 连接；
5. 向个人 Channel 发送一条消息；
6. 保留回调引用，在 UI 或应用生命周期结束时移除监听。

但是，旧文档中的浮动版本、硬编码 Token、`ws://`、设备整数、CDN 文件、手动 framework、错误类型与部分 API 均不能直接复制。四个当前官方发布 tag 只能作为源码对齐证据，不能作为当前 WuKongIM v3 的可执行兼容性凭据。

## 旧站准确页面树

| 层级 | 中文标题 | 准确 URL | 页面主线 |
| --- | --- | --- | --- |
| WuKongEasySDK | 概览 | [https://docs.githubim.com/zh/sdk/easy/overview](https://docs.githubim.com/zh/sdk/easy/overview) | EasySDK 定位、五步集成流程、四平台对照代码、使用场景和平台入口 |
| ├─ iOS | 5分钟集成 iOS | [https://docs.githubim.com/zh/sdk/easy/ios/getting-started](https://docs.githubim.com/zh/sdk/easy/ios/getting-started) | CocoaPods/SPM/手动安装、Swift 初始化、事件、UIKit/SwiftUI 生命周期、发送、错误处理 |
| ├─ Android | 5分钟集成 Android | [https://docs.githubim.com/zh/sdk/easy/android/getting-started](https://docs.githubim.com/zh/sdk/easy/android/getting-started) | Gradle/Maven、Kotlin 初始化、事件、Activity/Fragment 生命周期、发送、错误处理 |
| ├─ Flutter | 5分钟集成 Flutter | [https://docs.githubim.com/zh/sdk/easy/flutter/getting-started](https://docs.githubim.com/zh/sdk/easy/flutter/getting-started) | pub 安装、Dart 初始化、事件、`StatefulWidget`/Provider 生命周期、发送、错误处理 |
| └─ Web | 5分钟集成 Web | [https://docs.githubim.com/zh/sdk/easy/javascript/getting-started](https://docs.githubim.com/zh/sdk/easy/javascript/getting-started) | npm/yarn/CDN、ESM/CommonJS/全局变量、`on`/`off`、React/Vue 生命周期、连接与发送 |

英文镜像使用完全相同的路由结构，只把 `/zh/` 换为 `/en/`。旧页顶部导航和官方索引都只显示上述四个平台。

## 共享教学结构

### 旧站概览页的实质内容

[旧站概览](https://docs.githubim.com/zh/sdk/easy/overview) 将 EasySDK 定位为轻量、事件驱动、使用现代异步 API 的跨平台 SDK，并把教学顺序收敛为“选平台 → 安装 → 初始化 → 监听 → 连接 → 发送”。它还提供了四平台的并排快照，以及“快速原型、MVP、简单聊天、学习/演示、内部工具”的场景列表。

可以继承：

- 五步视觉流程；
- 四平台入口卡片；
- 一屏内对比不同语言的“初始化、监听、连接、发送”对应关系；
- 用场景清单，但必须和当前采用阻断项一起展示。

必须重写：

- “5 分钟”只能是短教学路径的名字，不是完成时间、可上线性或兼容性承诺；
- “零配置”不成立：每个 SDK 都至少需要 WebSocket URL、UID 和 Token；
- “自动消息同步”不能代替离线同步、会话、未读、推送和多设备产品设计；
- 四端是“概念相似”，不是真正相同的类名、方法签名和生命周期；
- 当前不应再建议“不确定时先用 Web EasySDK”；应先展示 JSON-RPC CONNECT 与生产日志阻断，或引导到已有可执行证据的完整 Web SDK。

### 四篇平台页的共同骨架

四页都采用如下顺序：

1. 概述与系统要求；
2. 安装 SDK；
3. 导入入口类/模块；
4. 用 `serverUrl` / `uid` / `token` 初始化；
5. 监听 Connect、Disconnect、Message、Error；
6. 演示多个 Message 监听器；
7. 说明回调引用必须与注册时一致，并对比正确/错误写法；
8. 给出一个框架或 UI 生命周期例子；
9. 连接和发送个人 Channel 文本 Payload；
10. 原生三端再补一段自动重连与错误分支；
11. 资源链接与“下一步”。

这套顺序很适合新手，但新站还应在连接前加上受信后端签发身份材料，在发送后加上 Alice/Bob 双端验收，在结尾加上断开、移除监听、离线能力和生产门禁。

## 逐页内容与 API 地图

### iOS

Source: [旧站 iOS 页](https://docs.githubim.com/zh/sdk/easy/ios/getting-started)

旧页教学内容：

- 安装：CocoaPods `pod 'WuKongEasySDK', '~> 1.0.0'`、SPM 不固定版本、以及“下载 framework”的手动方式；
- 入口：`import WuKongEasySDK`；
- 配置/初始化：`WuKongConfig(...)`、`WuKongEasySDK(config:)`；
- 事件：`onConnect`、`onDisconnect`、`onMessage`、`onError`；
- 清理：保存 `EventListener`，用 `removeListener`或 `removeAllListeners`移除；
- 连接/发送：`connect()`、`send(channelId:channelType:payload:)`、`MessagePayload`；
- UI 模式：`UIViewController.viewWillDisappear`和 SwiftUI `onAppear` / `onDisappear`；
- 错误模式：举例认证、网络、未连接、Channel 无效和消息过大。

可迁入的教学素材：

- “丢失监听器 token 就无法精确移除”的正反例对比；
- UIKit 和 SwiftUI 两种所有权模式的小例子；
- `[weak self]` 与回主线程更新 UI 的提示；
- 认证错误与网络错误分类的意图。

不能复制的旧事实/代码：

- 旧页称 iOS 12 / Swift 5.0；`v1.0.3` 的 SPM 清单要求 Swift 5.7 并声明 iOS 13，公开 `WuKongEasySDK` 类又被标记为 iOS 15，所以实用教程下限应是 iOS 15。[Package.swift](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Package.swift#L1-L23) [公开主类](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongEasySDK.swift#L11-L70)
- `WuKongConfig` 初始化器会抛错，旧页基础示例缺少 `try`。[配置源码](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongConfig.swift#L74-L175)
- 旧页错误处理中的 `send(to:...)` 不存在；公开方法是 `send(channelId:channelType:payload:)`。[公开发送 API](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongEasySDK.swift#L61-L104)
- `authFailed`、`networkError`、`invalidChannel` 都携带关联值；旧页的无参数 `switch` 分支不可编译。[错误枚举](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongError.swift#L11-L67)
- “拖入 `WuKongEasySDK.framework`”没有发布产物支撑；`v1.0.3` GitHub Release 没有附件，不应写成可执行安装路径。[GitHub Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.0.3)
- 旧 UI 例子只移除监听，没有从真正的 SDK 所有者生命周期调用 `disconnect()`；新例子应同时说明“页面订阅”和“应用级连接”的不同所有权。

### Android

Source: [旧站 Android 页](https://docs.githubim.com/zh/sdk/easy/android/getting-started)

旧页教学内容：

- 安装：Gradle Groovy、Gradle Kotlin DSL 和 Maven，都使用 `com.githubim:easysdk-android:1.0.0`；
- 初始化：`WuKongConfig.Builder()`、`WuKongEasySDK.getInstance()`、`init(context, config)`；
- 事件：`addEventListener(WuKongEvent.*, listener)`；
- 清理：保存 `WuKongEventListener<T>`，使用 `removeEventListener`或 `removeAllEventListeners`；
- UI 模式：Activity `onDestroy` 和 Fragment `onDestroyView`；
- 异步：`lifecycleScope.launch { connect() }`与 suspend `send(...)`；
- 错误：`WuKongErrorCode.AUTH_FAILED` / `NETWORK_ERROR`，并声称 SDK 自动重连。

可迁入的教学素材：

- Activity 和 Fragment 分别在哪个生命周期移除 UI 监听；
- 具名 listener 字段与临时匿名 listener 的正反例；
- 连接状态决定发送按钮可用性的 UI 模式；
- 同一事件可有多个监听器的说明。

不能复制的旧事实/代码：

- 版本应固定到当前发布的 `1.0.3`，不是旧页的 `1.0.0`。[Maven Central metadata](https://repo1.maven.org/maven2/com/githubim/easysdk-android/maven-metadata.xml)
- 旧页导入了不存在的根包类，例如 `com.githubim.easysdk.WuKongChannelType`和 `com.githubim.easysdk.WuKongEvent`；实际类在 `com.githubim.easysdk.enums`，Message/Result/Payload 在 `com.githubim.easysdk.model`。[主类导入](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L1-L24)
- 旧页说 Kotlin 1.5+，但精确 tag 自身使用 Kotlin 1.9.0、AGP 8.1.4、Gradle 8.4、compileSdk 34 和 minSdk 21；文档可记录这个源码构建 tuple，不应将旧页的 Kotlin 1.5 当作已验证下限。[插件版本](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/settings.gradle#L1-L10) [Android 构建配置](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/build.gradle#L10-L35)
- `v1.0.3` 是进程内单例，第二次 `init` 会抛 `SDK is already initialized`；旧页没有说明切换 UID/配置的限制。[初始化约束](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L36-L82) [单例入口](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L525-L539)
- 旧 Activity/Fragment 例子移除了 UI listener，却没有定义何时 `disconnect()`；新文档必须区分 Application 级连接所有者和页面级订阅者。

### Flutter

Source: [旧站 Flutter 页](https://docs.githubim.com/zh/sdk/easy/flutter/getting-started)

旧页教学内容：

- 安装：`wukong_easy_sdk: ^1.0.0`；
- 初始化：`WuKongConfig`、`WuKongEasySDK.getInstance()`、`init(config)`；
- 事件：`addEventListener`、`removeEventListener`、`WuKongEvent.connect/disconnect/message/error`；
- UI 模式：`StatefulWidget.initState/dispose`与 Provider `ChangeNotifier.dispose`；
- 连接/发送：`connect()`、`send(channelId:channelType:payload:)`；
- 错误：认证、网络、未连接、Channel 无效和消息过大。

可迁入的教学素材：

- 不要在 `build` 里注册监听，而是在 `initState` 注册并在 `dispose` 移除；
- 保存 Dart 函数引用，避免 Widget 重建后重复收消息；
- 页面级所有权与 Provider/Riverpod/Bloc 应用级所有权的对比。

不能复制的旧事实/代码：

- 发布版本应精确固定到 `1.0.4`，不能使用 `^1.0.0`。[pub.dev package API](https://pub.dev/api/packages/wukong_easy_sdk)
- 旧页称 Dart 2.17+；`v1.0.4` 实际要求 Dart `>=3.0.0 <4.0.0` 与 Flutter `>=3.0.0`。[pubspec](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/pubspec.yaml#L1-L13)
- 旧页的无参数 `removeAllEventListeners()` 不存在；`v1.0.4` 使用 `removeAllEventListeners(event)` 清理一类事件，使用 `clearAllEventListeners()` 清理全部。[监听器 API](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_easy_sdk.dart#L161-L217)
- 旧页 `dispose` 仅移除 listener；精确 tag 另有 SDK `disconnect()` 和 `dispose()`，真正所有者必须调用它们。[客户端生命周期 API](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_easy_sdk.dart#L75-L105) [dispose](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_easy_sdk.dart#L255-L270)
- 旧页最后有一个多余的空代码块，不应迁移。

### Web / JavaScript

Source: [旧站 Web 页](https://docs.githubim.com/zh/sdk/easy/javascript/getting-started)

旧页教学内容：

- 安装：未固定版本的 npm/yarn，以及 `unpkg.com/easyjssdk@latest/dist/easyjssdk.min.js`；
- 导入：ESM、CommonJS 和 `window.EasyJSSDK`；
- 初始化：`WKIM.init(url, auth)`；
- 事件：`WKIMEvent.Connect/Disconnect/Message/Error`与 `im.on`；
- 移除：`im.off(event, callback)` 必须传入同一个函数引用；
- 框架模式：类成员绑定、`beforeunload`、React Effect cleanup、Vue `beforeUnmount`；
- 连接/发送：`im.connect()`、`im.send(channelId, WKIMChannelType.Person, payload)`。

可迁入的教学素材：

- `off` 必须获得与 `on` 完全相同函数引用的语法和正反例；
- React/Vue/Svelte 卸载时释放的框架提示；
- 将 SDK 包装在一个稳定、有明确 `start/stop` 所有权的客户端对象中。

不能复制的旧事实/代码：

- 版本应固定为 `easyjssdk@2.0.2`；npm 的 `latest` 当前也是 `2.0.2`，但文档不应依赖可变的 dist-tag。[npm registry metadata](https://registry.npmjs.org/easyjssdk/2.0.2)
- 旧 CDN 路径不存在：`2.0.2` 发布包只含 ESM/CJS `index.js`、类型、source map、README 和 `package.json`，没有 `dist/easyjssdk.min.js` 或 UMD 全局变量包。[unpkg 发布文件清单](https://unpkg.com/easyjssdk@2.0.2/?meta)
- 旧页把设备整数写成 APP `1`、WEB `2`；`v2.0.2` 是 APP `0`、WEB `1`、Desktop `2`，且浏览器默认 Web。[设备枚举](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L594-L608) [CONNECT 默认值](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L979-L988)
- 旧 `ChatManager.destroy`、React 和 Vue 例子只 `off`，没有调 SDK 的 `destroy()`；精确 tag 的 `destroy()` 还会断开 socket、清空 listener/pending request 和全局引用，新例子应调它。[销毁 API](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L856-L881)
- 旧页的具体 Chrome/Firefox/Safari/Edge 最低版本没有来自发布包 metadata 或可执行测试；新文档应使用能力要求（WebSocket、`TextEncoder`、`TextDecoder`），不要复制这些浏览器数字。

## 官方 tag 与发布包校验

| 平台 | 精确源码 | 发布包证据 | 已核对的公开入口 |
| --- | --- | --- | --- |
| iOS | [`v1.0.3` / `643848f85be70e3e3f2be22fceb86ae428b6cc38`](https://github.com/WuKongIM/WuKongEasySDK-iOS/tree/643848f85be70e3e3f2be22fceb86ae428b6cc38) | [CocoaPods Trunk API](https://trunk.cocoapods.org/api/v1/pods/WuKongEasySDK) 列出 `1.0.3`；公开 cocoapods.org HTML 可能滞后 | `WuKongConfig`、`WuKongEasySDK`、`on*`、`removeListener`、`connect`、`disconnect`、`send` |
| Android | [`v1.0.3` / `62084632cd8d1f26c751b053b0fb82d6aaa63892`](https://github.com/WuKongIM/WuKongEasySDK-Android/tree/62084632cd8d1f26c751b053b0fb82d6aaa63892) | [Maven Central metadata](https://repo1.maven.org/maven2/com/githubim/easysdk-android/maven-metadata.xml) 的 latest/release 是 `1.0.3` | `WuKongConfig.Builder`、`getInstance`、`init`、`add/removeEventListener`、`connect`、`disconnect`、`send` |
| Flutter | [`v1.0.4` / `6179251b49414401fe0eac4bfa3fec3f9b13a9fc`](https://github.com/WuKongIM/WuKongEasySDK-Flutter/tree/6179251b49414401fe0eac4bfa3fec3f9b13a9fc) | [pub.dev API](https://pub.dev/api/packages/wukong_easy_sdk) 的 latest 是 `1.0.4` | `WuKongEasySDK.getInstance`、`init`、`add/removeEventListener`、`connect`、`disconnect`、`dispose`、`send` |
| Web | [`v2.0.2` / `c59c80551944c9e5d9b4a902ebd2629d3defb2e6`](https://github.com/WuKongIM/WuKongEasySDK-JS/tree/c59c80551944c9e5d9b4a902ebd2629d3defb2e6) | [npm registry](https://registry.npmjs.org/easyjssdk/2.0.2) 发布 `2.0.2` | `WKIM.init`、`on`、`off`、`connect`、`disconnect`、`destroy`、`send`；导出 `WKIMChannelType/WKIMEvent/WKIMDeviceFlag` |

注：CocoaPods 的普通 HTML 页在本次研究时仍显示缓存的 `1.0.2`，但 Trunk API 已列出 `1.0.3` 且记录了 2026-08-27 的发布时间。版本判断应以 Trunk API 为准。

## 当前 v3 协议边界

### CONNECT 不是可执行的当前接入路径

当前仓库的 JSON-RPC codec 确实能把 `connect` 请求映射为 `ConnectPacket`，而且 `ConnectParams` 包含 `clientKey`。[JSON-RPC ConnectParams](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/protocol/jsonrpc/types.go#L90-L114) [codec 转换](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/protocol/jsonrpc/codec.go#L387-L420)

但默认 Product Gateway 组装使用开启加密的 `WKProtoAuthenticator`；在没有 `clientKey` 时，它返回 `ReasonClientKeyIsEmpty`。[默认组装](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/internal/app/wiring.go#L1212-L1218) [认证器行为](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/gateway/auth.go#L39-L50) [缺少 `clientKey` 处理](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/gateway/auth.go#L94-L133)

四个固定 tag 的 CONNECT 参数都没有生成或发送 `clientKey`：

- [iOS CONNECT 参数](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongWebSocket.swift#L995-L1025)；
- [Android CONNECT 参数](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L318-L345)；
- [Flutter CONNECT 参数](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_config.dart#L47-L56)；
- [Web CONNECT 参数](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L979-L988)。

因此，“codec 能解码 CONNECT”不等于“这四个 SDK tag 能连接默认产品 Gateway”。在 SDK 完成客户端密钥协商、并对精确服务端组合做完 Alice/Bob 端到端验收前，EasySDK 页应维持“源码对齐/规划中”而不是可执行快速接入。

### SEND / RECV 和字段形态

当前执行代码的 `SendParams` 使用 camelCase 字段，`payload` 是 Go `[]byte`，在 JSON 里表示为 Base64 字符串；RECV 也是 camelCase + Base64。[当前 SendParams](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/protocol/jsonrpc/types.go#L103-L114) [当前 RecvNotificationParams](https://github.com/WuKongIM/WuKongIM/blob/83d6f81871c5667c8ea9bb980b094075fa015d45/pkg/protocol/jsonrpc/types.go#L174-L193)

| SDK tag | SEND | RECV | 结论 |
| --- | --- | --- | --- |
| iOS `1.0.3` | camelCase，但直接发 JSON 对象 | 把 Payload 当字典解码 | 与当前 Base64 字节合同不符。[iOS SEND](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongWebSocket.swift#L1110-L1140) |
| Android `1.0.3` | snake_case 且直接发对象 | model 主要使用 snake_case，也将 Payload 当对象 | 字段名与 Payload 都不符。[Android SEND](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L152-L203) |
| Flutter `1.0.4` | camelCase + Base64 | 将 Base64 字符串原样放在 `Message.payload` | 发送形态对齐；业务层必须显式 Base64 → UTF-8 → JSON 解码。[Flutter SEND](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_client.dart#L130-L170) [Flutter RECV model](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/models/message.dart#L106-L121) |
| Web `2.0.2` | camelCase + Base64 | 尝试 Base64 → JSON，失败则保留字符串 | Payload 形态与当前代码对齐，但 CONNECT 仍阻塞。[Web SEND](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L883-L925) [Web RECV](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L1105-L1120) |

### 敏感日志

四个精确 tag 都不应直接通过生产日志门禁：

- iOS 的 JSON logger 将 `enableJsonLogging` 守卫注释掉。[iOS logger](https://github.com/WuKongIM/WuKongEasySDK-iOS/blob/643848f85be70e3e3f2be22fceb86ae428b6cc38/Sources/WuKongEasySDK/WuKongWebSocket.swift#L611-L626)
- Android 的 RECV 解析错误无条件输出完整 `Params`。[Android parse log](https://github.com/WuKongIM/WuKongEasySDK-Android/blob/62084632cd8d1f26c751b053b0fb82d6aaa63892/src/main/java/com/githubim/easysdk/WuKongEasySDK.kt#L378-L406)
- Flutter 无公开关闭开关，会记录完整请求与响应。[Flutter request/receive logging](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_client.dart#L172-L246) [Flutter raw receive](https://github.com/WuKongIM/WuKongEasySDK-Flutter/blob/6179251b49414401fe0eac4bfa3fec3f9b13a9fc/lib/src/core/wukong_client.dart#L281-L303)
- Web 无条件使用 `console.debug` / `console.log` 打印请求、响应和原始消息。[Web JSON-RPC logging](https://github.com/WuKongIM/WuKongEasySDK-JS/blob/c59c80551944c9e5d9b4a902ebd2629d3defb2e6/src/index.ts#L1025-L1107)

新文档可以展示“如何配置最低日志”，但不能宣称这些开关已解决所有泄漏路径。发布前需要上游版本或经评审 fork，并在 Release 产物与日志采集链路做实测。

## 相对当前 `docs-site` 的迁移建议

### 旧站独有、仍值得增补的教学素材

| 优先级 | 素材 | 如何迁入 |
| --- | --- | --- |
| P1 | “保存 listener 引用”正反例 | 在每个平台的生命周期段落加一个很短的“正确 / 错误”对照；不需要复制旧页整个大类 |
| P1 | 平台 UI 所有权 | iOS 补 UIKit/SwiftUI 小节，Android 补 Activity/Fragment，Flutter 补 Widget/应用级状态容器，Web 补 React/Vue/Svelte cleanup；每节同时说明何时只移除页面 listener，何时断开应用级 SDK |
| P1 | 安装语法的同等变体 | Android 保留 Groovy + Kotlin DSL；Web 可补 yarn/pnpm 的精确版本写法；不增加不存在的 CDN 或 iOS framework |
| P2 | 五步集成流程图 | 在 EasySDK 概览上用简短流程或有序列表表达“安装 → 后端 bootstrap → 监听 → 连接 → Alice/Bob 验收 → 清理” |
| P2 | 使用场景决策表 | 保留“原型/MVP/简单实时事件”作为意图，但在表中显式写出当前 CONNECT、离线、日志和兼容性阻断，不做现时采用建议 |
| P2 | 多监听器行为 | 说明同一事件可有多个 listener，页面订阅者之间不应使用全局 `removeAll*` 相互破坏 |
| P3 | 错误分类入口 | 不复制旧错误代码；用精确 tag 的错误类型给出认证、网络、超时、未连接、无效 Channel 的处理策略 |

当前新站已经覆盖了精确版本、受信后端 bootstrap、生命周期封装、发送、Alice/Bob 验收、Payload/字段阻断、日志风险与常见问题。因此上表建议应优先以简短的对照、可折叠框架片段或表格增补，避免再造一套冗长快速接入代码。

### 明确不应从旧站迁入的内容

- `~> 1.0.0`、`^1.0.0`、无版本 npm/yarn、`@latest` 或默认分支；
- 在客户端代码中硬编码 UID/Token，或让浏览器直接调 Product HTTP 管理端点；
- 生产 `ws://`、容器内网地址或 URL query Token；
- iOS 12 / Swift 5.0、Flutter + Dart 2.17、未经证据支撑的浏览器最低版本；
- iOS 手动 framework 安装；
- Web `dist/easyjssdk.min.js` 与 `window.EasyJSSDK`；
- APP `1` / WEB `2` 等旧设备整数；
- iOS `send(to:)`、Flutter 无参数 `removeAllEventListeners()`、Android 错误根包 import；
- 只移除 listener 但不定义谁负责 `disconnect` / `dispose` / `destroy` 的清理代码；
- “零配置”、“自动消息同步”、“发送成功即对端收到”、“可直接加入文件传输”或“可直接上生产”之类未经证据支撑的承诺；
- 旧页“相关资源”里为所有原生平台统一链接到 JS 例子的做法；每页必须链接对应的平台仓库、tag 和包注册表；
- 旧协议链接 `pkg/jsonrpc/wukongim_rpc_schema.json`，该路径在当前 WuKongIM 仓库已不存在；当前执行代码、类型和实验性支持矩阵比历史 schema 更有权威。

## 建议的最终内容地图

### `/sdk/easy`

1. EasySDK 能力/非能力边界；
2. 当前不可执行的 JSON-RPC CONNECT 警告；
3. 五步学习流程；
4. iOS / Android / Flutter / Web 卡片；
5. 四个精确 tag、revision 与发布包；
6. 受信后端负责 UID、Token 和 WebSocket 地址；
7. Alice/Bob 验收与证据边界；
8. 何时改用完整 SDK。

### 每个 `/sdk/easy/<platform>/getting-started`

1. 源码对齐不等于运行验证；
2. 平台特有采用阻断；
3. 准确工具链前提；
4. 精确版本安装；
5. 从业务后端取得 bootstrap；
6. 用当前 tag 真实 API 初始化；
7. 先监听、再做有界连接；
8. 精确 listener 移除与 SDK 所有者清理；
9. 发送个人 Channel 消息；
10. Alice/Bob 双向验收；
11. 错误/重连/日志/离线能力的常见问题；
12. 对应平台的官方 tag、包注册表和后续指南。
