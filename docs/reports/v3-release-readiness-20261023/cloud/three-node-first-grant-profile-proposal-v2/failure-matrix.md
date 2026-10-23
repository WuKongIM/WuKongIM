# V2 once-status failure matrix (before implementation)

- Existing claim + first status with wrong source, launcher, or observed Slot node: second watcher must neither overwrite status bytes/mtime nor launch a helper.
- Existing claim + first status with unsafe owner or output path: reject safely without changing first status or creating requests.
- Simultaneous valid starts: atomic claim admits one watcher; exactly three helpers total, no second producer or status owner.
- First owner later fails a source/launcher/node guard: one skipped closed status is valid; no helper requests.
- Unclaimed failures emit only fixed closed stdout fields; no auth, URL, raw errors, or status write.
- Original stale receipt, correct node mapping, stop/cancel/join, timing, byte caps and sampler request count remain unchanged.
- Helper remains byte-for-byte V1; all tests use synthetic processes/files, no real TCP profiles or product launch.
