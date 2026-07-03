# Plugin Benchmark Baseline

This document records the internal plugin migration microbenchmark baseline.
Use it before and after changes that touch plugin dispatch, host RPC mapping,
PersistAfter, Send hooks, Receive hooks, NoPersist realtime delivery, offline
recipient detection, or plugin metrics.

## Baseline Commit

- Commit: `c6e42412a9ce test: add plugin benchmark baselines`
- Receive hook extension recorded from the 2026-06-22 working tree before the
  Receive hook commit.
- Date: 2026-06-22
- Host for recorded numbers: Apple M4, darwin/arm64
- Go: `go1.25.0`

## Benchmark Commands

Run the plugin package group:

```bash
go test ./internal/usecase/plugin ./internal/runtime/pluginhook ./internal/contracts/pluginevents ./internal/app -run '^$' -bench 'Benchmark(PersistAfter|Receive|SendMessageFromPluginReq|ChannelMessagesFromPluginReq|ClusterConfigFromSnapshot|ClusterChannelsBelongNode|ConversationChannels|HTTPForward|ListPlugins|SendPluginCandidates|BeforeSend|PluginHook|PluginMetricsObserver)' -benchmem -benchtime=3s
```

Run the channelappend plugin-related subset:

```bash
go test ./internal/runtime/channelappend -run '^$' -bench 'Benchmark(SubmitLocalNoPersistRealtimeScoped|ChannelAppendPostCommitPlugin|RecipientProcessorOfflineObserver)$' -benchmem -benchtime=3s
```

## Recorded Numbers

### Plugin usecase

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPersistAfterMessageBatchMapping/payload_128` | `71.60` | `392` | `4` |
| `BenchmarkPersistAfterMessageBatchMapping/payload_1024` | `155.1` | `1288` | `4` |
| `BenchmarkPersistAfterMessageBatchMapping/payload_16384` | `1528` | `16648` | `4` |
| `BenchmarkSendMessageFromPluginReq/payload_128` | `56.76` | `176` | `2` |
| `BenchmarkSendMessageFromPluginReq/payload_1024` | `137.1` | `1072` | `2` |
| `BenchmarkSendMessageFromPluginReq/payload_16384` | `1401` | `16432` | `2` |
| `BenchmarkChannelMessagesFromPluginReq/items_1` | `133.6` | `528` | `8` |
| `BenchmarkChannelMessagesFromPluginReq/items_16` | `1802` | `7488` | `98` |
| `BenchmarkChannelMessagesFromPluginReq/items_128` | `14259` | `59584` | `770` |
| `BenchmarkClusterConfigFromSnapshot` | `9742` | `47384` | `526` |
| `BenchmarkClusterChannelsBelongNode/items_1` | `136.7` | `256` | `7` |
| `BenchmarkClusterChannelsBelongNode/items_16` | `819.2` | `1792` | `36` |
| `BenchmarkClusterChannelsBelongNode/items_128` | `4047` | `11648` | `157` |
| `BenchmarkConversationChannels/items_1` | `39.64` | `136` | `3` |
| `BenchmarkConversationChannels/items_16` | `280.7` | `1216` | `18` |
| `BenchmarkConversationChannels/items_128` | `2027` | `9408` | `130` |
| `BenchmarkConversationChannels/items_1000` | `16144` | `72256` | `1002` |
| `BenchmarkHTTPForward/local_payload_128` | `3149` | `4184` | `58` |
| `BenchmarkHTTPForward/remote_payload_128` | `328.2` | `736` | `6` |
| `BenchmarkHTTPForward/local_payload_1024` | `3234` | `7848` | `58` |
| `BenchmarkHTTPForward/remote_payload_1024` | `371.5` | `1632` | `6` |
| `BenchmarkHTTPForward/local_payload_16384` | `8936` | `71208` | `58` |
| `BenchmarkHTTPForward/remote_payload_16384` | `1752` | `16992` | `6` |
| `BenchmarkHTTPForwardFanoutDeferred` | `68.42` | `200` | `3` |
| `BenchmarkPersistAfterCandidates/plugins_1` | `113.9` | `360` | `4` |
| `BenchmarkPersistAfterCandidates/plugins_16` | `1356` | `5864` | `21` |
| `BenchmarkPersistAfterCandidates/plugins_128` | `14248` | `45800` | `133` |
| `BenchmarkPersistAfterCandidates/plugins_1024` | `135253` | `344306` | `1029` |
| `BenchmarkReceivePluginCandidates/bindings_1` | `233.4` | `520` | `5` |
| `BenchmarkReceivePluginCandidates/bindings_16` | `3076` | `9488` | `25` |
| `BenchmarkReceivePluginCandidates/bindings_128` | `26636` | `74128` | `137` |
| `BenchmarkReceivePluginCandidates/bindings_1024` | `250413` | `562745` | `1035` |
| `BenchmarkReceiveOffline/payload_128` | `705.9` | `983` | `11` |
| `BenchmarkReceiveOffline/payload_1024` | `1013` | `2871` | `11` |
| `BenchmarkReceiveOffline/payload_16384` | `6088` | `35527` | `11` |
| `BenchmarkListPlugins/plugins_1` | `85.36` | `336` | `3` |
| `BenchmarkListPlugins/plugins_16` | `858.1` | `5632` | `18` |
| `BenchmarkListPlugins/plugins_256` | `11708` | `86017` | `258` |
| `BenchmarkListPlugins/plugins_1024` | `50208` | `344068` | `1026` |
| `BenchmarkSendPluginCandidates/plugins_1` | `107.2` | `360` | `4` |
| `BenchmarkSendPluginCandidates/plugins_16` | `1339` | `5864` | `21` |
| `BenchmarkSendPluginCandidates/plugins_256` | `28093` | `86249` | `261` |
| `BenchmarkSendPluginCandidates/plugins_1024` | `129268` | `344306` | `1029` |
| `BenchmarkBeforeSend/no_candidates` | `50.76` | `24` | `1` |
| `BenchmarkBeforeSend/one_plugin` | `419.0` | `768` | `11` |
| `BenchmarkBeforeSend/chain_4` | `1569` | `3384` | `37` |

### Plugin hook runtime

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPluginHookEnqueue` | `799.9` | `1331` | `5` |
| `BenchmarkPluginHookQueueFull` | `2263497` | `257` | `4` |
| `BenchmarkPluginHookReceiveEnqueue` | `1060` | `1352` | `5` |
| `BenchmarkPluginHookReceiveQueueFull` | `2222822` | `257` | `4` |

### Plugin event contracts

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPersistAfterCommittedClone/payload_128/scoped_0` | `22.88` | `128` | `1` |
| `BenchmarkPersistAfterCommittedClone/payload_128/scoped_10` | `51.56` | `288` | `2` |
| `BenchmarkPersistAfterCommittedClone/payload_128/scoped_1000` | `1243` | `16512` | `2` |
| `BenchmarkPersistAfterCommittedClone/payload_1024/scoped_0` | `87.18` | `1024` | `1` |
| `BenchmarkPersistAfterCommittedClone/payload_1024/scoped_10` | `109.4` | `1184` | `2` |
| `BenchmarkPersistAfterCommittedClone/payload_1024/scoped_1000` | `1182` | `17408` | `2` |
| `BenchmarkPersistAfterCommittedClone/payload_16384/scoped_0` | `1045` | `16384` | `1` |
| `BenchmarkPersistAfterCommittedClone/payload_16384/scoped_10` | `1073` | `16544` | `2` |
| `BenchmarkPersistAfterCommittedClone/payload_16384/scoped_1000` | `2190` | `32768` | `2` |
| `BenchmarkReceiveOfflineClone/payload_128/scoped_0` | `33.25` | `128` | `1` |
| `BenchmarkReceiveOfflineClone/payload_128/scoped_10` | `76.76` | `288` | `2` |
| `BenchmarkReceiveOfflineClone/payload_128/scoped_1000` | `1903` | `16512` | `2` |
| `BenchmarkReceiveOfflineClone/payload_1024/scoped_0` | `147.4` | `1024` | `1` |
| `BenchmarkReceiveOfflineClone/payload_1024/scoped_10` | `195.8` | `1184` | `2` |
| `BenchmarkReceiveOfflineClone/payload_1024/scoped_1000` | `1925` | `17408` | `2` |
| `BenchmarkReceiveOfflineClone/payload_16384/scoped_0` | `1775` | `16384` | `1` |
| `BenchmarkReceiveOfflineClone/payload_16384/scoped_10` | `1750` | `16544` | `2` |
| `BenchmarkReceiveOfflineClone/payload_16384/scoped_1000` | `3880` | `32768` | `2` |

### App plugin metrics observer

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPluginMetricsObserverSendInvoke` | `117.8` | `128` | `4` |
| `BenchmarkPluginMetricsObserverReceiveEnqueue` | `168.6` | `128` | `4` |
| `BenchmarkPluginMetricsObserverReceiveInvoke` | `161.7` | `128` | `4` |

### Channelappend plugin and NoPersist edges

| Benchmark | ns/op | B/op | allocs/op | Extra |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkSubmitLocalNoPersistRealtimeScoped` | `3301` | `5185` | `43` | `3.000 goroutine-delta` |
| `BenchmarkChannelAppendPostCommitPlugin/disabled` | `101.6` | `320` | `1` | |
| `BenchmarkChannelAppendPostCommitPlugin/enabled_enqueue` | `105.1` | `320` | `1` | |
| `BenchmarkRecipientProcessorOfflineObserver/recipients_16` | `486.4` | `256` | `1` | |
| `BenchmarkRecipientProcessorOfflineObserver/recipients_1024` | `56301` | `99888` | `19` | |
| `BenchmarkRecipientProcessorOfflineObserver/recipients_10000` | `642181` | `818682` | `64` | |

## Interpreting Regressions

- Treat sustained `B/op` or `allocs/op` growth as a stronger signal than a
  single slower `ns/op` sample.
- Re-run the same command on the same machine before declaring a timing
  regression.
- Candidate and list benchmarks intentionally include `plugins_1024`; do not
  remove those cases when changing plugin registry or selection logic.
- Receive candidate benchmarks intentionally include `bindings_1024`; keep that
  case when changing UID binding selection.
- Offline recipient observer benchmarks intentionally include `recipients_10000`
  as a local signal for large-group scanning cost. Keep it below integration
  scale so ordinary benchmark runs stay practical.
- `BenchmarkSubmitLocalNoPersistRealtimeScoped` must stay in the channelappend
  subset because command-style NoPersist realtime delivery is owned by
  channelappend, not the plugin usecase.
- If an intentional migration increases allocations, update this document in
  the same commit and explain the measured tradeoff.
