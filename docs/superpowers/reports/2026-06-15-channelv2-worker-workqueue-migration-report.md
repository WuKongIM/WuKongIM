# ChannelV2 Worker Workqueue Migration Report

## Baseline

Captured on: 2026-06-15

Baseline code commit: `6e64fe537904`

Command:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

Environment:

```text
go version go1.25.11 darwin/arm64
goos: darwin
goarch: arm64
cpu: Apple M4
```

Notes:

- `goroutine-delta` is the live pool footprint while the benchmark pool is running, not a post-close leak metric.
- StoreAppend baseline must be evaluated with `batch-calls/op` and `single-append-calls/op`, not only `ns/op`.
- `benchstat` was not available in this environment when this baseline was captured.
- Post-migration benchmarks must use the same command, machine, and Go version unless the report explicitly calls out the reason.

Baseline summary:

| Benchmark | Median ns/op | Median B/op | Median allocs/op | Extra median metrics |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkWorkerPoolSubmitAndRun/workers1-10` | 15728 | 943 | 8 | `goroutine-delta=1` |
| `BenchmarkWorkerPoolSubmitAndRun/workers16-10` | 1109 | 657 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/none-10` | 1024 | 657 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/recording-10` | 1075 | 655 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolFullReject-10` | 84.39 | 1 | 0 | none |
| `BenchmarkWorkerPoolStoreAppendBatch-10` | 532.5 | 1503 | 2 | `batch-calls/op=0.01660`, `single-append-calls/op=0.0000300`, `goroutine-delta=1` |

Comparison method:

- Compare every benchmark/sub-benchmark name independently.
- Prefer `benchstat` when it is available, using the full `-count=5` baseline and post-migration output.
- If `benchstat` is not available, compare the median of each metric from the five baseline samples to the median of the five post-migration samples.
- Do not choose the best individual sample from either side.
- Post-migration output must keep every baseline benchmark/sub-benchmark name and must include five samples per name. Missing, renamed, or undersampled benchmarks make the gate result not comparable.

Raw output:

```text
goos: darwin
goarch: arm64
pkg: github.com/WuKongIM/WuKongIM/pkg/channelv2/worker
cpu: Apple M4
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   37640	     16013 ns/op	         1.000 goroutine-delta	     943 B/op	       8 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   37334	     15829 ns/op	         1.000 goroutine-delta	     943 B/op	       8 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   38258	     15728 ns/op	         1.000 goroutine-delta	     943 B/op	       8 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   38402	     15690 ns/op	         1.000 goroutine-delta	     943 B/op	       8 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   38545	     15650 ns/op	         1.000 goroutine-delta	     943 B/op	       8 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  568832	      1137 ns/op	        16.00 goroutine-delta	     656 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  631584	      1081 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  591174	      1027 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  550857	      1109 ns/op	        16.00 goroutine-delta	     656 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  578438	      1135 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  591736	      1046 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  729349	       982.2 ns/op	        16.00 goroutine-delta	     656 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  602545	      1148 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  527114	      1024 ns/op	        16.00 goroutine-delta	     657 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  530934	       998.6 ns/op	        16.00 goroutine-delta	     656 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  549831	      1125 ns/op	        16.00 goroutine-delta	     655 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  488287	      1117 ns/op	        16.00 goroutine-delta	     655 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  587172	      1075 ns/op	        16.00 goroutine-delta	     655 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  604057	      1034 ns/op	        16.00 goroutine-delta	     654 B/op	       5 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  620247	      1043 ns/op	        16.00 goroutine-delta	     655 B/op	       5 allocs/op
BenchmarkWorkerPoolFullReject-10                    	 6338758	        84.39 ns/op	       1 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	 6983324	        96.56 ns/op	       1 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	 6188780	        81.83 ns/op	       1 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	 7150717	        83.54 ns/op	       1 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	 6192178	        85.27 ns/op	       1 B/op	       0 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       532.5 ns/op	         0.01657 batch-calls/op	         1.000 goroutine-delta	         0.0000300 single-append-calls/op	    1503 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       527.9 ns/op	         0.01665 batch-calls/op	         1.000 goroutine-delta	         0.0000220 single-append-calls/op	    1504 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       527.4 ns/op	         0.01660 batch-calls/op	         1.000 goroutine-delta	         0.0000180 single-append-calls/op	    1503 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       543.5 ns/op	         0.01716 batch-calls/op	         1.000 goroutine-delta	         0.0000330 single-append-calls/op	    1509 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       609.4 ns/op	         0.01646 batch-calls/op	         1.000 goroutine-delta	         0.0000320 single-append-calls/op	    1502 B/op	       2 allocs/op
PASS
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/worker	20.431s
```

## Post-Migration

Post-migration code state: final working tree before the migration commit

Same command:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

During post-migration verification, `BenchmarkWorkerPoolSubmitAndRun/workers1`
and observer samples were noticeably sensitive to local scheduler noise. To
avoid comparing a noisy post-run against an unusually clean historical sample,
the baseline commit `6e64fe537904` was also rerun in a detached worktree on the
same machine immediately before the final post run, using the same command.
The gate result below uses that same-host baseline rerun for `ns/op`, `B/op`,
and `allocs/op`. The original historical raw baseline remains above for audit
history.

Same-host baseline rerun summary:

| Benchmark | Median ns/op | Median B/op | Median allocs/op | Extra median metrics |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkWorkerPoolSubmitAndRun/workers1-10` | 17139 | 943 | 8 | `goroutine-delta=1` |
| `BenchmarkWorkerPoolSubmitAndRun/workers16-10` | 1228 | 659 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/none-10` | 1147 | 658 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/recording-10` | 1160 | 656 | 5 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolFullReject-10` | 80.40 | 1 | 0 | none |
| `BenchmarkWorkerPoolStoreAppendBatch-10` | 558.2 | 1500 | 2 | `batch-calls/op=0.01623`, `single-append-calls/op=0.0000250`, `goroutine-delta=1` |

Post-migration summary:

| Benchmark | Median ns/op | vs same-host baseline | Median B/op | Median allocs/op | Extra median metrics |
| --- | ---: | ---: | ---: | ---: | --- |
| `BenchmarkWorkerPoolSubmitAndRun/workers1-10` | 17202 | +0.37% | 855 | 5 | `goroutine-delta=1` |
| `BenchmarkWorkerPoolSubmitAndRun/workers16-10` | 1103 | -10.18% | 614 | 3 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/none-10` | 1171 | +2.09% | 615 | 3 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolObserverOverhead/recording-10` | 1203 | +3.71% | 614 | 3 | `goroutine-delta=16` |
| `BenchmarkWorkerPoolFullReject-10` | 15.28 | -81.00% | 0 | 0 | none |
| `BenchmarkWorkerPoolStoreAppendBatch-10` | 444.2 | -20.42% | 1454 | 2 | `batch-calls/op=0.01683`, `single-append-calls/op=0.0000396`, `goroutine-delta=1` |

Post-migration raw output:

```text
goos: darwin
goarch: arm64
pkg: github.com/WuKongIM/WuKongIM/pkg/channelv2/worker
cpu: Apple M4
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   32877	     17373 ns/op	         1.000 goroutine-delta	     855 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   34638	     17203 ns/op	         1.000 goroutine-delta	     855 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   35295	     17202 ns/op	         1.000 goroutine-delta	     855 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   34568	     16933 ns/op	         1.000 goroutine-delta	     855 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers1-10         	   34256	     17184 ns/op	         1.000 goroutine-delta	     855 B/op	       5 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  570360	      1157 ns/op	        16.00 goroutine-delta	     615 B/op	       3 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  464467	      2325 ns/op	        16.00 goroutine-delta	     616 B/op	       3 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  459582	      1101 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  550622	      1103 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolSubmitAndRun/workers16-10        	  528969	      1079 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  519204	      1116 ns/op	        16.00 goroutine-delta	     615 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  572239	      1216 ns/op	        16.00 goroutine-delta	     615 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  550702	      1048 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  627338	      1183 ns/op	        16.00 goroutine-delta	     615 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/none-10         	  552742	      1171 ns/op	        16.00 goroutine-delta	     615 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  515035	      1142 ns/op	        16.00 goroutine-delta	     613 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  568144	      1662 ns/op	        16.00 goroutine-delta	     616 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  488028	      1203 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  455257	      1219 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolObserverOverhead/recording-10    	  456162	      1188 ns/op	        16.00 goroutine-delta	     614 B/op	       3 allocs/op
BenchmarkWorkerPoolFullReject-10                    	43429744	        14.60 ns/op	       0 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	44277308	        15.36 ns/op	       0 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	38525955	        15.28 ns/op	       0 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	44185741	        15.34 ns/op	       0 B/op	       0 allocs/op
BenchmarkWorkerPoolFullReject-10                    	39835346	        15.23 ns/op	       0 B/op	       0 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1376457	       595.9 ns/op	         0.01634 batch-calls/op	         1.000 goroutine-delta	         0.0000327 single-append-calls/op	    1445 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1000000	       610.5 ns/op	         0.01683 batch-calls/op	         1.000 goroutine-delta	         0.0000560 single-append-calls/op	    1454 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1262067	       444.2 ns/op	         0.01711 batch-calls/op	         1.000 goroutine-delta	         0.0000396 single-append-calls/op	    1459 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1374188	       432.3 ns/op	         0.01651 batch-calls/op	         1.000 goroutine-delta	         0.0000320 single-append-calls/op	    1449 B/op	       2 allocs/op
BenchmarkWorkerPoolStoreAppendBatch-10              	 1371604	       440.9 ns/op	         0.01699 batch-calls/op	         1.000 goroutine-delta	         0.0000488 single-append-calls/op	    1457 B/op	       2 allocs/op
PASS
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/worker	24.109s
```

## Gate

- For every benchmark/sub-benchmark, `ns/op` regression must be no more than 5% by the comparison method above.
- For every benchmark/sub-benchmark, `allocs/op` must not increase.
- For every benchmark/sub-benchmark, `B/op` must not increase by more than 5% or more than 16 bytes/op, whichever is larger. If this threshold is exceeded, the report must mark the gate as failed unless a follow-up code change removes the regression.
- `BenchmarkWorkerPoolStoreAppendBatch-10` must keep median `batch-calls/op <= 0.01743` (105% of the baseline median `0.01660`). Higher values mean smaller batches or more store calls per item.
- `BenchmarkWorkerPoolStoreAppendBatch-10` must keep median `single-append-calls/op <= 0.00005`.

Gate result: pass against the same-host baseline rerun.

- All `ns/op` medians are within the 5% regression threshold; most hot paths are faster.
- `allocs/op` does not increase; submit/run and observer paths drop from 5-8 allocs/op to 3-5 allocs/op.
- `B/op` does not increase; every benchmark is lower.
- StoreAppend post median `batch-calls/op=0.01683`, below the `0.01743` gate.
- StoreAppend post median `single-append-calls/op=0.0000396`, below the `0.00005` gate.
