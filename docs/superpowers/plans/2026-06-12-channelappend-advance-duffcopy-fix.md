# channelappend advance() 大结构体值拷贝优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除 `channelWriter.advance()` 主循环里 `commitEffect`(176B) / `appendEffect`(128B) 的按值返回拷贝，把负载下 CPU 中约 35% 的 `runtime.duffcopy` + `runtime.duffzero` 开销砍掉。

**Architecture:** 当前 `advance()` 每轮迭代都按值返回两个大结构体（即便无工作时也返回零值结构体），返回值沿 `nextCommitEffect → nextCommitLocked → advance` 和 `nextAppendBatch → nextAppendLocked → advance` 链条逐跳 memcpy。本计划把这条链路改为「指针返回 + 调用方栈预分配 effect」：`next*Locked` 接受 `*effect` 出参并返回 `bool`，命中时原地填充、未命中时不碰内存。effect 在 `advance` 的循环外预分配一次，循环内复用。`runAppend/runCommit` 改为指针接收，`.run` 方法保持值语义不变（它们运行在 worker goroutine、不在热循环）。

**Tech Stack:** Go 1.25；包 `github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend`；测试 `go test`，基准 `go test -bench`。

**根因证据（负载下 30s CPU profile，三节点一致）：**
- `channelWriter.advance` cum ≈ 67%
- `runtime.duffcopy` flat ≈ 28%，`runtime.duffzero` flat ≈ 6.5%
- duffcopy 调用者 95% 来自 `nextCommitLocked`(54%) 与 `advance`(40%)
- 结构体尺寸：`AuthorityTarget`=80B，`subscriberCache`=40B，`commitEffect`=176B，`appendEffect`=128B
- 证据目录：`docs/development/perf-runs/20260612-224805-qps10000-cpu/pprof/under-load/`

**关键约束 / 不变量（不得破坏）：**
- 单写者不变量：同一 writer 同时只有一个 goroutine 跑 `advance`（`scheduled` CAS 保护）。
- `advance()` 与 `advanceAppendOnly()` 两条路径都要改：group send 走 `advance()`（有 post-commit），p2p/append-only 走 `advanceAppendOnly()`。
- effect 复用必须保证：effect 一旦交给 `runAppend/runCommit`（它们把 effect 捕获进闭包提交到 pool），下一轮循环不能再写同一块内存。**因此 effect 不能跨「已提交」边界复用** —— 见 Task 4 的设计说明。
- `nextCommitEffect` 持有 `s.committed` 的切片别名（`events`），不可改成拷贝（已有测试 `TestChannelStateCommitEffectSharesQueuedImmutablePayload` 锁定此行为）。
- 现有基准只设 `commitPorts{}`，跑的是 `advanceAppendOnly()`，**测不到本次热点**。Task 1 先补一个走 `advance()` 的基准做基线。

---

## File Structure

- `internalv2/runtime/channelappend/state.go` — `nextCommitEffect` / `nextAppendBatch` 改为指针出参（命中填充、未命中不写）。
- `internalv2/runtime/channelappend/writer.go` — `nextCommitLocked` / `nextAppendLocked` 改为 `*effect` 出参 + bool；`advance` / `advanceAppendOnly` 在循环外预分配 effect 并复用；`runAppend` / `runCommit` 改指针接收。
- `internalv2/runtime/channelappend/benchmark_test.go` — 新增走 `advance()`（post-commit）路径的基准，作为热点基线与回归门禁。
- `internalv2/runtime/channelappend/writer_advance_test.go` — 新增「effect 复用不串话」回归测试。

不新增文件；全部在既有文件内修改，遵循包内现有约定。

---

### Task 1: 新增走 advance() 热路径的基准（建立基线）

**Files:**
- Modify: `internalv2/runtime/channelappend/benchmark_test.go`（在文件末尾、`maxBenchmarkInt` 之前追加）

现有基准用 `commitPorts{}`（无 post-commit），只跑 `advanceAppendOnly`。要测到 `commitEffect` duffcopy，必须让 `hasPostCommitWork()` 为真，即设置一个 `ConversationActiveAdmitter`。用一个 no-op admitter 即可触发 `advance()` 分支而不引入真实交付开销。

- [ ] **Step 1: 写基准 + no-op admitter 测试替身**

在 `benchmark_test.go` 末尾 `func maxBenchmarkInt` 定义之前插入：

```go
// benchmarkNoopActiveAdmitter enables the post-commit path (hasPostCommitWork
// == true) so advance() — not advanceAppendOnly() — is exercised, without
// adding real delivery cost. This is the path where commitEffect duffcopy
// dominates under load.
type benchmarkNoopActiveAdmitter struct{}

func (benchmarkNoopActiveAdmitter) AdmitActiveBatch(context.Context, conversationactive.ActiveBatch) error {
	return nil
}

func BenchmarkSubmitLocalHotChannelPostCommit(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		ConversationActiveAdmitter: benchmarkNoopActiveAdmitter{},
	})
	target := benchmarkAuthorityTarget("bench-postcommit")
	item := benchmarkSendItem("bench-postcommit")
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}
```

并在文件顶部 import 块加入 `conversationactive` 包（若尚未存在）：

```go
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
```

- [ ] **Step 2: 跑基准确认能编译并产出基线数字**

Run:
```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE \
  -bench 'BenchmarkSubmitLocalHotChannelPostCommit' -benchtime=200x -count=3
```
Expected: PASS，输出形如 `BenchmarkSubmitLocalHotChannelPostCommit-10  ...  N ns/op  ... B/op  ... allocs/op`。**记录这三行 ns/op 与 allocs/op 作为基线**（写进 commit message）。

- [ ] **Step 3: 提交基线基准**

```bash
git add internalv2/runtime/channelappend/benchmark_test.go
git commit -m "test(channelappend): add post-commit advance() benchmark baseline"
```

---

### Task 2: state.nextCommitEffect 改为指针出参

**Files:**
- Modify: `internalv2/runtime/channelappend/state.go:185-214`
- Test: `internalv2/runtime/channelappend/state_test.go`（已有两个测试调用 `nextCommitEffect`，需同步签名）

把 `nextCommitEffect(key string) (commitEffect, bool)` 改为 `nextCommitEffect(key string, out *commitEffect) bool`：命中时把字段写进 `*out`，未命中时**完全不碰** `*out`（避免 duffzero）。

- [ ] **Step 1: 改实现签名与函数体**

将 `state.go` 中整个 `nextCommitEffect` 函数（185-214 行）替换为：

```go
func (s *channelState) nextCommitEffect(key string, out *commitEffect) bool {
	if s.commitInflight {
		return false
	}
	if s.commitCursor >= len(s.committed) {
		return false
	}
	limit := commitBatchMaxEvents
	backlog := s.commitBacklog()
	if backlog < limit {
		limit = backlog
	}
	if limit <= 0 {
		return false
	}
	events := s.committed[s.commitCursor : s.commitCursor+limit]
	s.commitAttempts++
	out.key = key
	out.seq = s.nextCommitSeq
	out.attempt = s.commitAttempts
	out.events = events
	out.target = s.target
	out.subscriberCache = s.subscriberCache
	s.nextCommitSeq += uint64(limit)
	s.commitInflight = true
	s.commitInflightEvents = limit
	return true
}
```

- [ ] **Step 2: 同步 state_test.go 两个调用点**

`state_test.go:120` 与 `state_test.go:145` 当前是 `effect, ok := state.nextCommitEffect("2:room")`。两处都改为：

```go
		var effect commitEffect
		ok := state.nextCommitEffect("2:room", &effect)
```

（保留各自后续对 `effect.events` / `effect.subscriberCache` 的断言不变。）

- [ ] **Step 3: 跑相关测试确认通过**

Run:
```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ \
  -run 'TestChannelStateCommitEffect' -count=1 -v
```
Expected: `TestChannelStateCommitEffectSharesQueuedImmutablePayload` 与 `TestChannelStateCommitEffectReusesReadySubscriberCache` 均 PASS（验证切片别名与 cache 复用语义未变）。

注：此步会因 `writer.go` 仍调用旧签名而**整包编译失败**。这是预期的——`-run` 只编译需要的测试时若整包不过仍会报错。若 Step 3 报 `nextCommitEffect` 调用数不匹配，先做 Task 3 再回头跑。**正确做法：Task 2 与 Task 3 连续完成后再统一编译**，因此本步若仅因 writer.go 调用点报错，记为预期失败，继续 Task 3。

- [ ] **Step 4: 暂不提交，进入 Task 3（同一编译单元）**

---

### Task 3: writer.nextCommitLocked 改为指针出参，advance/advanceAppendOnly 复用 effect

**Files:**
- Modify: `internalv2/runtime/channelappend/writer.go:206-273`（`nextAppendLocked`、`nextCommitLocked`）
- Modify: `internalv2/runtime/channelappend/writer.go:109-152`（`advance`、`advanceAppendOnly`）
- Modify: `internalv2/runtime/channelappend/writer.go:217,275`（`runAppend`、`runCommit` 改指针接收）

**复用安全性设计（关键）：** effect 一旦传给 `runAppend/runCommit`，会被捕获进闭包提交到 worker pool，因此**那一块 effect 内存的所有权已转移**，本轮不可再写。但下一轮循环开始时，上一轮的 effect 已被消费（要么提交了、要么没命中没写过），所以「循环外分配一个 appendEffect 变量 + 一个 commitEffect 变量，每轮 `next*Locked` 命中时覆盖写入、命中后立即 `run*` 消费」是安全的：在 `run*` 把它捕获进闭包后，下一轮覆盖写之前，闭包已经持有的是**值拷贝**（闭包按值捕获 effect 形参）。下面 Step 1 把 `runAppend/runCommit` 改为接收指针并在闭包内**解引用成局部值**，确保闭包捕获的是当轮快照而非共享指针。

- [ ] **Step 1: nextAppendLocked / nextCommitLocked 改指针出参**

将 `writer.go` 的 `nextAppendLocked`（206-215 行）替换为：

```go
func (w *channelWriter) nextAppendLocked(out *appendEffect) bool {
	seq, items, ok := w.state.nextAppendBatch()
	if !ok {
		return false
	}
	w.ports.metrics.addPendingAppendItems(-len(items))
	w.ports.metrics.addAppendInflightItems(len(items))
	w.ports.metrics.observePressure()
	out.target = w.state.target
	out.key = w.key
	out.seq = seq
	out.items = items
	return true
}
```

将 `nextCommitLocked`（265-273 行）替换为：

```go
func (w *channelWriter) nextCommitLocked(out *commitEffect) bool {
	if w.runtimeStopped() {
		dropped := w.state.dropCommitBacklog()
		w.ports.metrics.addPostCommitBacklog(-dropped)
		w.ports.metrics.observePressure()
		return false
	}
	return w.state.nextCommitEffect(w.key, out)
}
```

- [ ] **Step 2: runAppend / runCommit 改为指针接收，闭包内取值快照**

将 `runAppend`（217-223 行）替换为：

```go
func (w *channelWriter) runAppend(effect *appendEffect) {
	snapshot := *effect
	_ = w.ports.pool.submit(func() {
		completion := snapshot.run(w.ports.runtimeCtx, w.ports.append)
		w.applyAppendCompletion(completion)
		w.rescheduleIfNeeded()
	})
}
```

将 `runCommit`（275-281 行）替换为：

```go
func (w *channelWriter) runCommit(effect *commitEffect) {
	snapshot := *effect
	_ = w.ports.pool.submit(func() {
		completion := snapshot.run(w.ports.runtimeCtx, w.ports.commit)
		w.applyCommitCompletion(completion)
		w.rescheduleIfNeeded()
	})
}
```

设计说明：`snapshot := *effect` 在提交前做一次值拷贝，确保 `run*` 闭包持有当轮独立快照，下一轮循环复用 `effect` 内存不会串话。这一次拷贝发生在「确实有工作要 run」的路径上（原本就要拷贝），无工作的热路径不再拷贝——净收益正是消除空返回的 duffcopy/duffzero。

- [ ] **Step 3: advance 复用循环外 effect**

将 `advance`（109-134 行）替换为：

```go
func (w *channelWriter) advance() {
	if !w.ports.commit.hasPostCommitWork() {
		w.advanceAppendOnly()
		return
	}
	var appendEff appendEffect
	var commitEff commitEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		hasCommit := w.nextCommitLocked(&commitEff)
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
		}
		if hasCommit {
			w.runCommit(&commitEff)
		}
		if !hasAppend && !hasCommit {
			if w.deactivate() && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
	}
}
```

- [ ] **Step 4: advanceAppendOnly 复用循环外 effect**

将 `advanceAppendOnly`（136-152 行）替换为：

```go
func (w *channelWriter) advanceAppendOnly() {
	var appendEff appendEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
			continue
		}
		if w.deactivate() && w.tryActivate() {
			continue // work arrived during the deactivate window; keep going
		}
		return
	}
}
```

- [ ] **Step 5: 整包编译 + vet**

Run:
```bash
GOWORK=off go build ./internalv2/runtime/channelappend/ && \
GOWORK=off go vet ./internalv2/runtime/channelappend/
```
Expected: 无输出（编译与 vet 均通过）。若报 `nextCommitEffect`/`nextAppendLocked` 调用点数量或签名不符，回到 Task 2 Step 1 / 本任务 Step 1 核对。

- [ ] **Step 6: 跑全包测试（含 -race）确认语义不变**

Run:
```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1 && \
GOWORK=off go test ./internalv2/runtime/channelappend/ -race \
  -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup' -count=1
```
Expected: 两条命令都 PASS。`-race` 这条专门覆盖 advance/commit/state/group，确认 effect 复用没有引入数据竞争。

- [ ] **Step 7: 提交 Task 2+3**

```bash
git add internalv2/runtime/channelappend/state.go \
        internalv2/runtime/channelappend/state_test.go \
        internalv2/runtime/channelappend/writer.go
git commit -m "perf(channelappend): return append/commit effects via pointer out-param to drop hot-loop duffcopy"
```

---

### Task 4: effect 复用不串话回归测试

**Files:**
- Modify: `internalv2/runtime/channelappend/writer_advance_test.go`（文件末尾追加）

验证「同一 writer 连续多批、append 与 commit 交错」时，复用的 effect 缓冲不会把上一批的数据泄漏到下一批：用 per-channel 顺序 appender 跑多批，断言每条 SEND 都成功且 append seq 单调。这覆盖了 effect 内存复用最危险的场景（高频交错提交）。

- [ ] **Step 1: 写回归测试**

`newWriterRuntime` 当前用 `commitPorts{}`（无 post-commit），跑的是 `advanceAppendOnly`。为覆盖 `advance()` 复用路径，用 `Group` + no-op admitter 直接打多批。在 `writer_advance_test.go` 末尾追加：

```go
func TestAdvancePostCommitReusesEffectWithoutBleed(t *testing.T) {
	group := New(Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		Appender:                   &orderedAppender{},
		MessageID:                  newBenchmarkMessageIDs(1),
		ConversationActiveAdmitter: benchmarkNoopActiveAdmitter{},
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	target := benchmarkAuthorityTarget("reuse-1")
	const batches = 200
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("reuse-1")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = f
	}
	for i, f := range futures {
		res, err := f.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(res) != 1 || res[0].Err != nil || res[0].Result.Reason != ReasonSuccess {
			t.Fatalf("batch %d result = %#v, want one successful result", i, res)
		}
	}
}
```

- [ ] **Step 2: 跑测试（普通 + race）确认通过**

Run:
```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ \
  -run 'TestAdvancePostCommitReusesEffectWithoutBleed' -count=1 -v && \
GOWORK=off go test ./internalv2/runtime/channelappend/ \
  -run 'TestAdvancePostCommitReusesEffectWithoutBleed' -race -count=1
```
Expected: 两条都 PASS，无 race 报告。

- [ ] **Step 3: 提交回归测试**

```bash
git add internalv2/runtime/channelappend/writer_advance_test.go
git commit -m "test(channelappend): cover advance() effect-buffer reuse for bleed and races"
```

---

### Task 5: 基准对比验证收益

**Files:** 无（只跑基准，记录数字）

- [ ] **Step 1: 重跑 Task 1 基准并与基线对比**

Run:
```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE \
  -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalManyChannelsParallel$' \
  -benchtime=200x -count=5 | tee /tmp/channelappend_bench_after.txt
```
Expected: PASS。对照 Task 1 Step 2 记录的基线，`BenchmarkSubmitLocalHotChannelPostCommit` 的 ns/op 应下降；allocs/op 不应上升。

- [ ] **Step 2: 用 benchstat 量化（若可用）**

Run:
```bash
which benchstat && echo "benchstat available" || \
  GOWORK=off go run golang.org/x/perf/cmd/benchstat@latest -h >/dev/null 2>&1 && echo "go-run benchstat ok" || \
  echo "benchstat unavailable; compare /tmp numbers by hand"
```
若可用，把基线与 after 两次 `-count` 输出分别存文件后 `benchstat old.txt new.txt`。否则人工对比 Step 1 与基线的 ns/op 中位数。

- [ ] **Step 3: 记录结论（不提交代码，仅在 PR 描述/commit body 写明）**

写下：基线 ns/op、优化后 ns/op、下降百分比，引用 profile 证据目录。无独立提交。

---

### Task 6: 端到端复测（可选但推荐）

**Files:** 无（重跑压测脚本，采集负载下 profile 复核）

- [ ] **Step 1: 重跑 10000 QPS 压测**

Run（与首次分析同参，便于对比）：
```bash
WK_BENCH_DURATION=60s WK_BENCH_WARMUP=10s WK_BENCH_COOLDOWN=5s GOWORK=off \
  ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000 \
  --out-dir docs/development/perf-runs/$(date +%Y%m%d-%H%M%S)-qps10000-cpu-after
```
Expected: 脚本跑完，产出 `summary.txt`。预期 actual_qps 较首次（1812）提升、p99 下降；至少 `advance` 占比应显著回落。

- [ ] **Step 2: 稳态窗口手动抓 CPU profile（脚本自带的只抓空载）**

压测进入测量窗口后（warmup 10s 之后），另开一个 shell：
```bash
mkdir -p /tmp/wk_cpu_prof_after
for p in 5011 5012 5013; do
  curl -fsS "http://127.0.0.1:$p/debug/pprof/profile?seconds=30" \
    > /tmp/wk_cpu_prof_after/node-$p-cpu.pb.gz &
done; wait
```

- [ ] **Step 3: 确认 advance 占比下降**

Run:
```bash
go tool pprof -top -nodecount=15 /tmp/wk_cpu_prof_after/node-5012-cpu.pb.gz | head -20
```
Expected: `runtime.duffcopy` flat% 从 ~28% 明显下降；`channelWriter.advance` cum% 低于首次的 ~67%。

---

## Self-Review

**1. Spec coverage（根因 → 任务映射）：**
- 空返回路径的 duffzero → Task 2/3 把 `next*` 改指针出参，未命中不写内存 ✓
- 命中路径的 duffcopy 逐跳传递 → Task 3 循环外复用单一 effect + 提交前一次性快照拷贝 ✓
- 现有基准测不到热点 → Task 1 新增 post-commit 基准 ✓
- 复用安全性（串话/竞争）→ Task 4 回归测试 + Task 3 Step 6 的 -race ✓
- 收益验证 → Task 5 微基准、Task 6 端到端 ✓

**2. Placeholder scan：** 无 TBD/TODO；每个改代码的 step 都给了完整函数体与命令。✓

**3. Type consistency：**
- `nextCommitEffect(key string, out *commitEffect) bool` —— state.go 定义、writer.go `nextCommitLocked` 调用、state_test.go 两处调用，签名一致 ✓
- `nextCommitLocked(out *commitEffect) bool` / `nextAppendLocked(out *appendEffect) bool` —— writer.go 定义与 `advance`/`advanceAppendOnly` 调用一致 ✓
- `runAppend(*appendEffect)` / `runCommit(*commitEffect)` —— 定义与调用点 `w.runAppend(&appendEff)` / `w.runCommit(&commitEff)` 一致 ✓
- `appendEffect.run` / `commitEffect.run` 仍是值接收，未改（闭包内 `snapshot := *effect` 后调用），与 append.go/commit.go 现有定义一致 ✓
- `benchmarkNoopActiveAdmitter` 在 Task 1 定义，Task 4 复用，同包可见 ✓
- `nextAppendBatch` 签名未改（仍 `(uint64, []preparedSend, bool)`），Task 3 Step 1 仅在 writer 层包装，state_test.go 对它的调用无需改 ✓

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-12-channelappend-advance-duffcopy-fix.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
