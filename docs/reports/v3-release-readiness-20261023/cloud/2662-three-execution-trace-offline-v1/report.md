# Offline execution-trace aggregates: partial

The single authorized run completed in6.84s:36 actual calls (3 parsed drains,12 generators,12 decoders,9 tops), with three scheduler tops skipped after ineligible decodes. All36 direct tool processes and the parent runner joined. The3 raw traces (24,146,616 bytes) and15 metadata files retained identical SHA/size/mtime_ns/device/inode tuples before/after; tool/source/code final hashes passed. No retry or limit increase occurred.

All three parsed drains hit the fixed256MiB stdout stream cap. No parsed text was retained; processes were killed/reaped. This is a validation-budget limit, not proof of malformed input. Coverage remains prefix_or_unknown. All12 pprof generators returned0, which can represent only a valid prefix. The3 scheduler decoders returned closed profile_invalid and were not retried. Nine sync/net/syscall aggregate views passed the bounded envelope.

| Node | admitRecoveryPage overlap: count / SUM seconds | readOwnerPage overlap: count / SUM seconds | recoverPage overlap: count / SUM seconds |
|---|---:|---:|---:|
|1|1864 /117.606640|865 /36.953305|2739 /154.659630|
|2|1950 /111.297299|920 /37.553111|2882 /148.895213|
|3|2625 /168.195536|877 /37.939725|3507 /206.235079|

These synchronization-profile counts/delay sums span concurrent goroutines. Cohorts overlap; do not add them. Counts are tool records, not real RPCs; sums are not wall-clock latency or percentiles. Network views include readLoop waiting, potentially idle connections. No sampled-message or cross-node RPC link was decoded. Existing symbol projections cannot locate a particular READ's queue/network/provider/reply delay, measure total Registry work, or prove no lock wait occurred. Standard tool stackless-transition/unblocker-stack limits remain.

All actual raw profiles, full symbol stacks and errors stay private. The standard tools created auxiliary Library directories under the already0700 isolated HOME with default0755; after completion these private auxiliary modes were tightened to0700/0600 and recorded. Direct raw/derived/full-stack outputs were0600 from creation.

Tests-first history is preserved: unchanged old-envelope RED2 methods/1 error (KeyError9) is distinct from missing-new-API RED. Independent review's signal-cleanup and joined-receipt issues each gained frozen real RED before minimal fixes. Final28/28 passed in0.585s, retaining unchanged original23+4 test sources. All first logs and pre-fix source history remain. Final fields/packed and symbol-parser core are byte-identical to the reviewed old decoder. No product/source/DB/cloud mutation, optional extractor, message_loss fix, Linux performance or release qualification is claimed.
