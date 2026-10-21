# Independent transport planner negative inventory

Prepared before planner implementation; this is an independent boundary checklist, not a claim that every case has been executed. Scope is the root-approved preview/add_release offline slice of design A. Whole-site activation, stable support and native A/B switching belong to later slices.

| Boundary | Independent negative case / expected result |
| --- | --- |
| Independent anchor | Rewrite archive plus embedded plan/snapshot/signing receipts and external receipt coherently, retain original descriptor anchors: reject. Changing SHA labels cannot bless changed control-file bytes. |
| Per-generation identity | Wrong numeric audit ID, control SHA, target/source release or source SHA, archive/snapshot digest, external receipt digest, or any one independent control hash: reject. bool must not substitute for integer. |
| Narrow scope | stable/multichannel, enabled stable control, strict stable version under preview, update_bootstrap/remove_indexes/remove_payloads, missing add_release source evidence: reject. |
| Complete verification | A retained generation is just as untrusted as the current one. Do not skip its archive/control/closure validation because its digest or public path appeared before. Duplicate audit ID must not create ambiguous identity. |
| Archive safety | Canonical USTAR required, no traversal/absolute path/backslash/duplicate/missing parent/PAX/GNU/symlink/hardlink/special node/trailing bytes; independently bounded member count and actual byte size. |
| Source filesystem | Symlink/hardlink/special inputs, changed file during read, unknown/duplicate JSON properties and excessive descriptor/snapshot count/cumulative budget fail without partial output. No credentials or network side effects. |
| Structural site closure | Extra/missing site objects, incorrect status/snapshot relation, altered repo/bootstrap manifest/key bytes, missing package or old by-hash, malformed or escaping/duplicate RPM href, hash/open-hash mismatch: reject. Receipt rebuilding alone does not prove site closure. |
| Bounded decompression | Compressed and open lengths bounded; zstd/native decompressor requirements rejected before external execution for this pure-Python slice. |
| Cross-generation union | Same immutable public path with different bytes/size fails. Valid old by-hash/versioned package/digest metadata objects survive union. Input history ordering or descriptor relocation cannot change canonical plan bytes. |
| Mutable entrypoints | Packages, Packages.gz, Release, InRelease, Release.gpg, repomd.xml and its signature are mutable entrypoints; never derive permanent-cache classification solely from suffix. RPM digest metadata names must bind the actual digest. |
| Honest output | plan_only=true; signatures_verified/github_verified/cloud_verified=false. No upload, activation or deletion action. Explicit prior inventory is retention evidence, not independently verified GitHub ancestry or trusted rollback authority. |
| Output ownership | Existing output and symlink output/parent fail; output must not replace an input; failure leaves no success JSON or damage to preexisting inputs. Temporary cleanup only owns its created directory. |

Existing archive inspector/extractor and receipt verifier are trusted implementation building blocks; the new planner must reuse their contracts, not silently loosen them. Public synthetic TEST ONLY signing fixture bytes can establish consistency only. Missing cryptographic verification is an explicit boundary, not a bug in this slice.
