# First expired offline inspection limits

- Input evidence/logs or original DB must not change: hash bytes and mtime_ns before/after. Never open original DB; only PRIVATE0700 copies.
- Only the first unique ordered expired sample (worker2/ordinal314) is admitted; all other4 identities are for safe classification only, not DB query scope.
- Fixed original message inventory must be36files/66,553,406bytes; mismatch stops before copy/query.
- ReadOnly CLI/codec source must be byte-identicald349→c495 and exact newc495CLI hash must match; otherwise stop, no fallback.
- Copy corruption, failed query, invalid JSON/uint64/mismatched ClientMsgNo/nonunique/truncated rows stay unknown. No retry or expanded scan.
- Exactly at most3 sequential queries,10s each,total90s,GOMAX1,exact channel+clientno,limit100, no payload/UID projection. SQL/raw results/errors stay PRIVATE0600.
- No float conversion of uint64 MessageID/seq. Unique durable row plus reconstructed ClientMsgNo fingerprint qualifies ID binding only; not historical online receipt.
- Warning matching must use the exact same-run MessageID and seq, with raw identity confined private; same group/window alone is not causal association.
- CLI processes must join on completion/timeout; no service/profile/extra request/cloud action.

- This layer is a proposal only. No DB copy, open, query or process happens before explicit root review. Missing warning association remains unknown until approved durable-ID proof.
