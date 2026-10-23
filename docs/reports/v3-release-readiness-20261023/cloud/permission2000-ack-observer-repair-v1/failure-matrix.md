# ACK-count Top publication repair: tests first

Accepted seam: real delivery.Runtime + app onlineDeliveryObserver; public PushOwner, Recvack and Stop, with the existing collector mutex held to model the observed storage ingestion critical section. Session writes are a narrow in-memory adapter; no server/network or verifier is mocked. The runtime also uses the real app RuntimeObserver, so the test does not claim all unrelated Top observation callbacks are lock-free.

All planned failure modes before implementation:
1. A committed pending ACK cannot complete Recvack while Top ingestion/control holds its mutex.
2. A new PushOwner cannot reach the session writer while its ACK reservation publishes under that mutex; after it reaches the writer, Recvack must progress even if the unrelated owner-push terminal observation still waits.
3. Current values (including explicit zero) are omitted/stale in periodic Top samples; historical samples change retroactively.
4. clone loses the current ACK value, shares live mutable value, or resampling a clone resurrects stale map state.
5. Runtime Stop/reset does not publish zero; restarting and another push/ack loses exact counts.
6. Concurrent current-value updates, sampling and cloning race or corrupt the gauge; after all writers join, a final explicit zero must survive sampling and clone resampling.
7. Timeout assertions leak blocked goroutines/runtime workers: unlock ingestion first, join every caller, then Stop with a bounded context even on RED.

Two lock regression subcases and Stop/reset/concurrent sampling are integration-tagged because they create goroutines and use elapsed deadlines. Snapshot/clone sequence remains fast default-tier. Original code must compile and fail specifically on bounded progress, not an invented API. No runtime ACK ordering, admission limits, parser/verdict gates or payload semantics will change. A mechanism fix is not proof of historical message-loss causality or 2,000 SEND/s acceptance.
