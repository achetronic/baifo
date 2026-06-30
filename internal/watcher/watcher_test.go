// SPDX-License-Identifier: Apache-2.0

package watcher

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls until cond returns true or the deadline expires.
// Returns true if cond fired in time. Polling beats sleep here because
// fsnotify latency is filesystem-dependent and we want the test to be
// snappy on Linux yet not flaky on slower CI disks.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestWatcher_OnChange_FiresAfterWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "baifo.yaml")
	if err := os.WriteFile(file, []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	w, err := New()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	var fires int32
	if err := w.OnChange(file, func(string) { atomic.AddInt32(&fires, 1) }); err != nil {
		t.Fatalf("OnChange: %v", err)
	}
	w.Start()

	// Modify the file. We expect exactly one handler invocation per
	// logical write, even if fsnotify reports multiple OS-level events.
	if err := os.WriteFile(file, []byte("k: v2\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	if !waitFor(2*time.Second, func() bool { return atomic.LoadInt32(&fires) >= 1 }) {
		t.Fatalf("handler did not fire after write")
	}
}

// atomicSave writes body to a temp file in the same dir and renames it
// over dst, mimicking how vim/VSCode/most editors persist a file. The
// rename replaces dst's inode, which is exactly what used to kill the
// fsnotify watch.
func atomicSave(t *testing.T, dst, body string) {
	t.Helper()
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatalf("rename over dst: %v", err)
	}
}

// TestWatcher_OnChange_SurvivesAtomicSaves is the regression guard for
// the "hot-reload dies after the first save" bug. Editors save by
// temp-file + rename, swapping the target's inode every time. An
// inode-level watch goes deaf after the first rename; a directory watch
// must keep firing on every subsequent atomic save.
func TestWatcher_OnChange_SurvivesAtomicSaves(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "baifo.yaml")
	if err := os.WriteFile(file, []byte("k: v0\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	w, err := New()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	var fires int32
	if err := w.OnChange(file, func(string) { atomic.AddInt32(&fires, 1) }); err != nil {
		t.Fatalf("OnChange: %v", err)
	}
	w.Start()

	// First atomic save: must fire.
	atomicSave(t, file, "k: v1\n")
	if !waitFor(2*time.Second, func() bool { return atomic.LoadInt32(&fires) >= 1 }) {
		t.Fatalf("handler did not fire after first atomic save")
	}
	// Let the debounce window settle so the second save is counted
	// independently rather than coalesced with the first.
	time.Sleep(debounceWindow + 100*time.Millisecond)

	// Second atomic save over a fresh inode: this is the one that used
	// to be missed because the inode watch was already dead.
	atomicSave(t, file, "k: v2\n")
	if !waitFor(2*time.Second, func() bool { return atomic.LoadInt32(&fires) >= 2 }) {
		t.Fatalf("handler did not fire after second atomic save (watch went deaf on rename)")
	}
}

func TestWatcher_Debounce_CoalescesBurst(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "baifo.yaml")
	if err := os.WriteFile(file, []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer w.Close()

	var fires int32
	if err := w.OnChange(file, func(string) { atomic.AddInt32(&fires, 1) }); err != nil {
		t.Fatalf("OnChange: %v", err)
	}
	w.Start()

	// Fire 10 writes within ~50ms — well under the 250ms debounce
	// window. Expect a single Handler call once the dust settles.
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(file, []byte("k: v\n# noise\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait long enough for the debounce window to expire after the
	// last write, plus a safety margin.
	time.Sleep(debounceWindow + 200*time.Millisecond)

	got := atomic.LoadInt32(&fires)
	if got != 1 {
		t.Fatalf("expected 1 debounced fire, got %d", got)
	}
}

func TestWatcher_OnChangeInDir_FiresOnNewFile(t *testing.T) {
	dir := t.TempDir()

	w, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer w.Close()

	var fires int32
	if err := w.OnChangeInDir(dir, func(string) { atomic.AddInt32(&fires, 1) }); err != nil {
		t.Fatalf("OnChangeInDir: %v", err)
	}
	w.Start()

	if err := os.WriteFile(filepath.Join(dir, "new.yaml"), []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("create: %v", err)
	}

	if !waitFor(2*time.Second, func() bool { return atomic.LoadInt32(&fires) >= 1 }) {
		t.Fatalf("dir handler did not fire on new file")
	}
}

func TestWatcher_Close_Idempotent(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	w.Start()
	if err := w.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Second Close should not panic; the underlying fsnotify call may
	// return an error (already closed) which we tolerate.
	_ = w.Close()
}
