package rag

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches for file changes and updates the index asynchronously
type Watcher struct {
	engine   *Engine
	watcher  *fsnotify.Watcher
	dataDir  string
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	updating bool
	lastUpdate time.Time
	updateDelay time.Duration
}

// NewWatcher creates a new file watcher
func NewWatcher(engine *Engine, dataDir string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		engine:     engine,
		watcher:    watcher,
		dataDir:    dataDir,
		ctx:        ctx,
		cancel:     cancel,
		updateDelay: 2 * time.Second, // Debounce: wait 2 seconds after last change
	}

	return w, nil
}

// Start starts watching for file changes
func (w *Watcher) Start() error {
	contentDir := filepath.Join(w.dataDir, "content")
	
	// Watch content directory recursively
	if err := w.watcher.Add(contentDir); err != nil {
		return fmt.Errorf("failed to watch content directory: %w", err)
	}

	// Start watching goroutine
	go w.watch()

	return nil
}

// Stop stops watching
func (w *Watcher) Stop() error {
	w.cancel()
	return w.watcher.Close()
}

// watch watches for file changes and triggers index updates
func (w *Watcher) watch() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// Only process markdown files
			if filepath.Ext(event.Name) == ".md" {
				w.lastUpdate = time.Now()
				// Trigger update after delay (debounce)
				go w.debouncedUpdate()
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("Watcher error: %v\n", err)
		case <-ticker.C:
			// Check if we need to update (debounce)
			if !w.lastUpdate.IsZero() && time.Since(w.lastUpdate) >= w.updateDelay {
				if !w.updating {
					w.lastUpdate = time.Time{} // Reset
					go w.updateIndex()
				}
			}
		}
	}
}

// debouncedUpdate triggers an update after a delay
func (w *Watcher) debouncedUpdate() {
	time.Sleep(w.updateDelay)
	
	w.mu.Lock()
	if w.updating {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	w.updateIndex()
}

// updateIndex updates the index
func (w *Watcher) updateIndex() {
	w.mu.Lock()
	if w.updating {
		w.mu.Unlock()
		return
	}
	w.updating = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.updating = false
		w.mu.Unlock()
	}()

	// Get project ID (use empty string for default)
	projectID := ""
	if err := w.engine.UpdateIndex(projectID); err != nil {
		fmt.Printf("Failed to update index: %v\n", err)
	}
}

