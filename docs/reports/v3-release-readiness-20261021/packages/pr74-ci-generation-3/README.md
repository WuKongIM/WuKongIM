# PR #74 — generation 3 final CI audit

Exact head `f2c37e92b09bcd11599d31449bbca5de0ac52c25`; actual checkout `3ca7c376fc9cef29f0530886c893dd82a7609bf4`; both complete trees are `11ead51354aecc30087202959eeb09028617387f`. API identities, checkout logs and the driver receipt agree. Both runs are their first automatic attempt for this head.

| Gate | Exact-head result |
| --- | --- |
| [Pages 39602187472](https://github.com/WuKongIM/packages/actions/runs/39602187472) | Success: 501 tests in 120.464s, no skips; 4 contract tests; deployment skipped. |
| [Native 39602187505, original job 119229193510](https://github.com/WuKongIM/packages/actions/runs/39602187505/job/119229193510) | Success: 7 source metadata + 10 local signing + 6 bootstrap + 31 candidate + 8 transport cases; full signed repository build/sign/verify/install passed. |
| [Stable clients job 119229193899](https://github.com/WuKongIM/packages/actions/runs/39602187505/job/119229193899) | Failure: 23 of 49 cases started; 22 passed, the 23rd failed; 26 not reached. |

The first failure is `rocky-linux-9-A-to-B-request-barrier`, download command index 161, exit 1 in 1.161s, not a timeout. The bounded stderr says `wukongim-3.0.0-1.x86_64: Cannot download, all mirrors were already tried without success`. HTTP serial 24 loads A's mirrorlist, repomd/signature and metadata, then fetches B's mirrorlist and requests A version 3.0.0 beneath B's snapshot. Four payload requests return 404. The executed shell first runs `dnf makecache --refresh`, then a separate `dnf install --downloadonly`; this establishes a two-command boundary and does not prove mirrorlist changes inside a single DNF transaction. See first-failure.json, first-failure-stderr.txt and first-failure-requests.json. Final repair design is outside this read-only audit.

Both baseline generations completed real download/bootstrap and the full original installed CLI gate on Ubuntu 24.04, Debian 13, Rocky Linux 9 and AlmaLinux 9. Separate clients-A.json/clients-B.json retain all eight installed receipts (version/help/bench_validate/db_query/migrate_diagnose/invalid_inputs/read_only_data). Identity is the fixed source `fc18221282f289088ec9b4282fda8e0bfc67a3b8`, TEST ONLY versions 3.0.0/3.0.1, bootstrap versions 1.1.1/1.1.2, Go 1.25.11. Build identity `release` exercises the unchanged gate; these are explicitly TEST ONLY builds, not a production source Release or attestation.

Ubuntu and Debian A-to-B barriers actually used retained SHA512 by-hash objects with HTTP 200 and completed body writes. Their cache first/repeat/B-target and all executed negative cases passed; repeat receipts include nonempty same-inode/content metadata with atime advancement. The full 49-case suite remains failed. Rocky cache/negative cases, Alma barrier/cache/negative cases and later cases were not reached.

Final Docker cleanup is verified with no errors and zero containers/networks. Reader cleanup removed 51 containers and one network; final cleanup removed three scoped cache helpers (54 total named container attempts). All three cache cleanup receipts report empty remaining files. Fixture secret-tree removal is true. These are exact-run claims, not global daemon cleanup claims.

The exercise step ran 12:46:26–13:06:28 UTC; the job ended 13:06:32Z within its 55-minute driver/60-minute job limits. Complete native-original logs are archived losslessly as gzip with round-trip SHA recorded. Public artifact ZIP digests were recomputed against all five GitHub API digests and members passed CRC; only public JSON/JSONL members are retained.

Preserved read errors: one artifact EOF recovered on read 2; in-progress job logs returned HTTP404; final job-log first read returned EOF, then read 2 succeeded. The last raw stderr includes a short-lived signed download query, so it is preserved locally with mode0600 and excluded from the public archive; its digest and redacted public copy remain. None of these transport read errors are workflow retries or substitutes for the native failure.

No source edit, dispatch, rerun, cancellation, merge, publication, cloud or secret access occurred in this audit. Prior generations remain unchanged. Archive exactly ARCHIVE_ALLOWLIST.txt payloads plus ARCHIVE_ALLOWLIST.txt, SHA256SUMS and archive-sha256.json. The latter three are not recursively self-hashed.
