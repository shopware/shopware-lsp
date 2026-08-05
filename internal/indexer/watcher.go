//go:build !darwin

package indexer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watcherDebounce = 200 * time.Millisecond

type platformWatcherState struct {
	watcher *fsnotify.Watcher
}

func (fs *FileScanner) StartWatcher() error {
	if fs.watcher != nil {
		return fmt.Errorf("file watcher is already running")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}
	fs.watcher = watcher
	if err := fs.addDirectoryToWatcher(fs.projectRoot); err != nil {
		_ = watcher.Close()
		fs.watcher = nil
		return err
	}
	if fs.watcherCtx == nil || fs.watcherCtx.Err() != nil {
		fs.watcherCtx, fs.cancel = context.WithCancel(context.Background())
	}

	fs.watcherWg.Add(1)
	go fs.watch()
	return nil
}

func (fs *FileScanner) watch() {
	defer fs.watcherWg.Done()
	defer func() { _ = fs.watcher.Close() }()

	pendingAdds := make(map[string]struct{})
	pendingRemoves := make(map[string]struct{})
	pendingFullScan := false
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(watcherDebounce)
	}
	process := func() {
		adds := mapKeys(pendingAdds)
		removes := mapKeys(pendingRemoves)
		fullScan := pendingFullScan
		pendingAdds = make(map[string]struct{})
		pendingRemoves = make(map[string]struct{})
		pendingFullScan = false
		if fullScan {
			if err := fs.IndexAll(fs.watcherCtx); err != nil {
				log.Printf("Error rescanning changed PHAR archive: %v", err)
			}
			return
		}
		if len(adds) > 0 {
			if err := fs.IndexFiles(fs.watcherCtx, adds); err != nil {
				log.Printf("Error indexing changed files: %v", err)
			}
		}
		if len(removes) > 0 {
			if err := fs.RemoveFiles(fs.watcherCtx, removes); err != nil {
				log.Printf("Error removing deleted files: %v", err)
			}
		}
	}

	for {
		select {
		case <-fs.watcherCtx.Done():
			return
		case event, ok := <-fs.watcher.Events:
			if !ok {
				return
			}
			if isPHARArchivePath(event.Name) {
				// An archive update can add, modify, and remove thousands of
				// logical PHP sources. A normal workspace scan atomically
				// refreshes its materialized view and removes stale entries.
				pendingFullScan = true
				resetTimer()
				continue
			}
			if fs.recordEvent(event, pendingAdds, pendingRemoves) {
				resetTimer()
			}
		case err, ok := <-fs.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("File watcher error: %v", err)
		case <-timer.C:
			process()
		}
	}
}

func (fs *FileScanner) recordEvent(
	event fsnotify.Event,
	adds,
	removes map[string]struct{},
) bool {
	info, statErr := os.Stat(event.Name)
	if statErr == nil && info.IsDir() {
		if event.Op&fsnotify.Create != 0 {
			if !fs.shouldEnterDirectory(event.Name) {
				return false
			}
			if err := fs.addDirectoryToWatcher(event.Name); err != nil {
				log.Printf("Error watching new directory: %v", err)
			}
		}
		return false
	}

	if !fs.shouldIndexPath(event.Name) {
		if statErr != nil && event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			paths, err := fs.trackedPathsUnder(event.Name)
			if err != nil {
				log.Printf("Error resolving removed directory contents: %v", err)
				return false
			}
			for _, path := range paths {
				removes[path] = struct{}{}
				delete(adds, path)
			}
			return len(paths) > 0
		}
		return false
	}
	if statErr != nil || event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		if event.Op&(fsnotify.Remove|fsnotify.Rename) == 0 {
			return false
		}
		removes[event.Name] = struct{}{}
		delete(adds, event.Name)
		return true
	}
	if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
		adds[event.Name] = struct{}{}
		delete(removes, event.Name)
		return true
	}
	return false
}

func (fs *FileScanner) StopWatcher() {
	if fs.watcher == nil {
		return
	}
	fs.cancel()
	fs.watcherWg.Wait()
	fs.watcher = nil
}

func (fs *FileScanner) addDirectoryToWatcher(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("access watch path %s: %w", path, err)
		}
		if !info.IsDir() {
			return nil
		}
		if !fs.shouldEnterDirectory(path) {
			return filepath.SkipDir
		}
		if err := fs.watcher.Add(path); err != nil {
			return fmt.Errorf("watch directory %s: %w", path, err)
		}
		return nil
	})
}
