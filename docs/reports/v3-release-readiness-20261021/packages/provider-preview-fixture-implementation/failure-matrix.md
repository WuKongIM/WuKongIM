# D3 milestone 1 — pre-implementation failure matrix

Base: be32eb375e3c77dc5b3d1070fd798c281ae2d3bc; tree e319ee72e8d30f012fd5e6206d131e2b75c47a09.

This matrix and the entire process-level drill are written before implementing the export or injected-preview seam. The first run must fail at the missing exporter, not a substituted verifier. Tests use the pinned, non-root Linux signing container with no network; the existing loopback audit HTTP fixture remains reachable inside its network namespace. All key material is generated TEST ONLY and destroyed before default native verification and any public export.

| Case | Required result |
|---|---|
| Export seam absent | Actual drill RED; no claimed positive native integration |
| Native preview + data-only stable A/B | Real dpkg/rpm packages, original builder/signer/composer/default verifier; external receipt/control and canonical archive exported only after key destruction |
| Existing client export missing binaries, malformed ELF, wrong fixed source | Each rejected; no silent fallback to data-only; this tests format/source guards, not arbitrary valid-ELF provenance |
| Existing structural preview fixture | Default native verifier rejects; cannot count structural bytes as signed native output |
| Changed preview DEB/RPM, APT signatures/index, RPM metadata | Default native verifier rejects each; signatures also tested with receipt rebound to reach real GPG verification |
| Changed preview archive, external receipt, control, audit ID or control SHA | Original v2 planner rejects; no new output |
| Missing preview / missing retained archive | Original v2 planner rejects |
| Native A/B + native preview | Original v2 planner runs without verify_candidate mocks; deterministic repeat; exact current/retained IDs and false verification fields preserved |
| TEST ONLY without opt-in | Original planner rejects |
| Fresh production authentication | Original fresh verifier rejects these explicit TEST ONLY inputs; no GitHub/source provenance claim |
| Wrong expected plan digest / route limit | Original materializer rejects; no partial published output |
| Original materialization | Every object hash/size and source audit/member exact; aliases retain original provenance; generated mirror bytes and routes agree; retained generation never becomes client publication target |
| Repeated archive | Same signed input yields byte-identical canonical archive |
| Secrets / partial export / missing tools / timeout / cleanup failure | Fail closed, bounded nonzero run; known output removed on failure; no private bytes in public evidence |

Not tested or claimed: product binary execution/installed CLI, GitHub attestation truth, fresh production-auth positive, official SDK operations, live CDN route equivalence, cloud publication, public-client success or activation. No production scripts/manifests/keys/workflows change. Five-file scope: existing native candidate helper, existing native client seal helper, new process-level drill, object delivery documentation, changelog.
