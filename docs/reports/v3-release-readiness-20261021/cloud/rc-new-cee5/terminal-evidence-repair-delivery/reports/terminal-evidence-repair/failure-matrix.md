# Failure matrix (written before implementation)

- Available terminal worker evidence/status omitted: fail exact byte/hash retention.
- Formal stops before capacity versus capacity actually starts: explicit stage evidence, never false required/missing.
- Worker/status missing, empty, malformed, oversized, partial, unavailable, timed out: truthful fixed states; sibling success preserved; invalid bytes not retained.
- Wrong schema/run/cut/timestamp, duplicate worker, bad generation/sequence, invalid example vocabulary, forbidden raw identity/error fields: reject.
- Terminal report bad/missing end/schema/finality, future/backward time: no wall-clock fallback query.
- Single aggregate metrics response over 4 MiB but disjoint partitions fit: preserve original selector coverage and step, retain all partitions bounded by 24 MiB shared transfer budget/8 requests/60 seconds.
- Invalid/missing/oversized one partition: incomplete while valid siblings retained.
- Shared deadline exhausted after first partition: no further network requests, explicit remaining statuses.
- Selector partition union equals original and intersections empty, including exact counter names and arbitrary suffixes.
- Error bodies/raw strings never appear in manifests/stdout/stderr; original product verdict untouched.

All new executable coverage uses the integration build tag. No cloud or product processes are started. The existing producer tests and old 16-capture contract are preserved as historical observations in offline-collector-analysis; fixture expectations will be updated only after the new failures are observed.

Additional pre-implementation scope: exact existing presence endpoint and delivery recipient/authority/push metrics are absent from the old selector. Add two priority disjoint partitions, prove names against source definitions, keep total transfer/time budget unchanged. Empty optional metric families retain explicit zero coverage without inventing a new required-family gate.
