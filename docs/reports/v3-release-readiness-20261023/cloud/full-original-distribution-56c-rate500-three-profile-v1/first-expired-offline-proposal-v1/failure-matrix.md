# One56c expired-message offline plan: failure matrix

- Wrong source HEAD/tree/clean stamp, native CLI SHA, frozen run/source/identity/inventory pin, missing/nonregular/symlink original DB: stop before output mkdir or copy/query.
- Current original inventory must exactly equal frozen path/size/SHA/mtime mapping. Any drift stops; copies must match original path/size/SHA mapping before any CLI opens a copy. Never open originals with a database engine.
- Exactly one retained ordinary-group generation1 sample must resolve uniquely by the prior fixed initial-population hash transcription. Unsupported/nonunique fingerprint remains unknown; never query another identity.
- Any query timeout/nonzero exit/malformed output/has_more/nonunique row/client-number mismatch/out-of-contract uint64 or divergent replica ID/seq stops remaining queries without retry or scope expansion.
- Max three queries: one fixed group/client number, one per node, limit100, GOMAX1, timeout10s each, outer90s including copies/queries/owned process cleanup.85s work ceiling, TERM2s/KILL1s remain.
- Exact warning association must use one unique fingerprint->ClientMsgNo->decimal MessageID+seq and same-run canonical warning tuple within first-grant through terminal; absent/ambiguous data stays unknown. Group/time correlation alone is not an exact join.
- Canonical warning parsing remains offline, bounded to the three existing warn logs; no service/profile/API/cloud/DB-original query or new workload. Private UID/channel/client number/SQL/CLI/error/body bytes never enter public proof.
- Originals and frozen run/proposal/source remain unchanged. Any unavailable postcheck is unconfirmed rather than complete. Actual copying/querying still requires root's later explicit single-invocation approval.
