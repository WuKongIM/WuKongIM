未发现阻塞问题。探针通过真实 Engine→fake-client→SessionPool.drain→Verifier；逻辑时钟、准入回执与下一次读取屏障确定顺序，没有直接伪造验证结果。

独立解析普通及 race 终版日志：各 8 场景通过、收据一致，停止后会话与受控读取均为零。Go 版本首失败及探针自身 admissionErr 竞争保留；后者仅用 channel 修复。

机制成立：+9 秒排队、+11 秒处理与真正 +11 秒到达，在 +10 秒过期后得到相同 loss/evidence。但假传输夹具不证明历史云故障由排队造成，也不改变原 product_failure/message_loss。未复跑或访问云。

源码及证据锚见 source-binding.json、evidence-verification.json；校验清单见 archive-sha256.json。
