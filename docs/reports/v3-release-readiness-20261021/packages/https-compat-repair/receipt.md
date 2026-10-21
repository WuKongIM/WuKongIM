# TEST ONLY HTTPS compatibility repair

Base: `4ba39668c892ca17eb33b165afe993ccc683161f`; only two test files changed. No commit, push, CI dispatch, Docker, production credentials, native package build or repository publication.

## Evidence and hypothesis result

Second CI failed on Debian 13 `apt-get update` with exit 100 and OpenSSL `unexpected eof while reading`. The request receipt records successful InRelease (1807 bytes) and Release (1099 bytes) responses. This receipt alone does not prove complete socket delivery.

Three hypotheses were frozen in hypotheses.json before experiments: missing TLS close_notify; body/Content-Length mismatch; certificate/hostname failure. The regression starts the actual existing serve() with only its listening address replaced by 127.0.0.1:0. It creates and destroys a temporary TEST ONLY certificate, verifies its trust and hostname, sends HTTP/1.0, and reads through TLS EOF with suppress_ragged_eofs=False.

The unchanged server failed twice with SSLEOFError UNEXPECTED_EOF_WHILE_READING, after independently confirming 200 OK, exact Content-Length and every body byte. This rules out the other two hypotheses for the reproduced symptom. The final expanded test also fails against the frozen original server (06 log).

## Repair and validation

The fixture now completes TLS shutdown before TCP shutdown. unwrap() sends close_notify and waits at most two seconds for the peer; the server always closes the socket even when peers disconnect or fail to respond. No TLS policy, trust, hostname, signing or target-version check is relaxed. The intentionally truncated disconnect response still declares seven excess bytes.

The final real TLS regression passes. It also verifies untrusted CA and wrong hostname rejection, and that the disconnect mode raises IncompleteRead with the expected missing seven bytes. The 52 directly related driver/production-validator tests pass (05 log); git diff --check passes. The four-client workflow and 49-case native target are unchanged. Temporary TLS keys were deleted by TemporaryDirectory and are not in evidence.

## Remaining boundary

This proves the local fixture protocol defect and repair with real TLS, not recovery of the Debian 13 native APT case. The authorized next CI run must confirm that, together with all 49 Linux native cases. Parent owns the separate SHA512/SHA256 by-hash investigation; this delta makes no by-hash, producer, production-validator or workflow changes.
