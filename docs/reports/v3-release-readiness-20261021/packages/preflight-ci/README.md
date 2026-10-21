# PR 67 source-preflight CI — independently verified

Exact packages head: `0ce7184bbf69472f607159f74a096e9160852843`. Both automatic pull_request runs completed successfully on attempt 1; no retry or manual dispatch occurred. Monitoring and evidence collection did not change repository files.

- Snapshot [39549984615](https://github.com/WuKongIM/packages/actions/runs/39549984615): contracts and 342 tests / 100.482 s / OK, no skips. Configure Pages, Upload complete Pages artifact and Deploy complete Pages snapshot all skipped.
- TEST ONLY repository [39549984616](https://github.com/WuKongIM/packages/actions/runs/39549984616): new native metadata stage and existing complete unsigned build → sign → verify → install stage all actually succeeded. The full repository step ran 05:32:18–05:36:43 UTC and ended `native-package signed repository container validation passed`. Its existing trusted source is `ed7e67d8f781b4f92872e42116c11b304fe03b5c`; the installed test RPM is `0.0.0~SNAPSHOT_ed7e67d-1.x86_64`, not a product release candidate.

Both checkout logs show synthetic merge `4e73d6fbeb255d1206409537d00f1d76eb151623`. GitHub Git-commit API records prove its parents include the exact head, and its tree `ccfaa2893b024a61902ce1d3bf6dd35d9f38eb99` equals the head tree. Both runtime token-permission blocks contain only Contents: read and Metadata: read. Stable stays disabled with object_storage_required and no releases; protected manifests, keys, toolchain pins and publication/signing workflow boundaries are unchanged from base.

## Native artifact

Artifact `12321989695`, `native-source-metadata-test-only`: API size 1,267 bytes; SHA-256 `d42eff76246d803cad1c4d55a583df8630da6e488419c8fbd4ccc4dee127ded2`. Downloaded size/digest agree, ZIP CRC passes, and the only member is `native-source-metadata.json`. Producer API identity binds run 39549984616 and exact head.

All seven case verdicts independently match expected outcomes:

| Case | CLI exit | Expected outcome |
| --- | ---: | --- |
| stable | 0 | Exact 3.1.0 DEB/RPM identity |
| wrong-channel | 1 | Preview rejects a stable source |
| wrong-deb-version | 1 | DEB version mismatch |
| preview | 0 | DEB 3.1.0~rc.1-hotfix / RPM 3.1.0~rc.1_hotfix |
| wrong-rpm-version | 1 | RPM version mismatch |
| wrong-rpm-epoch | 1 | Epoch 9 rejected against 0 |
| signed-rpm | 1 | Actual RSAHEADER:pgpsig rejected |

Native tools: dpkg-deb 1.22.6 (amd64), RPM 4.18.2, GnuPG 2.4.4. The actual observed signature tag is only RSAHEADER; the refusal reports RSA/SHA256 and TEST ONLY key ID c76ffeba405096c3. Other legacy/header fields are covered separately by local CLI boundary-stub tests, not by a claim that these native tools generated every historic format.

The new tiny fixtures contain no executed/installed payload. Their source receipt is deliberately synthetic (Release ID 4242, source SHA of repeated 1s); success verifies the native metadata contract, not GitHub provenance of a real stable source Release. The separate existing complete TEST ONLY repository step does install its own snapshot packages. Production stable signing, storage, deployment, and public exact-version APT/RPM acceptance remain unfinished.

## Reproduce and archive

Run the offline checker with a new output filename:

```sh
python3 -B verify_ci.py --root . --output /tmp/packages-preflight-ci-recheck.json
```

`verified-ci.json` retains step facts; `summary.json` is the compact result. Original log ZIPs have intact CRCs and SHA-256 entries, with selected source/permission/result excerpts beside them. No private-key block or signed URL was found by the recorded bounded log scan; these credential-free CI logs contain no production signing material. This scan is supplementary, not a proof about arbitrary hidden credentials.

Copy only files in `ARCHIVE_ALLOWLIST.json`. Initial/intermediate status polls may remain in this temporary directory for observation history but are not needed for the verified archive. `SHA256SUMS` covers all allowlisted files except itself. No binary packages, private test keys, token values, temporary signed URLs or unrelated artifacts are in the selected native artifact.
