# Permission evidence layout

Each archival PR is based directly on main and stays below the existing 50-file,
1-MiB and 30,000-complete-line Review Agent limits. The original source objects,
full archive bytes and every failed observation remain retained. These historical
artifacts keep their original verdict/source scope.

| PR | Contents |
| --- | --- |
| [#980](https://github.com/WuKongIM/WuKongIM/pull/980) | Independent comparison/E2E driver, diagnostics, report metadata |
| [#981](https://github.com/WuKongIM/WuKongIM/pull/981) | Permission/append runtime, sampled diagnostics, knowledge and Changelog |
| [#984](https://github.com/WuKongIM/WuKongIM/pull/984) | Original baseline JSON and CPU/heap profiles |
| [#985](https://github.com/WuKongIM/WuKongIM/pull/985) | Original cohort and stage-diagnosis archives |
| [#986](https://github.com/WuKongIM/WuKongIM/pull/986) | Original request-timeline archive byte slice 1 |
| [#987](https://github.com/WuKongIM/WuKongIM/pull/987) | Original request-timeline archive byte slice 2 |
| [#988](https://github.com/WuKongIM/WuKongIM/pull/988) | Original main-integration archive |
| [#989](https://github.com/WuKongIM/WuKongIM/pull/989) | Current sequential diagnosis/qualification archive byte slice 1 |
| [#990](https://github.com/WuKongIM/WuKongIM/pull/990) | Current sequential diagnosis/qualification archive byte slice 2 |

Reconstruct the request-timeline archive only after both slices are available:

```sh
cat docs/reports/2026-10-01-permission-request-timeline-evidence.tar.gz.part-01 docs/reports/2026-10-01-permission-request-timeline-evidence.tar.gz.part-02 > /tmp/2026-10-01-permission-request-timeline-evidence.tar.gz
shasum -a 256 /tmp/2026-10-01-permission-request-timeline-evidence.tar.gz
```

Expected bytes 1080961, SHA-256
`002d1505d1713d415a4294fe55b31ef144f8a091f03dbe3185d2fd640eac66d3`.
The slices are not independently extractable. The existing request-timeline
manifest hashes the unchanged contents of the reconstructed archive. Its
historical local archive link names the reconstructed output. Both unchanged
full historical archives also remain in the preserved composite branch.

Reconstruct the current sequential archive after both slices are available:

```sh
cat docs/reports/2026-10-01-permission-sequential-evidence.tar.gz.part-01 docs/reports/2026-10-01-permission-sequential-evidence.tar.gz.part-02 > /tmp/2026-10-01-permission-sequential-evidence.tar.gz
shasum -a 256 /tmp/2026-10-01-permission-sequential-evidence.tar.gz
```

Expected bytes 1514844, SHA-256
`2eb068e94ce358e333bd43691d546895449a8cb907c1e12bb94f9b0b1e219c5f`.
The [manifest](2026-10-01-permission-sequential-manifest.json) hashes the 194
selected evidence files; the archive contains those files plus the manifest
itself (195 members). Source/native identities, every failed probe, all six
predetermined comparison receipts, independent reviews and exact-runtime CI
evidence are retained. Product binaries and the full CI binary artifact remain
local with recorded hashes. From the extracted archive, run
`verify-reviewed-pairs.py` and `verify-ci-500qps.py` to recompute both verdicts.
The archive was sealed before these delivery-only documentation commits.

The driver must land before runtime, followed by exact-head integration checks.
All scopes remain Draft; artifacts do not grant merge/release approval. Current
sequential acceptance still fails and remains separate from historical CI passes.
