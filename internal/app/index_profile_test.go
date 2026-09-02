//go:build integration

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/runtimeconfig"
	"github.com/stretchr/testify/require"
)

// TestShopwareTrunkIndexingProfile is a deliberately small, opt-in resource
// harness for the production workspace composition. Keep feature assertions
// in TestShopwareTrunkIndexing; this test isolates construction, cold
// indexing, a warm no-op scan, retained Go heap, and on-disk cache size.
//
// Build the test binary first when collecting OS-level peak RSS and CPU time:
//
//	go test -c -tags=integration ./internal/app -o app-index-profile.test
//	/usr/bin/time -l ./app-index-profile.test \
//	  -test.run '^TestShopwareTrunkIndexingProfile$' -test.v
func TestShopwareTrunkIndexingProfile(t *testing.T) {
	runtimeconfig.ApplyMemoryPolicy()
	root := realWorldProjectRoot(t)
	cacheRoot := t.TempDir()
	if override := os.Getenv("SHOPWARE_LSP_PROFILE_CACHE_DIR"); override != "" {
		cacheRoot = override
		require.NoError(t, os.MkdirAll(cacheRoot, 0o755))
	}
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)
	ctx := context.Background()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	constructedAt := time.Now()
	server := lsp.NewServer(nil, root, "index-profile")
	workspace, err := NewWorkspace(ctx, root, server)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, workspace.Close()) })
	constructionElapsed := time.Since(constructedAt)
	if configuredWorkers := os.Getenv(
		"SHOPWARE_LSP_PROFILE_WORKERS",
	); configuredWorkers != "" {
		workers, parseErr := strconv.Atoi(configuredWorkers)
		require.NoError(t, parseErr)
		require.Positive(t, workers)
		workspace.Scanner().SetWorkerCount(workers)
	}

	var phpIndex *php.PHPIndex
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*php.PHPIndex); ok {
			phpIndex = candidate
			break
		}
	}
	require.NotNil(t, phpIndex)

	coldStarted := time.Now()
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	coldElapsed := time.Since(coldStarted)

	var cold runtime.MemStats
	runtime.ReadMemStats(&cold)
	if os.Getenv("SHOPWARE_LSP_PROFILE_SEMANTIC_STORAGE") != "" {
		t.Logf(
			"semantic storage: %+v",
			phpIndex.SemanticSnapshot().WorkspaceStorageStats(),
		)
	}
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	retainedRSS := profileCurrentRSS()
	writeLiveHeapProfile(t)

	warmStarted := time.Now()
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	warmElapsed := time.Since(warmStarted)

	if os.Getenv("SHOPWARE_LSP_PROFILE_REVERSE_REFERENCES") != "" {
		runtime.GC()
		var referencesBefore runtime.MemStats
		runtime.ReadMemStats(&referencesBefore)
		referencesStarted := time.Now()
		// Any lookup initializes the complete immutable reverse index. An empty
		// ID keeps this resource measurement independent of checkout contents.
		_ = phpIndex.SemanticSnapshot().ReferencesTo("")
		referencesElapsed := time.Since(referencesStarted)
		runtime.GC()
		var referencesAfter runtime.MemStats
		runtime.ReadMemStats(&referencesAfter)
		writeHeapProfile(
			t,
			"SHOPWARE_LSP_REVERSE_HEAP_PROFILE",
		)
		t.Logf(
			"reverse-reference profile: first_lookup=%s retained_growth=%s "+
				"allocation=%s mallocs=%d",
			referencesElapsed.Round(time.Millisecond),
			formatBytes(referencesAfter.HeapAlloc-referencesBefore.HeapAlloc),
			formatBytes(referencesAfter.TotalAlloc-referencesBefore.TotalAlloc),
			referencesAfter.Mallocs-referencesBefore.Mallocs,
		)
	}

	cacheBytes, cacheFiles, err := profileDirectorySize(cacheRoot)
	require.NoError(t, err)
	cacheEntries, err := profileDirectoryEntries(cacheRoot)
	require.NoError(t, err)
	t.Logf(
		"index profile: construction=%s cold=%s warm=%s classes=%d gomaxprocs=%d num_cpu=%d "+
			"heap_cold=%s heap_retained=%s heap_inuse=%s heap_idle=%s "+
			"heap_released=%s heap_sys=%s stack_sys=%s other_sys=%s "+
			"go_sys=%s rss_retained=%s "+
			"total_alloc=%s total_alloc_bytes=%d mallocs=%d frees=%d "+
			"num_gc=%d gc_pause=%s "+
			"cache=%s cache_files=%d",
		constructionElapsed.Round(time.Millisecond),
		coldElapsed.Round(time.Millisecond),
		warmElapsed.Round(time.Millisecond),
		len(phpIndex.ClassSymbols()),
		runtime.GOMAXPROCS(0),
		runtime.NumCPU(),
		formatBytes(cold.HeapAlloc),
		formatBytes(retained.HeapAlloc),
		formatBytes(retained.HeapInuse),
		formatBytes(retained.HeapIdle),
		formatBytes(retained.HeapReleased),
		formatBytes(retained.HeapSys),
		formatBytes(retained.StackSys),
		formatBytes(retained.OtherSys),
		formatBytes(retained.Sys),
		formatBytes(retainedRSS),
		formatBytes(retained.TotalAlloc-before.TotalAlloc),
		retained.TotalAlloc-before.TotalAlloc,
		retained.Mallocs-before.Mallocs,
		retained.Frees-before.Frees,
		retained.NumGC-before.NumGC,
		time.Duration(retained.PauseTotalNs-before.PauseTotalNs),
		formatBytes(cacheBytes),
		cacheFiles,
	)
	t.Logf("cache files: %s", strings.Join(cacheEntries, ", "))

	if documentPath := os.Getenv(
		"SHOPWARE_LSP_PROFILE_SEMANTIC_DOCUMENT",
	); documentPath != "" {
		runtime.GC()
		var documentBefore runtime.MemStats
		runtime.ReadMemStats(&documentBefore)
		documentStarted := time.Now()
		document, found := phpIndex.SemanticDocument(documentPath)
		documentElapsed := time.Since(documentStarted)
		require.True(t, found, "profile semantic document %s", documentPath)
		var documentAfter runtime.MemStats
		runtime.ReadMemStats(&documentAfter)
		t.Logf(
			"semantic document profile: path=%s elapsed=%s symbols=%d "+
				"references=%d allocation=%s mallocs=%d",
			documentPath,
			documentElapsed.Round(time.Microsecond),
			len(document.Symbols),
			len(document.References),
			formatBytes(
				documentAfter.TotalAlloc-documentBefore.TotalAlloc,
			),
			documentAfter.Mallocs-documentBefore.Mallocs,
		)
		runtime.KeepAlive(document)
	}
}

func writeLiveHeapProfile(t *testing.T) {
	t.Helper()
	writeHeapProfile(t, "SHOPWARE_LSP_HEAP_PROFILE")
}

func writeHeapProfile(t *testing.T, environmentVariable string) {
	t.Helper()
	profilePath := os.Getenv(environmentVariable)
	if profilePath == "" {
		return
	}
	file, err := os.Create(profilePath)
	require.NoError(t, err)
	require.NoError(t, pprof.WriteHeapProfile(file))
	require.NoError(t, file.Close())
}

func profileCurrentRSS() uint64 {
	output, err := exec.Command(
		"ps",
		"-o",
		"rss=",
		"-p",
		strconv.Itoa(os.Getpid()),
	).Output()
	if err != nil {
		return 0
	}
	kib, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return kib * 1024
}

func profileDirectorySize(
	root string,
) (uint64, int, error) {
	var size uint64
	files := 0
	err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 0 {
			size += uint64(info.Size())
		}
		files++
		return nil
	})
	return size, files, err
}

func profileDirectoryEntries(root string) ([]string, error) {
	type cacheEntry struct {
		path string
		size int64
	}
	var entries []cacheEntry
	err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, cacheEntry{
			path: relativePath,
			size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].size > entries[j].size
	})
	formatted := make([]string, 0, len(entries))
	for _, entry := range entries {
		formatted = append(
			formatted,
			entry.path+"="+formatBytes(uint64(max(entry.size, 0))),
		)
	}
	return formatted, nil
}
