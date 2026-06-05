package engine

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// sourceExts are the file extensions whose changes warrant a reindex. Other
// files (lockfiles, images, build output) are ignored so editor churn and
// generated artifacts don't trigger needless rebuilds.
var sourceExts = map[string]bool{
	".go":   true,
	".ts":   true,
	".tsx":  true,
	".mts":  true,
	".cts":  true,
	".js":   true,
	".jsx":  true,
	".html": true,
	".json": true, // tsconfig / package manifests affect TS resolution
}

// Watcher invokes onChange (debounced) whenever a source file under root
// changes. fsnotify watches single directories, not trees, so we walk the
// project and add a watch per directory, skipping the usual noise, and add
// newly-created directories as they appear.
type Watcher struct {
	w        *fsnotify.Watcher
	debounce time.Duration
	onChange func()
	done     chan struct{}
}

// NewWatcher starts watching root (defaulting to the working directory) and
// fires onChange after a quiet period following each relevant change.
func NewWatcher(root string, debounce time.Duration, onChange func()) (*Watcher, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		w:        fw,
		debounce: debounce,
		onChange: onChange,
		done:     make(chan struct{}),
	}
	if err := w.addTree(root); err != nil {
		_ = fw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	close(w.done)
	return w.w.Close()
}

// addTree registers every (non-skipped) directory under root with fsnotify.
func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip, don't abort the walk
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		_ = w.w.Add(path)
		return nil
	})
}

// skipDir reports whether a directory should be left unwatched: VCS metadata,
// dependency and build trees, and hidden directories generally.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func (w *Watcher) loop() {
	// A single debounce timer: editors emit bursts (write, chmod, rename) and
	// a save may touch many files at once. We coalesce them into one reindex.
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	schedule := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(w.debounce, func() {
			select {
			case fire <- struct{}{}:
			default:
			}
		})
	}

	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			// Watch directories created after startup (e.g. a new package).
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					if !skipDir(filepath.Base(ev.Name)) {
						_ = w.addTree(ev.Name)
					}
					continue
				}
			}
			if !sourceExts[strings.ToLower(filepath.Ext(ev.Name))] {
				continue
			}
			schedule()
		case <-fire:
			// A debounce timer may fire concurrently with Close(); don't run a
			// reindex once we've been told to stop.
			select {
			case <-w.done:
				return
			default:
			}
			w.onChange()
		case err, ok := <-w.w.Errors:
			if !ok {
				return
			}
			log.Printf("watch error: %v", err)
		}
	}
}
