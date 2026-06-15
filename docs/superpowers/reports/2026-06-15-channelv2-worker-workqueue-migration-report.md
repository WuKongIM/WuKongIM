# ChannelV2 Worker Workqueue Migration Report

## Baseline

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

The post-migration benchmark output will be added after implementation.

## Gate

- `ns/op` regression must be no more than 5%.
- `allocs/op` must not increase.
- Any material `B/op` increase requires explanation before proceeding.
- StoreAppend batching must retain a non-zero `batch-calls/op` and must not materially increase `single-append-calls/op`.
