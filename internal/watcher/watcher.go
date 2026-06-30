// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package watcher emits debounced events when files under the active
// .baifo/ directory change. The App subscribes once at boot, registers
// per-file callbacks, and the watcher takes care of the rest: native
// inotify/kqueue events come in noisy (editors write+rename+rename,
// some atomic-save dance, ...), so we debounce by 250ms before firing
// the callback.
//
// The package is intentionally small: it does not understand baifo's
// own config schema, it just routes events by absolute path. The App
// owns the "if baifo.yaml changed, reload providers + mcps" mapping.
package watcher

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Handler is invoked when one of the watched paths changes. The path
// is the absolute path as returned by fsnotify.
type Handler func(path string)

// debounceWindow is the per-path coalescing window. Editors that save
// via temp-file + rename emit several events per logical save; the
// window collapses them into one Handler call.
const debounceWindow = 250 * time.Millisecond

// Watcher fans fsnotify events into per-path Handlers with debouncing.
// Construct one with New, register paths with OnChange, then Start.
// Close releases the underlying fsnotify watcher.
type Watcher struct {
	mu       sync.Mutex
	fs       *fsnotify.Watcher
	handlers map[string]Handler // absPath -> handler
	dirHooks map[string]Handler // absDir -> handler for any file in it
	timers   map[string]*time.Timer

	stopCh chan struct{}
	done   chan struct{}
}

// New creates a Watcher. The underlying fsnotify.Watcher is created
// eagerly so configuration errors (e.g. inotify exhaustion) surface
// at construction, not at Start.
func New() (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	return &Watcher{
		fs:       fs,
		handlers: make(map[string]Handler),
		dirHooks: make(map[string]Handler),
		timers:   make(map[string]*time.Timer),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}, nil
}

// OnChange registers a Handler for the exact absolute path.
//
// We watch the file's PARENT DIRECTORY, not just the file inode. This
// is the fix for the classic fsnotify footgun: editors (vim with
// backups, VSCode, and basically every "atomic save" scheme) write to a
// temp file and rename it over the target, so baifo.yaml becomes a
// brand-new inode on every save. An inode-level watch on the old file
// goes permanently deaf the instant that rename lands — which is why
// hot-reload silently stopped firing after the first edit. A directory
// watch survives the swap because the directory inode is stable, and
// fsnotify reports child events with their full path, so dispatch still
// routes them by exact path match.
//
// We additionally add the file inode directly when it exists. That is
// redundant for correctness (the dir watch already covers it) but lets
// plain in-place writes fire without depending on directory-event
// delivery; a missing file is fine since the dir watch catches its
// creation.
func (w *Watcher) OnChange(absPath string, h Handler) error {
	w.mu.Lock()
	w.handlers[absPath] = h
	w.mu.Unlock()

	parent := filepath.Dir(absPath)
	if err := w.fs.Add(parent); err != nil {
		return fmt.Errorf("watch dir %q for %q: %w", parent, absPath, err)
	}
	// Best-effort direct watch; ignore failure (file may not exist yet).
	_ = w.fs.Add(absPath)
	return nil
}

// OnChangeInDir registers a Handler that fires for any file change
// inside absDir (recursive coverage is limited to the immediate
// directory — fsnotify itself is not recursive). Use this for
// directories like .baifo/skills/ where each skill is a sub-folder.
func (w *Watcher) OnChangeInDir(absDir string, h Handler) error {
	w.mu.Lock()
	w.dirHooks[absDir] = h
	w.mu.Unlock()
	return w.fs.Add(absDir)
}

// Start launches the event loop. Returns immediately; events arrive
// on the goroutine spawned here. Call Close to stop.
func (w *Watcher) Start() {
	go w.run()
}

// Close stops the watcher and releases the underlying resources.
// Safe to call multiple times.
func (w *Watcher) Close() error {
	select {
	case <-w.stopCh:
		// already closed
	default:
		close(w.stopCh)
	}
	<-w.done
	return w.fs.Close()
}

// run is the event loop. We treat Create / Write / Rename events as
// changes; Remove is intentionally ignored because the most common
// "remove" we see is the temp-file dance of editors, which is
// immediately followed by a Create that we DO want to react to.
func (w *Watcher) run() {
	defer close(w.done)
	for {
		select {
		case <-w.stopCh:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			w.dispatch(ev.Name)
		case _, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// fsnotify errors are non-fatal; we drop them silently
			// to avoid spamming the operator. A future logging hook
			// could surface them.
		}
	}
}

// dispatch finds the handler for absPath (exact match first, then
// directory match) and schedules it through the debounce timer.
func (w *Watcher) dispatch(absPath string) {
	w.mu.Lock()
	h, ok := w.handlers[absPath]
	if !ok {
		parent := filepath.Dir(absPath)
		if dh, dok := w.dirHooks[parent]; dok {
			h, ok = dh, true
		}
	}
	if !ok {
		w.mu.Unlock()
		return
	}
	_, exactlyWatched := w.handlers[absPath]
	// Reset / arm the debounce timer for this path.
	if t, exists := w.timers[absPath]; exists {
		t.Stop()
	}
	w.timers[absPath] = time.AfterFunc(debounceWindow, func() {
		h(absPath)
	})
	w.mu.Unlock()

	// Self-heal the direct inode watch. When an atomic save renames a
	// new file over absPath, fsnotify silently drops the watch on the
	// old inode; re-adding here re-attaches it to the new one so the
	// fast in-place-write path keeps working alongside the dir watch.
	// Best-effort: the dir watch already guarantees delivery, so we
	// ignore failures (e.g. the file briefly not existing mid-rename).
	if exactlyWatched {
		_ = w.fs.Add(absPath)
	}
}
