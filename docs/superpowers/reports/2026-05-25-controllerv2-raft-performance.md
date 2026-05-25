# ControllerV2 Raft Performance Refactor Results

## Command

`GOWORK=off go test ./pkg/controllerv2/raft -run '^$' -bench 'BenchmarkControllerRaft' -benchmem -benchtime=1s -count=1`

## Results

```text
raft2026/05/25 18:18:51 INFO: 1 switched to configuration voters=()
raft2026/05/25 18:18:51 INFO: 1 became follower at term 0
raft2026/05/25 18:18:51 INFO: newRaft 1 [peers: [], term: 0, commit: 0, applied: 0, lastindex: 0, lastterm: 0]
raft2026/05/25 18:18:51 INFO: 1 became follower at term 1
raft2026/05/25 18:18:51 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:51 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:52 INFO: 1 is starting a new election at term 1
raft2026/05/25 18:18:52 INFO: 1 became pre-candidate at term 1
raft2026/05/25 18:18:52 INFO: 1 received MsgPreVoteResp from 1 at term 1
raft2026/05/25 18:18:52 INFO: 1 has received 1 MsgPreVoteResp votes and 0 vote rejections
raft2026/05/25 18:18:52 INFO: 1 became candidate at term 2
raft2026/05/25 18:18:52 INFO: 1 received MsgVoteResp from 1 at term 2
raft2026/05/25 18:18:52 INFO: 1 has received 1 MsgVoteResp votes and 0 vote rejections
raft2026/05/25 18:18:52 INFO: 1 became leader at term 2
goos: darwin
goarch: arm64
pkg: github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft
cpu: Apple M4
BenchmarkControllerRaftProposeSingleNode-10         	raft2026/05/25 18:18:52 INFO: 1 switched to configuration voters=()
raft2026/05/25 18:18:52 INFO: 1 became follower at term 0
raft2026/05/25 18:18:52 INFO: newRaft 1 [peers: [], term: 0, commit: 0, applied: 0, lastindex: 0, lastterm: 0]
raft2026/05/25 18:18:52 INFO: 1 became follower at term 1
raft2026/05/25 18:18:52 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:52 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:52 INFO: 1 is starting a new election at term 1
raft2026/05/25 18:18:52 INFO: 1 became pre-candidate at term 1
raft2026/05/25 18:18:52 INFO: 1 received MsgPreVoteResp from 1 at term 1
raft2026/05/25 18:18:52 INFO: 1 has received 1 MsgPreVoteResp votes and 0 vote rejections
raft2026/05/25 18:18:52 INFO: 1 became candidate at term 2
raft2026/05/25 18:18:52 INFO: 1 received MsgVoteResp from 1 at term 2
raft2026/05/25 18:18:52 INFO: 1 has received 1 MsgVoteResp votes and 0 vote rejections
raft2026/05/25 18:18:52 INFO: 1 became leader at term 2
      25	  45238202 ns/op	   77154 B/op	     354 allocs/op
raft2026/05/25 18:18:53 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:53 INFO: 1 became follower at term 1
raft2026/05/25 18:18:53 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
BenchmarkControllerRaftStartupWithLongHistory-10    	raft2026/05/25 18:18:54 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:54 INFO: 1 became follower at term 1
raft2026/05/25 18:18:54 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
raft2026/05/25 18:18:54 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:54 INFO: 1 became follower at term 1
raft2026/05/25 18:18:54 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
raft2026/05/25 18:18:54 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:54 INFO: 1 became follower at term 1
raft2026/05/25 18:18:54 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
raft2026/05/25 18:18:54 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:54 INFO: 1 became follower at term 1
raft2026/05/25 18:18:54 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
raft2026/05/25 18:18:55 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:55 INFO: 1 became follower at term 1
raft2026/05/25 18:18:55 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
raft2026/05/25 18:18:55 INFO: 1 switched to configuration voters=(1)
raft2026/05/25 18:18:55 INFO: 1 became follower at term 1
raft2026/05/25 18:18:55 INFO: newRaft 1 [peers: [1], term: 1, commit: 101000, applied: 101000, lastindex: 101000, lastterm: 1]
       6	 208820666 ns/op	 7456309 B/op	   61556 allocs/op
PASS
ok  	github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft	4.324s
```

## Notes

- Startup benchmark seeds a snapshot at Raft index 100,000 plus a bounded 1,000-entry WAL suffix, then measures service startup, snapshot restore, suffix replay, and RawNode construction.
- Proposal benchmark waits for committed local apply semantics on a single-node cluster and therefore includes WAL append, scheduled FSM apply, statefile save, applied metadata persistence, and proposal completion.
- The output includes etcd raft logger lines emitted by the benchmarked RawNode startup path.
