# Deployment and measured-start handoff — exact cee5

The unique [run34/attempt1](https://github.com/WuKongIM/WuKongIM/actions/runs/39637426411) completed successfully at 2026-10-21 17:06:15 UTC. Deployment39639281581 activated at17:02:39 UTC:3 service nodes,256 healthy slots,0 pending tasks,3 workers and7/7 targets. Actual top/deployment checkout logs both identify cee5d51f325fb84033060f546337d326110adae5.

The actual handoff/run-start receipt identifies generation1, the hash of the exact rehearsal Lease ID, and **2026-10-22 01:05:48.909224454+08:00 →03:05:48.909224454+08:00** (two hours). The source-bound code waits all three workers' traffic readiness and successful first global grant before writing this receipt. The previously fully verified bundle YAML matches the exact source config SHA and specifies10000 synchronized online users, zero completed sync coverage, and2000 SEND/s. This is typed start-barrier evidence, not an independent live counter or sustained-throughput measurement. No2h result/formal pass has yet been observed.

The immutable budget is H1351044800/S1201044800 micros with new-request prior0; Quote reserve74477600micros. The old task reserve148955200micros remains separate; reserves are not invoices. Acquire resources, deployment hosts/disks, outcome/readiness, source/bundle, selector and encrypted envelope identities match. Ciphertexts were not decrypted, and no private key was read.

The deployment observer initially compared an unprefixed Lease digest to a prefixed deployment digest and stopped with AssertionError. `handoff/observer-correction.json` retains that observation error; the corrected comparison follows exact-source `deploymentDigest`, without modifying any original bytes. The new pre-clock parser accepted the real empty Job input: valid=true, Job=0, no invalid/missing properties; raw104-byte stdout digest equals the previous run33 reproduction input.

This archive contains only explicit public JSON, bounded source excerpts, actual checkout receipts, download checksums and metadata. Raw logs, ZIPs, ciphertexts, SSH configuration and binaries remain excluded. The full prior bundle/Quote/Acquire archive is referenced by manifest digest and not recopied.

Monitoring handoff: last own snapshot poll-12; root-poll-01/02/03 belong to root. Top and all4 child runs are terminal, but remote rehearsal remains under the existing safety finalizer. No dispatch/provider/SSH/workflow change was performed by this observer. Scheduled finalization must establish terminal result and exact zero inventory before any success conclusion.
