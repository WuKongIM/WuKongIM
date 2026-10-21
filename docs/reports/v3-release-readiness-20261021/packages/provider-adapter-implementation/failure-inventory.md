# D2 failure inventory and validation boundary

Approved seams: official SDK serialized HTTP/response boundary; original planner, GitHub resolvers, source preflight subprocess boundary. No real cloud, resource creation, production credentials or private keys. The initial 17 SDK and 8 input/CI tests were written before the two implementation files and failed; see first-red.json/logs. Two later v1 HTTP wiring tests supplemented coverage after initial implementation. This is an explicit deviation from the requested all-tests-first order; no retrospective first-red claim is made for those two tests.

| Matrix | Implemented coverage / limit |
|---|---|
| A production boundary | canonical resource, explicit credentials, caller mismatch, TEST ONLY composite and arbitrary authority rejection; v1 planner/fresh read positive. activate always refuses. |
| B immutable identity | historical candidate/seal/current separation, Git ancestry/blob negatives, audit ID/size/hash and immutable state; actual original stable helper reused. Whole production-shaped v2/native signature success is not demonstrated by the new fixture. |
| C drift | input/plan digest before network, source/audit/current identity comparison, dirty executor red→green, CDN before-write digest fence. No production writer composes these stages. |
| D versioning | legal never-enabled empty configuration only; empty/malformed/wrong root/namespace/Status including empty/Enabled/Suspended/unknown/extra XML rejected. |
| E writes | actual OSS SDK forbid-overwrite, content length/cache, exact conflict only, pre/post timeout one PUT, denied/other409, local/key mismatch no write. |
| F GET | streamed EOF/size/SHA/cache/encoding/interruption rejection, bounded bytes and time, safe error messages. |
| G wire | literal root/repo, retained B→A key and SHA512, priority, conflict/neighbor/query/traversal/encoding, 50-rule limit and local expression bound; cloud equivalence remains false/activation unsupported. |
| H config | 20-item batch bound, full ordered observation, unknown/unmanaged/deletion/group/drift rejection, zero/missing/duplicate/partial/timeout ConfigId and readback. Later distinct-but-wrong ConfigId red→green retained. No transaction/CAS claim. |
| I refresh | exact IDs in groups of ten, URL/type and Complete, Refreshing progression, missing/foreign/duplicate/Failed/Canceled/Timeout/deadline rejected. |
| J clients/public | original materializer/simulator/client validator unchanged; no fake client callback or new acceptance result. Existing related/default suite checks retained bindings. Origin/TLS/public four-client execution is intentionally absent with production entry unsupported. |
| K completion/authority | all SDK success receipts preserve false fields; dependency missing/zero test/skip cannot pass mandatory independent CI; no authority inference or resource management APIs. |

Test corrections preserved in raw attempts: initial region validator wrongly assumed a numeric suffix; generated request uses set_endpoint; OSS fixture needed Date and blank query values; requests adapter sees redirects already consumed by Session; OSS uploads expose an iterator; multiple-batch fixture initially reused IDs. These were SDK/fixture corrections, not relaxed cloud success criteria.
