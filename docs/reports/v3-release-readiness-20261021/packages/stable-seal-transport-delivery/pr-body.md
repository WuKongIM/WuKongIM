Stable offline candidates can now be bound to an external immutable audit Release and included with preview in a deterministic object-storage plan. The existing candidate archive, receipt, null IDs and signed APT/RPM bytes are preserved. Shared public entries remain owned by preview; stable RPM uses a mirrorlist pointing to one complete immutable snapshot.

The explicit stable path validates historical Git control/producer bytes, fresh source assets, attestations and native metadata, and actual candidate signatures before asset writes. Final source/main/tag/policy checks fence publication. TEST ONLY requires loopback endpoints and rejects redirects. Stable remains disabled; production workflows, manifests, keys, permissions and live hosting are unchanged.

Validation on the final reviewed tree:

- 468 Python tests passed (one existing native RPM environment skip), plus four contract tests and the complete contract script.
- The pinned Linux signing image passed 31 candidate cases plus eight transport cases, including signature checks after removal of test private material, loopback audit readback, deterministic composite output and retained-generation rejection.
- Independent review found and closed a resolution-file read race with a failing regression before the fix. All 38 protected control/producer files remain byte-identical.
- The first native attempt exited 137 with an unknown initiator; that failure is retained. The one unchanged isolated repeat and subsequent revision-bound runs passed.

This PR supplies sealing/transport capability and local validation. Actual production sealing, OSS/CDN activation and exact-version public-client acceptance remain outstanding. Automatic Linux PR checks are pending.
