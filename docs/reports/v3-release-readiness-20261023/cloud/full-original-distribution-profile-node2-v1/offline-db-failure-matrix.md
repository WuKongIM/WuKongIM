# Bounded offline database inspection failure matrix

- Original source data may mutate during a DB open: never open original DB; hash bytes and mtime_ns of original message files before/after; inspect private copies only.
- CLI may write or migrate: existing wkcli db query uses inspect.OpenStore with engine.Options{ReadOnly:true}, then pure SELECT; do not use import/export/repl or product startup. Copy can still change locally without affecting original.
- Identity reconstruction may be missing/nonunique or incompatible: accept exactly the 2 verified unique ordinary-group identities; quote SQL strings; no wildcard scan.
- Empty/missing/unavailable replica is not successful absence: retain each query exit/stderr/hash and explicit status.
- Limits may truncate: channel-bounded query limit100, require has_more=false for completeness; do not silently infer missing when truncated.
- More than one row may match: preserve all exact match IDs/sequences, classify nonunique rather than selecting one.
- Query may hang or exceed scope: maximum6 sequential existing CLI commands, each10s timeout, overall90s; terminate owned CLI only.
- Raw IDs or payload could leak: no payload/from_uid projection; SQL/stdout/stderr only PRIVATE 0600, public fingerprint/group-index and ClientMsgNo SHA256/typed message IDs/sequences/stats.
- Offline persistence could be mistaken for online delivery: report only durable rows after stopped run, not contemporaneous Manager route or RECV.
- Source codec/command could differ: record exact existing binary SHA and source revision/read-only implementation hashes.
