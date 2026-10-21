# D2 review repair v2 — failures before implementation

Same authorized seams: public provider methods and official SDK HTTP adapter, with fake credentials and sockets forbidden. No production activation or actual provider execution.

1. A `before` body may hide an unmanaged rewrite while retaining the still-current original digest. Reject before any write.
2. Hidden unknown function, changed ordered configs, changed top-level domain, missing/extra observation fields, or changed resource binding must not bypass the complete observation contract. Reject before writes. Re-hashing a hidden rule must also fail the fresh full-observation fence.
3. An observation from another explicit principal/resource must not authorize this provider instance, even when its CDN detail/configuration is identical. Keep same-resource exact managed update positive.
4. Official Core logs SecurityToken before transport when its logger, inherited package logger, or inherited root logger enables DEBUG. Every controlled operation must reject before entering credential-bearing SDK code, preserve user logger state, make no adapter call, and log none of the fixture key/token/secret. DEBUG=sdk also rejects.
5. Debug enabled before construction must reject before constructing credential-bearing SDK clients; enabling it after construction must still reject operations. Production must neither change global logging/environment nor install a logger proxy/monkeypatch. Caller must keep debug disabled for the operation; this is not per-call isolation from concurrent global reconfiguration.

All new regression methods will be saved and run against the unchanged frozen implementation first. Old HTTP wiring tests remain after-implementation additions and the original Standards process P2 remains open as history.
