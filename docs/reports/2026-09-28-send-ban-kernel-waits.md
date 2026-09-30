# Natural kernel-wait observation: the full diagnostic passed

The unchanged 5,000-Channel / 4,500-SEND/s product completed the full 30-minute
diagnostic: 8,100,000 SENDs, zero pending messages, zero permission busy,
zero transport/Channel RPC admission rejection, zero ordinary membership writes,
and zero metric-sampling errors. SENDACK P99 was 92 ms. Gateway admissions and
finished records exactly matched all 8,100,000 inputs, without shard rejection
or unfinished handler work. This is a diagnostic pass, not a claimed repair of
the preceding natural EOF or a replacement for clean R2 and three R6 pairs.

The [repeated-delay experiment](2026-09-28-send-ban-sync-window.md) established
that slow message-WAL completion is sufficient for the occupied-shard EOF. This
follow-up observes real kernel waits without injecting delay, pacing compaction,
or changing queues, durability, product configuration or business behavior.

## Probe scope and validation

The existing external I/O observer now reads only the three ready-manifest node
PIDs. Every 250 ms it examines at most 256 threads per node and retains at most
16 matching `fsync`/`fdatasync` observations per node. The syscall number and FD
are checked before and after reading `wchan` and the FD symlink. There are explicit
read-error, exited/raced-thread and overflow fields. Reentry can reuse the same
number/FD, so repeated snapshots never prove one continuous syscall duration.

The observer retains 160 recent samples and ten I/O-pressure peak pairs, under
its existing 8 MiB output cap and 40-minute lifetime. No per-sample disk writes,
new privileges, tracefs mounts or denied kernel-stack reads were introduced.
Linux arm64 syscall constants come from the installed Go toolchain. Three fixed
read-only sources add the shared ext4 journal info, delayed-allocation gauge and
session-write counter. Rounded journal averages are preserved as raw text; they
are not subtracted as interval-latency counters.

Failure contracts and parser/scope/cap tests preceded the helper. Archived RED
failed on its absent APIs; GREEN and race passed. After adding the three readable
journal sources, GREEN and race were rerun successfully. Ten reused offline
analysis contracts also passed. Two 25-Channel / 4,500-SEND/s / 15-second tool
checks each completed 67,500 SENDs with complete captures. The second check had
no proc errors/overflow, at most 20 threads/node, mean node scan 0.114 ms and
maximum 1.050 ms. Its observed WAL wait points included 84 journal waits,
14 page waits and three zero-symbol observations. Counts are sampled residency,
not counts of unique operations.

The product and harness are the previously validated original-Pebble shard/flight
diagnostic binaries. Only the external observer changed. Source and binary hashes,
context digests, pre-run plan and post-run identities are included in the assets.
The Docker Desktop guest is Linux 6.10.14-linuxkit. The task's `/lab` volume is
ext4 on `/dev/vda1`; before and after the run it had about 318 GiB available.
Host free space during the run stayed above 40,717,168,640 bytes. No OOM or guard
termination occurred. No build, trace parsing or cleanup overlapped SEND.

## Full natural outcome

`sustained-01` prepared its 5,000 Channels in 2m48.935s, then completed
1,800.012 measured seconds. The runner ended normally after 1,990.399s; only
the task container's idle sleep remained. The kernel observer returned success,
all four flight captures were complete, and every compressed trace passed scheduler
extraction. Full native decoding is retained separately for the sustained run and
both tool checks.

The driver recorded 8,100,000 SEND calls and no SEND/socket-write error. Maximum
observed scheduling lag was 140.507 ms, enqueue time 59.527 ms and socket-write
time 87.295 ms; these are driver observations, not peer receipt latencies.
Node admissions were 2,916,000 / 2,592,000 / 2,592,000 and each matched its
finished count. Final pending and all relevant public rejection counters were
zero. Successful handler completion and protocol delivery remain different
evidence: both the recorder conservation and the black-box soak passed.

## Kernel evidence and what it changes

The observer collected 7,201 samples. The union of the retained tail and selected
peak endpoints has 178 unique samples, including one sample at or after the capture cutoff, excluded below.
Below the capture cutoff, mean node scan was 0.180 ms, maximum 2.534 ms,
maximum thread count 29, and eight raced reads; no read error or bound overflow.

In that selected pre-capture union, message-WAL observations include
87 `submit_bio_wait`, 55 `folio_wait_bit_common`, three `jbd2_log_wait_commit`
and seven zero-symbol records. Message SST observations include 18 journal waits
and eight page waits. Other paths are preserved without relabeling: Controller
state/Raft files and directories, message directories and MANIFEST files.
The selected union is neither a whole-run census nor comparable to the short
startup checks as a latency distribution.

The largest retained I/O-full interval was 84.66%, about 409.53 seconds before
capture. At its first endpoint all three message WALs were observed in
`folio_wait_bit_common`, alongside SST and metadata/directory waits. The shared
journal's completed transaction count advanced only from 3,234,206 to 3,234,207
between those observations. Another selected interval, about 193.03 seconds
before capture, observed two WALs in `submit_bio_wait` at both endpoints, with
an unchanged journal count. Those repeated samples do not establish one syscall
instance, and a sampled wait symbol is not a complete kernel stack.

Upstream Linux 6.10's [ext4 sync path](https://github.com/torvalds/linux/blob/v6.10/fs/ext4/fsync.c)
waits for file data, performs journal synchronization, and may issue a device
flush. The [block flush implementation](https://github.com/torvalds/linux/blob/v6.10/block/blk-flush.c)
uses `submit_bio_wait`. Thus the observed WAL symbols are consistent with
different kernel I/O stages; attributing every wait to journal commit would be
incorrect. These upstream references explain an inference, not an exact stack
or source-identity proof for the patched LinuxKit kernel.

This run survived the selected high-pressure intervals. A high scalar I/O-full
percentage, a journal wait or a block wait alone does not establish the occupied
batch/queue accumulation that preceded the earlier EOF. Whole-VM block counters
do not identify the physical host drive's cause, and the new observer does not
measure host scheduling or firmware behavior. No failing request was reproduced
in this run, so it cannot supply that missing per-failure attribution.

## Next acceptance step

Return to the unchanged, uninstrumented product for one fresh original-load
30-minute R2 qualification, after archiving and ending this analysis. Retain every
previous failure; do not replace it with this diagnostic pass or adjust workload,
queue bounds or durability. R6 still requires three complete fresh comparisons.
No production tuning is justified by this run alone.

Artifacts and derivations are indexed by
`assets/send-ban-kernel-waits-20260928/manifest.json`. `verify_kernel.py` checks
the proc scope/bounds and cutoff; `summarize_peaks.py` preserves both endpoint
snapshots of the selected I/O peaks. Original compressed traces and raw I/O/proc
observations remain available for independent reanalysis.
