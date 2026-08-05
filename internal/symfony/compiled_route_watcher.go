package symfony

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type CompiledRouteWatcher struct {
	projectRoot string
	watcher     *fsnotify.Watcher

	mu       sync.RWMutex
	path     string
	routes   map[string]Route
	revision uint64

	watchedDirs map[string]struct{}
	wg          sync.WaitGroup
	closeOnce   sync.Once
	closeErr    error
}

func NewCompiledRouteWatcher(
	projectRoot string,
) (*CompiledRouteWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	result := &CompiledRouteWatcher{
		projectRoot: projectRoot,
		watcher:     watcher,
		routes:      make(map[string]Route),
		watchedDirs: make(map[string]struct{}),
	}
	result.watchCacheDirectories()
	if err := result.Refresh(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("Initial compiled route load failed: %v", err)
	}
	result.wg.Add(1)
	go result.watchChanges()
	return result, nil
}

func (w *CompiledRouteWatcher) Refresh() error {
	if w == nil {
		return nil
	}
	path, err := w.findRouteFile()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.publish("", nil)
		}
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	routes := ParseCompiledRoutes(path, content)
	w.publish(path, routes)
	return nil
}

func (w *CompiledRouteWatcher) Routes() ([]Route, uint64) {
	if w == nil {
		return nil, 0
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	result := make([]Route, 0, len(w.routes))
	for _, route := range w.routes {
		result = append(result, route)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].FilePath < result[right].FilePath
	})
	return result, w.revision
}

func (w *CompiledRouteWatcher) Route(name string) (Route, bool) {
	if w == nil || name == "" {
		return Route{}, false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	route, found := w.routes[name]
	return route, found
}

func (w *CompiledRouteWatcher) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		w.closeErr = w.watcher.Close()
		w.wg.Wait()
	})
	return w.closeErr
}

func (w *CompiledRouteWatcher) publish(
	path string,
	routes []Route,
) {
	next := make(map[string]Route, len(routes))
	for _, route := range routes {
		if route.Name != "" {
			next[route.Name] = route
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.path = path
	w.routes = next
	w.revision++
}

func (w *CompiledRouteWatcher) findRouteFile() (string, error) {
	type candidate struct {
		path           string
		environmentDev bool
		modern         bool
		modified       time.Time
	}
	var candidates []candidate
	for _, cacheDir := range w.cacheDirectories() {
		environmentEntries, err := os.ReadDir(cacheDir)
		if err != nil {
			continue
		}
		for _, environment := range environmentEntries {
			if !environment.IsDir() {
				continue
			}
			environmentDir := filepath.Join(cacheDir, environment.Name())
			files, readErr := os.ReadDir(environmentDir)
			if readErr != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() || !isCompiledRouteFileName(file.Name()) {
					continue
				}
				info, infoErr := file.Info()
				if infoErr != nil {
					continue
				}
				candidates = append(candidates, candidate{
					path: filepath.Join(
						environmentDir,
						file.Name(),
					),
					environmentDev: strings.HasPrefix(
						strings.ToLower(environment.Name()),
						"dev",
					),
					modern:   file.Name() == "url_generating_routes.php",
					modified: info.ModTime(),
				})
			}
		}
	}
	if len(candidates) == 0 {
		return "", os.ErrNotExist
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].environmentDev !=
			candidates[right].environmentDev {
			return candidates[left].environmentDev
		}
		if candidates[left].modern != candidates[right].modern {
			return candidates[left].modern
		}
		if !candidates[left].modified.Equal(candidates[right].modified) {
			return candidates[left].modified.After(
				candidates[right].modified,
			)
		}
		return candidates[left].path < candidates[right].path
	})
	return candidates[0].path, nil
}

func (w *CompiledRouteWatcher) watchCacheDirectories() {
	for _, cacheDir := range w.cacheDirectories() {
		w.addWatchDirectory(cacheDir)
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			parent := filepath.Dir(cacheDir)
			w.addWatchDirectory(parent)
			if _, exists := w.watchedDirs[parent]; !exists {
				w.addWatchDirectory(w.projectRoot)
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				w.addWatchDirectory(filepath.Join(cacheDir, entry.Name()))
			}
		}
	}
}

func (w *CompiledRouteWatcher) cacheDirectories() []string {
	return []string{
		filepath.Join(w.projectRoot, "var", "cache"),
		filepath.Join(w.projectRoot, "app", "cache"),
	}
}

func isCompiledRouteFileName(name string) bool {
	return name == "url_generating_routes.php" ||
		name == "UrlGenerator.php" ||
		strings.HasSuffix(name, "UrlGenerator.php")
}

func (w *CompiledRouteWatcher) addWatchDirectory(path string) {
	if path == "" {
		return
	}
	if _, exists := w.watchedDirs[path]; exists {
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	if err := w.watcher.Add(path); err != nil {
		log.Printf("Failed to watch compiled route directory %s: %v", path, err)
		return
	}
	w.watchedDirs[path] = struct{}{}
}

func (w *CompiledRouteWatcher) watchChanges() {
	defer w.wg.Done()
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			createdRelevantDirectory := false
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil &&
					info.IsDir() &&
					w.shouldWatchCreatedDirectory(event.Name) {
					w.addWatchDirectory(event.Name)
					w.watchCacheDirectories()
					createdRelevantDirectory = true
				}
			}
			if !createdRelevantDirectory &&
				!isCompiledRouteFileName(filepath.Base(event.Name)) {
				continue
			}
			if err := w.Refresh(); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				log.Printf("Failed to reload compiled routes: %v", err)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Compiled route watcher error: %v", err)
		}
	}
}

func (w *CompiledRouteWatcher) shouldWatchCreatedDirectory(
	path string,
) bool {
	path = filepath.Clean(path)
	for _, cacheDir := range w.cacheDirectories() {
		parent := filepath.Dir(cacheDir)
		if path == parent ||
			path == cacheDir ||
			filepath.Dir(path) == cacheDir {
			return true
		}
	}
	return false
}
