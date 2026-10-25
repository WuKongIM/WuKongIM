//go:build integration && darwin

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/stretchr/testify/require"
)

const (
	apfsObservationOutputCap  = 256 << 10
	apfsObservationDBs        = 3
	apfsObservationBatches    = 32
	apfsObservationRecords    = 32
	apfsObservationValueBytes = 3200
	apfsObservationMarker     = "WK_APFS_OBSERVATION_JSON:"
)

type apfsObservationIO struct {
	Count     int64 `json:"count"`
	Bytes     int64 `json:"bytes"`
	TotalNS   int64 `json:"total_ns"`
	MaxNS     int64 `json:"max_ns"`
	Errors    int64 `json:"errors"`
	FullSyncs int64 `json:"full_syncs"`
}

// apfsObservationFS is a pass-through recorder; it never gates or delays I/O.
type apfsObservationFS struct {
	vfs.FS
	mu      sync.Mutex
	methods map[string]apfsObservationIO
	files   map[string]map[string]apfsObservationIO
}

func (f *apfsObservationFS) observe(path, method string, n int64, start time.Time, full bool, err error) {
	d := time.Since(start).Nanoseconds()
	f.mu.Lock()
	defer f.mu.Unlock()
	add := func(row apfsObservationIO) apfsObservationIO {
		row.Count++
		row.Bytes += n
		row.TotalNS += d
		if d > row.MaxNS {
			row.MaxNS = d
		}
		if err != nil {
			row.Errors++
		}
		if full && err == nil {
			row.FullSyncs++
		}
		return row
	}
	f.methods[method] = add(f.methods[method])
	name := filepath.Base(path)
	if f.files[name] == nil {
		f.files[name] = make(map[string]apfsObservationIO, 4)
	}
	f.files[name][method] = add(f.files[name][method])
}
func (f *apfsObservationFS) snapshot() map[string]apfsObservationIO {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]apfsObservationIO, 4)
	for _, method := range []string{"Write", "Sync", "SyncData", "SyncTo"} {
		out[method] = f.methods[method]
	}
	return out
}
func (f *apfsObservationFS) fileSnapshot() map[string]map[string]apfsObservationIO {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]map[string]apfsObservationIO, len(f.files))
	for name, methods := range f.files {
		out[name] = make(map[string]apfsObservationIO, len(methods))
		for method, row := range methods {
			out[name][method] = row
		}
	}
	return out
}
func (f *apfsObservationFS) wrap(name string, file vfs.File, err error) (vfs.File, error) {
	if err != nil || !strings.HasSuffix(name, ".log") {
		return file, err
	}
	return &apfsObservationFile{File: file, owner: f, path: name}, nil
}
func (f *apfsObservationFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.Create(name, category)
	return f.wrap(name, file, err)
}
func (f *apfsObservationFS) ReuseForWrite(old, name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.ReuseForWrite(old, name, category)
	return f.wrap(name, file, err)
}

type apfsObservationFile struct {
	vfs.File
	owner *apfsObservationFS
	path  string
}

func (f *apfsObservationFile) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := f.File.Write(p)
	f.owner.observe(f.path, "Write", int64(n), start, false, err)
	return n, err
}
func (f *apfsObservationFile) Sync() error {
	start := time.Now()
	err := f.File.Sync()
	f.owner.observe(f.path, "Sync", 0, start, true, err)
	return err
}
func (f *apfsObservationFile) SyncData() error {
	start := time.Now()
	err := f.File.SyncData()
	f.owner.observe(f.path, "SyncData", 0, start, true, err)
	return err
}
func (f *apfsObservationFile) SyncTo(length int64) (bool, error) {
	start := time.Now()
	full, err := f.File.SyncTo(length)
	f.owner.observe(f.path, "SyncTo", length, start, full, err)
	return full, err
}

type apfsObservationCommit struct {
	DB           int               `json:"db"`
	Batch        int               `json:"batch"`
	EncodedBytes int               `json:"encoded_bytes"`
	WallNS       int64             `json:"wall_ns"`
	Native       NativeCommitStats `json:"native"`
	Error        string            `json:"error,omitempty"`
}
type apfsObservationDB struct {
	db      *DB
	fs      *apfsObservationFS
	path    string
	batches []*Batch
	encoded []int
	active  atomic.Int64
	peak    atomic.Int64
}

func apfsObservationOptions() Options {
	// Match the production message engine, including its explicit debt step.
	return Options{MemTableSize: 64 << 20, CompactionDebtConcurrencyBytes: 128 << 20}
}
func openAPFSObservationDB(t *testing.T, path string) *apfsObservationDB {
	t.Helper()
	stalls := newStallRecorder(time.Now)
	disk := newDiskSlowRecorder()
	poptions, err := pebbleOptions(apfsObservationOptions(), stalls)
	require.NoError(t, err)
	require.EqualValues(t, 512<<20, poptions.CacheSize)
	counter := &apfsObservationFS{FS: vfs.Default, methods: make(map[string]apfsObservationIO, 4), files: make(map[string]map[string]apfsObservationIO)}
	poptions.EventListener.DiskSlow = disk.observe
	poptions.FS = counter
	// A supplied FS bypasses native default wrapping. Restore that exact public
	// middleware; Pebble owns its closer, just as in production Open defaults.
	poptions.EnsureDefaults()
	poptions.WithFSDefaults()
	pdb, err := pebble.Open(path, poptions)
	require.NoError(t, err)
	return &apfsObservationDB{db: &DB{pdb: pdb, stalls: stalls, disk: disk}, fs: counter, path: path}
}
func apfsObservationRecord(batch, record int) ([]byte, []byte) {
	key := []byte(fmt.Sprintf("batch/%02d/record/%02d", batch, record))
	value := bytes.Repeat([]byte{byte(batch + 1), byte(record + 1)}, apfsObservationValueBytes/2)
	return key, value
}
func apfsObservationMetric(db *DB) map[string]any {
	m := db.pdb.Metrics()
	return map[string]any{"wal_bytes_in": m.WAL.BytesIn, "wal_size": m.WAL.Size, "memtable_bytes": m.MemTable.Size, "cache_bytes": m.BlockCache.Size, "flush_count": m.Flush.Count, "compaction_count": m.Compact.Count, "compactions_in_progress": m.Compact.NumInProgress, "l0_tables": m.Levels[0].TablesCount}
}
func apfsObservationDelta(after, before map[string]apfsObservationIO) map[string]apfsObservationIO {
	out := make(map[string]apfsObservationIO, 4)
	for method, row := range after {
		old := before[method]
		// Max is an endpoint cumulative maximum, not an interval maximum.
		out[method] = apfsObservationIO{Count: row.Count - old.Count, Bytes: row.Bytes - old.Bytes, TotalNS: row.TotalNS - old.TotalNS, MaxNS: row.MaxNS, Errors: row.Errors - old.Errors, FullSyncs: row.FullSyncs - old.FullSyncs}
	}
	return out
}
func apfsObservationRanks(samples []int64) map[string]int64 {
	values := append([]int64(nil), samples...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return map[string]int64{"min_ns": values[0], "p50_nearest_rank_ns": values[(len(values)*50+99)/100-1], "p95_nearest_rank_ns": values[(len(values)*95+99)/100-1], "max_ns": values[len(values)-1]}
}
func runAPFSObservationPhase(t *testing.T, root, name string, owners int) map[string]any {
	t.Helper()
	setupStart := time.Now()
	dbs := make([]*apfsObservationDB, apfsObservationDBs)
	digest := sha256.New()
	for b := 0; b < apfsObservationBatches; b++ {
		for r := 0; r < apfsObservationRecords; r++ {
			key, value := apfsObservationRecord(b, r)
			_, _ = digest.Write(key)
			_, _ = digest.Write(value)
		}
	}
	datasetSHA := hex.EncodeToString(digest.Sum(nil))
	for id := range dbs {
		db := openAPFSObservationDB(t, filepath.Join(root, name, fmt.Sprintf("db-%d", id)))
		dbs[id] = db
		for b := 0; b < apfsObservationBatches; b++ {
			batch := db.db.NewBatch()
			for r := 0; r < apfsObservationRecords; r++ {
				key, value := apfsObservationRecord(b, r)
				require.NoError(t, batch.Set(key, value))
			}
			db.batches = append(db.batches, batch)
			db.encoded = append(db.encoded, len(batch.batch.Repr()))
		}
	}
	beforeIO := make([]map[string]apfsObservationIO, len(dbs))
	beforeMetrics := make([]map[string]any, len(dbs))
	beforeFiles := make([]map[string]map[string]apfsObservationIO, len(dbs))
	for id, db := range dbs {
		beforeIO[id] = db.fs.snapshot()
		beforeMetrics[id] = apfsObservationMetric(db.db)
		beforeFiles[id] = db.fs.fileSnapshot()
	}
	setupNS := time.Since(setupStart).Nanoseconds()
	results := make([]apfsObservationCommit, apfsObservationDBs*apfsObservationBatches)
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	for id, db := range dbs {
		jobs := make(chan int, apfsObservationBatches)
		for b := 0; b < apfsObservationBatches; b++ {
			jobs <- b
		}
		close(jobs)
		for worker := 0; worker < owners; worker++ {
			wg.Add(1)
			go func(id int, db *apfsObservationDB, jobs <-chan int) {
				defer wg.Done()
				<-startGate
				for b := range jobs {
					n := db.active.Add(1)
					for old := db.peak.Load(); n > old && !db.peak.CompareAndSwap(old, n); old = db.peak.Load() {
					}
					batch := db.batches[b]
					start := time.Now()
					err := batch.Commit(true)
					elapsed := time.Since(start).Nanoseconds()
					stats := batch.CommitStats()
					native := batch.batch.CommitStats()
					row := apfsObservationCommit{DB: id, Batch: b, EncodedBytes: db.encoded[b], WallNS: elapsed, Native: stats}
					if err != nil {
						row.Error = err.Error()
					}
					if !stats.Available || stats.TotalDuration != native.TotalDuration || stats.SemaphoreWaitDuration != native.SemaphoreWaitDuration || stats.WALQueueWaitDuration != native.WALQueueWaitDuration || stats.MemTableWriteStallDuration != native.MemTableWriteStallDuration || stats.L0ReadAmpWriteStallDuration != native.L0ReadAmpWriteStallDuration || stats.WALRotationDuration != native.WALRotationDuration || stats.CommitWaitDuration != native.CommitWaitDuration {
						row.Error = "native observation unavailable or not equal"
					}
					if closeErr := batch.Close(); closeErr != nil {
						row.Error = closeErr.Error()
					}
					results[id*apfsObservationBatches+b] = row
					db.active.Add(-1)
				}
			}(id, db, jobs)
		}
	}
	startedWall := time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now()
	close(startGate)
	wg.Wait()
	commitNS := time.Since(start).Nanoseconds()
	finishedWall := time.Now().UTC().Format(time.RFC3339Nano)
	perDB := make([]map[string]any, len(dbs))
	var latency []int64
	var nativeTotal, nativeWait int64
	for _, row := range results {
		require.Empty(t, row.Error)
		require.Positive(t, row.WallNS)
		latency = append(latency, row.WallNS)
		nativeTotal += int64(row.Native.TotalDuration)
		nativeWait += int64(row.Native.CommitWaitDuration)
	}
	for id, db := range dbs {
		require.Zero(t, db.active.Load())
		require.LessOrEqual(t, db.peak.Load(), int64(owners))
		after := db.fs.snapshot()
		perDB[id] = map[string]any{"id": id, "owners_peak": db.peak.Load(), "io_before": beforeIO[id], "io_after_commit": after, "wal_files_before": beforeFiles[id], "wal_files_after_commit": db.fs.fileSnapshot(), "io_commit_delta": apfsObservationDelta(after, beforeIO[id]), "metrics_before": beforeMetrics[id], "metrics_after_commit": apfsObservationMetric(db.db)}
	}
	verifyStart := time.Now()
	for id, db := range dbs {
		require.NoError(t, db.db.Close())
		perDB[id]["io_after_close"] = db.fs.snapshot()
		perDB[id]["wal_files_after_close"] = db.fs.fileSnapshot()
		reopened, err := Open(db.path, apfsObservationOptions())
		require.NoError(t, err)
		check := sha256.New()
		for b := 0; b < apfsObservationBatches; b++ {
			for r := 0; r < apfsObservationRecords; r++ {
				key, want := apfsObservationRecord(b, r)
				got, found, err := reopened.Get(key)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, want, got)
				_, _ = check.Write(key)
				_, _ = check.Write(got)
			}
		}
		require.Equal(t, datasetSHA, hex.EncodeToString(check.Sum(nil)))
		require.NoError(t, reopened.Close())
		perDB[id]["reopened_verified_keys"] = apfsObservationBatches * apfsObservationRecords
	}
	return map[string]any{"phase": name, "owners_per_db": owners, "fresh_databases": len(dbs), "batches": len(results), "records": len(results) * apfsObservationRecords, "payload_bytes": len(results) * apfsObservationRecords * apfsObservationValueBytes, "dataset_sha256_per_db": datasetSHA, "setup_and_batch_prepare_ns_excluded": setupNS, "close_reopen_verify_ns_excluded": time.Since(verifyStart).Nanoseconds(), "started_wall": startedWall, "finished_wall": finishedWall, "commit_window_ns": commitNS, "physical_batches_per_second": float64(len(results)) * 1e9 / float64(commitNS), "native_total_mean_ns": float64(nativeTotal) / float64(len(results)), "native_commit_wait_mean_ns": float64(nativeWait) / float64(len(results)), "commit_wall_ranks": apfsObservationRanks(latency), "per_db": perDB, "commits": results, "all_owners_joined_before_close": true, "all_values_verified_after_reopen": true}
}

func TestNativeSyncAPFSHelper(t *testing.T) {
	if os.Getenv("WK_TEST_ENGINE_APFS_HELPER") != "1" {
		t.Skip("owned subprocess only")
	}
	require.Equal(t, 2, runtime.GOMAXPROCS(0))
	root := os.Getenv("WK_TEST_ENGINE_APFS_DATA_DIR")
	require.NotEmpty(t, root)
	var stat syscall.Statfs_t
	require.NoError(t, syscall.Statfs(root, &stat))
	var fsType []byte
	for _, b := range stat.Fstypename {
		if b == 0 {
			break
		}
		fsType = append(fsType, byte(b))
	}
	require.Equal(t, "apfs", string(fsType))
	var phases []map[string]any
	for _, phase := range []struct {
		name   string
		owners int
	}{{"A1", 1}, {"B1", 2}, {"B2", 2}, {"A2", 1}} {
		result := runAPFSObservationPhase(t, root, phase.name, phase.owners)
		phases = append(phases, result)
		fmt.Printf("APFS_PHASE_COMPLETE:%s batches=96 verified=true\n", phase.name)
	}
	report := map[string]any{"schema": "wukongim/apfs-native-sync-observation/v1", "complete": true, "platform": runtime.GOOS + "/" + runtime.GOARCH, "go_version": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "filesystem": string(fsType), "phase_order": "A(1)-B(2)-B(2)-A(1), phases named A1/B1/B2/A2", "memtable_bytes": 64 << 20, "read_headroom_bytes": 128 << 20, "combined_cache_budget_bytes": 512 << 20, "compaction_debt_step_bytes": 128 << 20, "sync": true, "no_sync_or_experimental_api": false, "wal_min_sync_interval_changed": false, "all_other_engine_options_default": true, "default_fs_health_middleware_restored": true, "fresh_databases_each_phase": true, "phases": phases, "limits": []string{"Non-gating observation; no speed-ratio assertion or product adoption decision", "Shared Mac/APFS, not Linux or 2000/s acceptance", "Fresh DBs remove DB growth carryover, not filesystem/cache/allocator/thermal/host drift", "Prepared bounded batches and synthetic fixed payload differ from production message orchestration", "Native CommitWait includes sequence publication and WAL sync, not pure fsync", "I/O durations overlap native/commit durations; do not add them", "I/O delta max_ns is endpoint cumulative maximum, not interval maximum", "Create/prepare/Close/reopen are excluded and reported separately"}}
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), apfsObservationOutputCap)
	fmt.Println(apfsObservationMarker + string(raw))
}

type apfsObservationOutput struct {
	mu sync.Mutex
	bytes.Buffer
	overflow bool
}

func (o *apfsObservationOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if len(p) > apfsObservationOutputCap-o.Len() {
		o.overflow = true
		p = p[:apfsObservationOutputCap-o.Len()]
	}
	_, _ = o.Buffer.Write(p)
	return n, nil
}
func writeAPFSObservationArtifact(t *testing.T, name string, raw []byte) {
	t.Helper()
	require.LessOrEqual(t, len(raw), apfsObservationOutputCap)
	if dir := os.Getenv("WK_TEST_ENGINE_APFS_ARTIFACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, err = file.Write(raw)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}
	h := sha256.Sum256(raw)
	t.Logf("APFS artifact %s bytes=%d sha256=%s", name, len(raw), hex.EncodeToString(h[:]))
}
func TestNativeSyncAPFSABBAObservation(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeSyncAPFSHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "WK_TEST_ENGINE_APFS_HELPER=1", "WK_TEST_ENGINE_APFS_DATA_DIR="+root, "GOMAXPROCS=2")
	cmd.WaitDelay = 2 * time.Second
	var output apfsObservationOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	started := time.Now()
	err = cmd.Run()
	elapsed := time.Since(started)
	raw := append([]byte(nil), output.Bytes()...)
	writeAPFSObservationArtifact(t, "apfs-helper-output.log", raw)
	execution := map[string]any{"process_wait_joined": true, "deadline_seconds": 90, "wait_delay_seconds": 2, "elapsed_ns": elapsed.Nanoseconds(), "timed_out": ctx.Err() != nil, "output_overflow": output.overflow, "output_bytes": len(raw), "complete": err == nil}
	if err != nil {
		execution["error"] = err.Error()
	}
	executionRaw, marshalErr := json.Marshal(execution)
	require.NoError(t, marshalErr)
	writeAPFSObservationArtifact(t, "apfs-helper-execution.json", executionRaw)
	require.NoError(t, ctx.Err())
	require.NoError(t, err)
	require.False(t, output.overflow)
	var reportRaw []byte
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte(apfsObservationMarker)) {
			require.Nil(t, reportRaw, "only one terminal report is valid")
			reportRaw = append([]byte(nil), line[len(apfsObservationMarker):]...)
		}
	}
	require.NotEmpty(t, reportRaw)
	var report map[string]any
	require.NoError(t, json.Unmarshal(reportRaw, &report))
	require.Equal(t, true, report["complete"])
	writeAPFSObservationArtifact(t, "apfs-observation.json", reportRaw)
	// Keep the complete bounded report in verbose test output when no artifact
	// directory was requested; a fresh directory is required for each replay.
	t.Log(string(reportRaw))
}
