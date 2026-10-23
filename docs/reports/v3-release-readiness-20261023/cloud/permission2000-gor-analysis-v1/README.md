# Five retained goroutine snapshots: bounded offline classification

Only five complete private debug2 files were parsed: three early cuts and node1/node2 late cuts, 27,237,389 bytes and 21,216 goroutine headers. Hash/size/mtime matched before and after; all snapshots had zero elided-frame markers and no unknown state labels. Node3 late exceeded the capture cap and its body was deleted; its counts are unknown. No CPU decoding, new profiles, endpoint, DB, build or test.

The exclusive category/state partition covers every observed goroutine exactly once. Selected source-line counts and the separately named subsets overlap and must not be added to that partition. Raw goroutine IDs, argument values, UIDs, stacks and complete callchains are excluded.

Permission read-barrier frames are all at read_barrier.go:96: early97/69/61, late227/230. The select alone does not distinguish control queue, quorum, durable-apply or cancellation. No waitApplyIdle symbol was observed in these five point snapshots; this does not prove that path never runs. Runnable is a point state, not a measured scheduling delay.

Both late snapshots show12 physical-Slot processSlot workers waiting in raftlog Save:186/submitWrite:90 for shared req.done. This is not12 independent fsync calls. The node1 late raftlog writer is in collectWriteRequests:132 runnable; node2 late is in flushWriteRequests:184 Batch.Commit(Sync), through Pebble publish:492 commit.Wait. The latter can wait for batch publication or WAL sync, which the frame alone cannot separate. A separate node2 WAL flush goroutine is in FD.Fsync:21 runnable, but no DB-file identity was retained, so it is not bound to the raftlog database. Early node3 has snapshot-directory fsync work.

Person-directory workers remain eight per node: early node1 seven membership/one completion; node2 two membership/six idle; node3 eight membership. Late node1 four membership/four completion; node2 eight membership. These are snapshot stage counts, not512 execution workers. DB coordinator submitResult:329 is a pending.done/cancel/close select; late node1 has11select+49runnable and node2 has53select there, plus two node2 callers at observeQueueDepth:316. The commit coordinator and raftlog writer are separate ownership paths.

The static raftlog collector sets one5ms deadline at first dequeue and does not reset it per arrival. A collect/select/runnable snapshot cannot establish that the5ms bound was exceeded. Timings of the profiled run remain diagnostic perturbation, not no-profile performance qualification.
