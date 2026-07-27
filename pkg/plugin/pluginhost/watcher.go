package pluginhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/fsnotify/fsnotify"
)

const defaultWatcherDebounce = 100 * time.Millisecond

// RestartRequest asks the runtime to restart a changed local plugin binary.
type RestartRequest struct {
	// PluginNo is the filename-safe plugin number derived from the changed path.
	PluginNo string
	// Path is the changed .wkp path.
	Path string
}

type debounceTimer interface {
	Stop() bool
}

type debounceScheduler interface {
	AfterFunc(delay time.Duration, fn func()) debounceTimer
}

type realDebounceScheduler struct{}

func (realDebounceScheduler) AfterFunc(delay time.Duration, fn func()) debounceTimer {
	return time.AfterFunc(delay, fn)
}

type debouncedEntry struct {
	timer debounceTimer
	seq   uint64
	req   RestartRequest
}

type pluginDebouncer struct {
	delay     time.Duration
	scheduler debounceScheduler
	emit      func(RestartRequest)

	mu      sync.Mutex
	nextSeq uint64
	stopped bool
	pending map[string]debouncedEntry
}

func newPluginDebouncer(delay time.Duration, scheduler debounceScheduler, emit func(RestartRequest)) *pluginDebouncer {
	if delay <= 0 {
		delay = defaultWatcherDebounce
	}
	if scheduler == nil {
		scheduler = realDebounceScheduler{}
	}
	return &pluginDebouncer{delay: delay, scheduler: scheduler, emit: emit, pending: make(map[string]debouncedEntry)}
}

func (d *pluginDebouncer) HandlePath(path string) {
	if filepath.Ext(path) != ".wkp" {
		return
	}
	no := strings.TrimSuffix(filepath.Base(path), ".wkp")
	if err := validatePluginNo(no); err != nil {
		return
	}
	cleanPath := filepath.Clean(path)

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if existing, ok := d.pending[cleanPath]; ok && existing.timer != nil {
		existing.timer.Stop()
	}
	d.nextSeq++
	seq := d.nextSeq
	req := RestartRequest{PluginNo: no, Path: cleanPath}
	timer := d.scheduler.AfterFunc(d.delay, func() { d.fire(cleanPath, seq) })
	d.pending[cleanPath] = debouncedEntry{timer: timer, seq: seq, req: req}
}

func (d *pluginDebouncer) fire(path string, seq uint64) {
	d.mu.Lock()
	entry, ok := d.pending[path]
	if d.stopped || !ok || entry.seq != seq {
		d.mu.Unlock()
		return
	}
	delete(d.pending, path)
	d.mu.Unlock()

	if d.emit != nil {
		d.emit(entry.req)
	}
}

func (d *pluginDebouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for path, entry := range d.pending {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(d.pending, path)
	}
}

// Watcher watches a local plugin directory and emits debounced restart requests.
type Watcher struct {
	dir       string
	delay     time.Duration
	onRestart func(context.Context, string)

	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	cancel    context.CancelFunc
	done      chan struct{}
	started   bool
	debouncer *pluginDebouncer
}

// WatcherOptions configure a plugin directory watcher.
type WatcherOptions struct {
	// Dir is the local directory containing .wkp plugin binaries.
	Dir string
	// DebounceDelay coalesces duplicate create/write events for one path.
	DebounceDelay time.Duration
	// OnRestart receives the plugin number that should be restarted.
	OnRestart func(context.Context, string)
}

// NewWatcher creates a fsnotify-backed plugin watcher.
func NewWatcher(opts WatcherOptions) *Watcher {
	return &Watcher{dir: opts.Dir, delay: opts.DebounceDelay, onRestart: opts.OnRestart}
}

// Start begins watching the plugin directory.
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return nil
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("create plugin watch dir %q: %w", w.dir, err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create plugin watcher: %w", err)
	}
	if err := watcher.Add(w.dir); err != nil {
		watcher.Close()
		return fmt.Errorf("watch plugin dir %q: %w", w.dir, err)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	debouncer := newPluginDebouncer(w.delay, nil, func(req RestartRequest) {
		if w.onRestart != nil {
			w.onRestart(watchCtx, req.PluginNo)
		}
	})
	w.watcher = watcher
	w.cancel = cancel
	w.done = make(chan struct{})
	w.debouncer = debouncer
	w.started = true
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskPluginWatcher, func() {
		w.run(watchCtx, watcher, debouncer, w.done)
	})
	return nil
}

// Stop stops watching and releases fsnotify resources.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	cancel := w.cancel
	watcher := w.watcher
	debouncer := w.debouncer
	done := w.done
	w.started = false
	w.cancel = nil
	w.watcher = nil
	w.debouncer = nil
	w.done = nil
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if watcher != nil {
		_ = watcher.Close()
	}
	if debouncer != nil {
		debouncer.Stop()
	}
	if done != nil {
		<-done
	}
}

func (w *Watcher) run(ctx context.Context, watcher *fsnotify.Watcher, debouncer *pluginDebouncer, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				debouncer.HandlePath(event.Name)
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
