# Spec v2 review — original findings closed; no new findings

Scope: complete eight-file working-copy change at `d824fe37918ee4ef22b1b1745bc02518812563e3`, with the four-file repair delta inspected against v1. All eight hashes match the v2 freeze; the other 116 tracked files remain identical. No Standards report was read.

- **Original configuration-body P2: CLOSED.** Spec requires “未知规则/需要删除旧项先拒绝” (`provider-adapter-next-slice/README.md:40`) and management checks against the complete real observation (`wire-followup/README.md:23`). `aliyun-object-storage-provider.py:266–276` now freezes the exact observation shape and binds its full body, digest, domain and resource. Lines 310 and 327–328 apply that binding before interpretation and compare the complete fresh observation before writing. Replaying the former hidden-rule probe now rejects with zero writes. Rehashed hidden rules, unknown rows, wrong domain/resource, incomplete/extra fields also reject; a legitimate managed update still uses exact ConfigId 90 and verifies readback.

- **Original late logging-guard P2: CLOSED.** The sensitive-output requirement (`provider-adapter-next-slice/failure-matrix.md:12`) is now checked before SDK construction (line 147), RPC signing (174), and every OSS entry (191, 196, 209, 243); transport retains its check. The original DEBUG/token probe and inherited parent/root/environment variants reject before adapter calls, without logging fake credentials or changing global logger/environment state. Documentation lines 176–180 accurately retain the limitation: unrelated concurrent global logger reconfiguration is not isolated.

Independent locked-SDK offline probe: **9 configuration cases and 28 logging cases pass**, exit 0, no provider sockets or real credentials. Probe source/results are retained. Original v1 findings/evidence remain unchanged. No source edits or full-suite rerun occurred.

Production activation remains unsupported; these results do not establish complete production-shaped v2/native-signature integration, live PCRE/query equivalence, public-client acceptance or a release. Those disclosed verification limits remain open outside this repair's two findings.
