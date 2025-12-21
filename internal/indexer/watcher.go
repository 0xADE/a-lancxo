package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// debounceDelay is the time to wait before triggering reindex after changes
	debounceDelay = 500 * time.Millisecond
)

// ReindexCallback is called when directory changes are detected
type ReindexCallback func(ctx context.Context) error

// Watcher monitors directories for file changes and triggers reindexing
type Watcher struct {
	watcher   *fsnotify.Watcher
	callback  ReindexCallback
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	debounce  *time.Timer
	pending   bool
	paths     []string // tracked directories
}

// NewWatcher creates a new directory watcher
func NewWatcher(callback ReindexCallback) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	return &Watcher{
		watcher:  fsWatcher,
		callback: callback,
	}, nil
}

// Start begins watching the specified directories
func (w *Watcher) Start(ctx context.Context, paths []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.ctx, w.cancel = context.WithCancel(ctx)
	w.paths = paths

	// Add all directories to watcher
	for _, path := range paths {
		if err := w.addPathRecursive(path); err != nil {
			// Log but continue with other paths
			fmt.Fprintf(os.Stderr, "Watcher: failed to watch %s: %v\n", path, err)
		}
	}

	// Start the event loop
	w.wg.Add(1)
	go w.eventLoop()

	return nil
}

// addPathRecursive adds a directory and all its subdirectories to the watcher
func (w *Watcher) addPathRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip inaccessible directories
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			if err := w.watcher.Add(path); err != nil {
				// Log but continue
				fmt.Fprintf(os.Stderr, "Watcher: failed to add %s: %v\n", path, err)
			}
		}

		return nil
	})
}

// eventLoop processes fsnotify events
func (w *Watcher) eventLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			w.mu.Lock()
			if w.debounce != nil {
				w.debounce.Stop()
			}
			w.mu.Unlock()
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		}
	}
}

// handleEvent processes a single fsnotify event
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// We care about Create and Remove events
	if event.Op&(fsnotify.Create|fsnotify.Remove) == 0 {
		return
	}

	// If a new directory was created, add it to the watcher
	if event.Op&fsnotify.Create != 0 {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			if err := w.addPathRecursive(event.Name); err != nil {
				fmt.Fprintf(os.Stderr, "Watcher: failed to add new directory %s: %v\n", event.Name, err)
			}
		}
	}

	// Schedule debounced reindex
	w.scheduleReindex()
}

// scheduleReindex schedules a debounced reindex
func (w *Watcher) scheduleReindex() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Reset timer if already pending
	if w.debounce != nil {
		w.debounce.Stop()
	}

	w.pending = true
	w.debounce = time.AfterFunc(debounceDelay, func() {
		w.mu.Lock()
		w.pending = false
		w.mu.Unlock()

		// Trigger reindex
		if w.callback != nil {
			if err := w.callback(w.ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Watcher: reindex failed: %v\n", err)
			}
		}
	})
}

// Stop stops the watcher
func (w *Watcher) Stop() error {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	if w.debounce != nil {
		w.debounce.Stop()
	}
	w.mu.Unlock()

	w.wg.Wait()

	return w.watcher.Close()
}

// GetWatchedPaths returns the list of root paths being watched
func (w *Watcher) GetWatchedPaths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.paths
}

