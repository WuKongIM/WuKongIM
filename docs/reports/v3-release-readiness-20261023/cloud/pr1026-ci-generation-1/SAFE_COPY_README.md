# Finite public copy of PR 1026 first CI evidence

This is a selected copy of frozen historical CI evidence. It is not a fresh
execution, a replay of the cloud loss, or a complete artifact bundle.

The original self-chat report contains client message numbers, sender/channel
identities and payloads. That report and its original ZIP are intentionally
excluded. Their verified byte counts and SHA-256 digests, together with the
ZIP's already verified complete member metadata, are retained in
`excluded-original-references.json`. Other safe extracted members and the
original download receipt remain byte-identical. All original local source
files remain unchanged. The separately noted 230 MB artifact was not downloaded
or verified and is not included here.

`SOURCE_PUBLIC_ALLOWLIST.txt` and `SOURCE_SHA256SUMS` are unchanged source
references that intentionally list excluded originals. They are not a complete
manifest of this selected copy. The source README is historical source material;
the enclosing `COPY_BATCH5_SHA256SUMS` describes actual files in this batch.

`extract-safe-self-chat-summary.py` verifies the exact frozen report bytes and
SHA-256 before extracting only the parent-approved root result, hash-slot count,
fixed production_default authentication-policy enum, suppression duration, and
case node count/result/invalid-auth refusal/restart count plus ACK, RECV and
history record counts. It emits no arbitrary strings, payloads, raw user/channel
identity, ClientMsgNo, MessageID or sequence. `safe-self-chat-summary.json` is the
repeatable output. No CI job or product test was rerun for this transform.

Repeat with the original local report, intentionally absent from this archive:

```sh
python3 extract-safe-self-chat-summary.py --original-report /path/to/original/self_chat/report.json --output /tmp/safe-self-chat-summary.json
```
