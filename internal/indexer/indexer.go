package indexer

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/0xADE/a-lancxo/internal/config"
	"github.com/0xADE/a-lancxo/internal/indexer/desktop"
	"github.com/0xADE/a-lancxo/internal/indexer/executable"
)

// Indexer coordinates indexing of executables and desktop files
type Indexer struct {
	index       *Index
	running     bool
	mu          sync.RWMutex
	indexCtx    context.Context
	indexCancel context.CancelFunc
	indexWg     sync.WaitGroup
	watcher     *Watcher
}

// NewIndexer creates a new indexer instance
func NewIndexer() *Indexer {
	return &Indexer{
		index: NewIndex(),
	}
}

// Start begins the indexing process using configured paths
func (idx *Indexer) Start(ctx context.Context) error {
	cfg := config.Get()
	paths := cfg.Path()
	if err := idx.runIndexing(ctx, paths); err != nil {
		return err
	}

	// Start watching directories for changes
	return idx.startWatcher(ctx, paths)
}

// startWatcher initializes and starts the directory watcher
func (idx *Indexer) startWatcher(ctx context.Context, execPaths []string) error {
	// Create watcher with reindex callback
	watcher, err := NewWatcher(func(ctx context.Context) error {
		_, err := idx.Reindex(ctx, nil)
		return err
	})
	if err != nil {
		return err
	}

	idx.mu.Lock()
	idx.watcher = watcher
	idx.mu.Unlock()

	// Combine executable paths with desktop paths
	allPaths := make([]string, 0, len(execPaths)+3)
	allPaths = append(allPaths, execPaths...)
	allPaths = append(allPaths, desktop.GetDesktopPaths()...)

	return watcher.Start(ctx, allPaths)
}

// Reindex reindexes executables in the provided paths, or all registered paths if none provided
// Returns the total number of indexed executables
func (idx *Indexer) Reindex(ctx context.Context, paths []string) (int, error) {
	var indexingPaths []string
	if len(paths) > 0 {
		indexingPaths = paths
	} else {
		cfg := config.Get()
		indexingPaths = cfg.Path()
	}

	err := idx.runIndexing(ctx, indexingPaths)
	if err != nil {
		return 0, err
	}

	idx.mu.RLock()
	count := idx.index.Count()
	idx.mu.RUnlock()

	return count, nil
}

// runIndexing performs the actual indexing work
func (idx *Indexer) runIndexing(ctx context.Context, paths []string) error {
	idx.mu.Lock()
	// Cancel previous indexing if running
	if idx.running && idx.indexCancel != nil {
		idx.indexCancel()
		idx.indexWg.Wait()
	}

	// Create new context for this indexing run
	indexCtx, cancel := context.WithCancel(ctx)
	idx.indexCtx = indexCtx
	idx.indexCancel = cancel
	idx.running = true

	// Clear existing index
	idx.index = NewIndex()
	idx.mu.Unlock()

	// Create channels for results
	execChan := make(chan *executable.ExecutableInfo, 100)
	desktopChan := make(chan *desktop.DesktopEntry, 100)

	idx.indexWg = sync.WaitGroup{}

	// Start executable scanning
	idx.indexWg.Add(1)
	go func() {
		defer idx.indexWg.Done()
		if err := executable.ScanPaths(paths, execChan); err != nil {
			// Log error but continue
			return
		}
	}()

	// Start desktop file scanning
	idx.indexWg.Add(1)
	go func() {
		defer idx.indexWg.Done()
		if err := desktop.ScanDesktopFiles(desktopChan); err != nil {
			// Log error but continue
			return
		}
	}()

	// Process results
	idx.indexWg.Add(1)
	go func() {
		defer idx.indexWg.Done()
		idx.processResults(indexCtx, execChan, desktopChan)
	}()

	// Wait for all scanning to complete
	idx.indexWg.Wait()

	idx.mu.Lock()
	idx.running = false
	idx.mu.Unlock()

	return nil
}

func (idx *Indexer) processResults(ctx context.Context, execChan <-chan *executable.ExecutableInfo, desktopChan <-chan *desktop.DesktopEntry) {
	for {
		select {
		case <-ctx.Done():
			return
		case exec, ok := <-execChan:
			if !ok {
				execChan = nil
			} else {
				entry := &Entry{
					Name:      exec.Name,
					Path:      exec.Path,
					Exec:      exec.Path,
					Terminal:  false,
					IsDesktop: false,
				}
				idx.index.Add(entry)
			}
		case desk, ok := <-desktopChan:
			if !ok {
				desktopChan = nil
			} else {
				// Skip NoDisplay entries
				if desktop.IsNoDisplay(desk.Path) {
					continue
				}

				entry := &Entry{
					Name:       desk.Name,
					Names:      desk.Names,
					Path:       desk.Path,
					Exec:       desk.Exec,
					Terminal:   desk.Terminal,
					Categories: desk.Categories,
					IsDesktop:  true,
				}
				idx.index.Add(entry)
			}
		}

		if execChan == nil && desktopChan == nil {
			break
		}
	}
}

// GetIndex returns the index instance
func (idx *Indexer) GetIndex() *Index {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.index
}

// IsRunning returns whether indexing is currently running
func (idx *Indexer) IsRunning() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.running
}

// Stop stops the indexing process and directory watcher
func (idx *Indexer) Stop() {
	idx.mu.Lock()
	if idx.running && idx.indexCancel != nil {
		idx.indexCancel()
	}
	idx.running = false
	watcher := idx.watcher
	idx.mu.Unlock()

	idx.indexWg.Wait()

	// Stop watcher if running
	if watcher != nil {
		if err := watcher.Stop(); err != nil {
			// Log but don't fail
			fmt.Fprintf(os.Stderr, "Indexer: failed to stop watcher: %v\n", err)
		}
	}
}
