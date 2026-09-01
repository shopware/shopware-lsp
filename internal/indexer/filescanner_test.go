package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultIndexWorkerCountIsLightweightAndCPUAware(t *testing.T) {
	t.Parallel()
	require.Equal(t, 1, defaultIndexWorkerCount(0))
	require.Equal(t, 1, defaultIndexWorkerCount(1))
	require.Equal(t, 2, defaultIndexWorkerCount(2))
	require.Equal(t, 4, defaultIndexWorkerCount(8))
	require.Equal(t, 4, defaultIndexWorkerCount(64))
}

func TestPreparationBatchReadyBoundsFilesAndSourceWeight(t *testing.T) {
	t.Parallel()

	require.False(t, preparationBatchReady(0, 0))
	require.False(t, preparationBatchReady(
		maxPreparationBatchFiles-1,
		maxPreparationBatchBytes-1,
	))
	require.True(t, preparationBatchReady(maxPreparationBatchFiles, 0))
	require.True(t, preparationBatchReady(1, maxPreparationBatchBytes))
	require.True(t, preparationBatchReady(
		maxPreparationBatchFiles,
		maxPreparationBatchBytes,
	))

	require.False(t, preparationBatchWouldOverflow(
		0,
		0,
		maxPreparationBatchBytes+1,
	))
	require.False(t, preparationBatchWouldOverflow(
		maxPreparationBatchFiles-1,
		maxPreparationBatchBytes-1,
		1,
	))
	require.True(t, preparationBatchWouldOverflow(
		maxPreparationBatchFiles,
		0,
		1,
	))
	require.True(t, preparationBatchWouldOverflow(
		1,
		maxPreparationBatchBytes-1,
		2,
	))
}

func TestFileScannerSourceSizeRejection(t *testing.T) {
	t.Parallel()

	scanner := &FileScanner{}
	scanner.maxFileSize.Store(8 << 20)
	reason, limit, rejected := scanner.sourceSizeRejection(8 << 20)
	require.False(t, rejected)
	require.Empty(t, reason)
	require.Zero(t, limit)

	reason, limit, rejected = scanner.sourceSizeRejection(8<<20 + 1)
	require.True(t, rejected)
	require.Equal(t, skippedConfiguredLimit, reason)
	require.EqualValues(t, 8<<20, limit)

	scanner.maxFileSize.Store(0)
	reason, limit, rejected = scanner.sourceSizeRejection(MaximumSourceFileBytes + 1)
	require.True(t, rejected)
	require.Equal(t, skippedParserRange, reason)
	require.Equal(t, MaximumSourceFileBytes, limit)
}

func TestFileScannerSkipsOversizedFilesAndReportsLargestFiles(t *testing.T) {
	root := t.TempDir()
	smallPath := filepath.Join(root, "small.php")
	largePath := filepath.Join(root, "large.php")
	require.NoError(t, os.WriteFile(smallPath, []byte("<?php"), 0o644))
	require.NoError(t, os.WriteFile(
		largePath,
		[]byte("<?php generated source"),
		0o644,
	))

	idx := &mockIndexer{indexedFiles: make(map[string]bool)}
	scanner, err := NewFileScanner(root, filepath.Join(t.TempDir(), "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.SetMaxFileSizeBytes(8)
	scanner.AddIndexer(idx)

	require.NoError(t, scanner.IndexAll(context.Background()))
	require.True(t, idx.indexedFiles[smallPath])
	require.False(t, idx.indexedFiles[largePath])

	stats, err := scanner.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.TrackedFiles)
	require.EqualValues(t, len("<?php"), stats.TrackedBytes)
	require.EqualValues(t, 8, stats.MaxFileSizeBytes)
	require.Equal(t, []FileSizeStats{{
		Path: smallPath, Bytes: int64(len("<?php")),
	}}, stats.LargestIndexedFiles)
	require.Equal(t, 1, stats.SkippedOversizedCount)
	require.EqualValues(t, len("<?php generated source"), stats.SkippedOversizedBytes)
	require.Equal(t, []SkippedFileStats{{
		Path: largePath, Bytes: int64(len("<?php generated source")),
		LimitBytes: 8, Reason: skippedConfiguredLimit,
	}}, stats.LargestSkippedFiles)
}

func TestFileScannerRemovesFileThatGrowsBeyondLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "generated.php")
	require.NoError(t, os.WriteFile(path, []byte("<?php"), 0o644))

	idx := &mockIndexer{indexedFiles: make(map[string]bool)}
	scanner, err := NewFileScanner(root, filepath.Join(t.TempDir(), "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.SetMaxFileSizeBytes(8)
	scanner.AddIndexer(idx)

	require.NoError(t, scanner.IndexFiles(context.Background(), []string{path}))
	require.True(t, idx.indexedFiles[path])
	require.NoError(t, os.WriteFile(path, []byte("<?php generated source"), 0o644))
	require.NoError(t, scanner.IndexFiles(context.Background(), []string{path}))
	require.False(t, idx.indexedFiles[path])

	stats, err := scanner.Stats(context.Background())
	require.NoError(t, err)
	require.Zero(t, stats.TrackedFiles)
	require.Equal(t, 1, stats.SkippedOversizedCount)

	scanner.SetMaxFileSizeBytes(64)
	require.NoError(t, scanner.IndexFiles(context.Background(), []string{path}))
	require.True(t, idx.indexedFiles[path])
	stats, err = scanner.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.TrackedFiles)
	require.Zero(t, stats.SkippedOversizedCount)
}

func TestShouldSkipRelPathChecksComponentsWithoutAllocating(t *testing.T) {
	separator := string(os.PathSeparator)

	require.False(t, shouldSkipRelPath(""))
	require.False(t, shouldSkipRelPath("."))
	require.False(t, shouldSkipRelPath(
		strings.Join([]string{"src", "variant"}, separator),
	))
	require.True(t, shouldSkipRelPath(
		strings.Join([]string{"src", "node_modules", "package"}, separator),
	))
	require.True(t, shouldSkipRelPath(
		strings.Join([]string{"public", "build"}, separator),
	))
	require.True(t, shouldSkipRelPath(
		strings.Join([]string{"Resources", "app", "administration", ".tmp", "vite"}, separator),
	))

	path := strings.Join(
		[]string{"src", "Storefront", "Resources", "views"},
		separator,
	)
	require.Zero(t, testing.AllocsPerRun(1_000, func() {
		_ = shouldSkipRelPath(path)
	}))
}

func TestCompareRawPathMatchesLexicalStringOrderWithoutAllocating(t *testing.T) {
	require.Negative(t, compareRawPath([]byte("/a.php"), "/b.php"))
	require.Zero(t, compareRawPath([]byte("/same.php"), "/same.php"))
	require.Positive(t, compareRawPath([]byte("/nested.php"), "/nest.php"))
	require.Zero(t, testing.AllocsPerRun(1_000, func() {
		_ = compareRawPath([]byte("/workspace/src/service.php"), "/workspace/src/service.php")
	}))
}

func TestFileScanner_DoesNotCommitFingerprintAfterIndexerFailure(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "broken.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	failing := &controlledIndexer{err: errors.New("index failed")}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()
	scanner.AddIndexer(failing)

	err = scanner.IndexFiles(context.Background(), []string{filePath})
	require.ErrorContains(t, err, "index failed")

	var count int
	require.NoError(t, scanner.db.QueryRow("SELECT COUNT(*) FROM file_hashes WHERE path = ?", filePath).Scan(&count))
	require.Zero(t, count)

	failing.err = nil
	require.NoError(t, scanner.IndexFiles(context.Background(), []string{filePath}))
	require.NoError(t, scanner.db.QueryRow("SELECT COUNT(*) FROM file_hashes WHERE path = ?", filePath).Scan(&count))
	require.Equal(t, 1, count)
}

func TestFileScannerUpdatesFileStatesInBatches(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.php")
	require.NoError(t, os.WriteFile(sourcePath, []byte("<?php"), 0o644))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })

	const stateCount = 257
	states := make([]fileState, stateCount)
	for index := range states {
		states[index] = fileState{
			path: filepath.Join(
				tempDir,
				"state-"+strconv.Itoa(index)+".php",
			),
			info: info,
		}
	}
	require.NoError(
		t,
		scanner.updateFileStates(context.Background(), states),
	)

	var count int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes",
	).Scan(&count))
	require.Equal(t, stateCount, count)
	for _, index := range []int{0, 127, 128, stateCount - 1} {
		var size, mtime int64
		require.NoError(t, scanner.db.QueryRow(
			"SELECT size, mtime FROM file_hashes WHERE path = ?",
			states[index].path,
		).Scan(&size, &mtime))
		require.Equal(t, info.Size(), size)
		require.Equal(t, info.ModTime().UnixNano(), mtime)
	}
}

func TestFileScanner_RollsBackWorkspaceRepositoriesTogether(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "atomic.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	store, err := NewStore(filepath.Join(tempDir, "indexes.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	firstRepository, err := NewDataIndexerInStore[string](store, "first")
	require.NoError(t, err)
	secondRepository, err := NewDataIndexerInStore[string](store, "second")
	require.NoError(t, err)
	require.NoError(t, firstRepository.BatchSaveItems(map[string]map[string]string{
		filePath: {"value": "old-first"},
	}))
	require.NoError(t, secondRepository.BatchSaveItems(map[string]map[string]string{
		filePath: {"value": "old-second"},
	}))

	first := &repositoryIndexer{repository: firstRepository, value: "new-first"}
	second := &repositoryIndexer{
		repository: secondRepository,
		value:      "new-second",
		err:        errors.New("second index failed"),
	}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
		store,
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()
	scanner.AddIndexer(first)
	scanner.AddIndexer(second)

	require.ErrorContains(
		t,
		scanner.IndexFiles(context.Background(), []string{filePath}),
		"second index failed",
	)
	firstValues, err := firstRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"old-first"}, firstValues)
	secondValues, err := secondRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"old-second"}, secondValues)

	second.err = nil
	require.NoError(t, scanner.IndexFiles(context.Background(), []string{filePath}))
	firstValues, err = firstRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"new-first"}, firstValues)
	secondValues, err = secondRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"new-second"}, secondValues)
}

func TestFileScanner_BatchFailureRetriesFilesIndividually(t *testing.T) {
	tempDir := t.TempDir()
	goodPath := filepath.Join(tempDir, "good.php")
	badPath := filepath.Join(tempDir, "bad.php")
	require.NoError(t, os.WriteFile(goodPath, []byte("<?php"), 0o644))
	require.NoError(t, os.WriteFile(badPath, []byte("<?php"), 0o644))

	store, err := NewStore(filepath.Join(tempDir, "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	repository, err := NewDataIndexerInStore[string](store, "batch-retry")
	require.NoError(t, err)
	indexer := &repositoryIndexer{
		repository: repository,
		value:      "indexed",
		err:        errors.New("bad file"),
		failPath:   badPath,
	}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
		store,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.workerCount = 1
	scanner.AddIndexer(indexer)

	require.ErrorContains(
		t,
		scanner.IndexFiles(context.Background(), []string{goodPath, badPath}),
		"bad file",
	)
	values, err := repository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"indexed"}, values)

	var goodTracked, badTracked int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes WHERE path = ?",
		goodPath,
	).Scan(&goodTracked))
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes WHERE path = ?",
		badPath,
	).Scan(&badTracked))
	require.Equal(t, 1, goodTracked)
	require.Zero(t, badTracked)
}

func TestFileScanner_RollsBackRepositoryRemovalAndClear(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "atomic.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	store, err := NewStore(filepath.Join(tempDir, "indexes.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	firstRepository, err := NewDataIndexerInStore[string](store, "first")
	require.NoError(t, err)
	secondRepository, err := NewDataIndexerInStore[string](store, "second")
	require.NoError(t, err)
	seed := func() {
		require.NoError(t, firstRepository.BatchSaveItems(map[string]map[string]string{
			filePath: {"value": "first"},
		}))
		require.NoError(t, secondRepository.BatchSaveItems(map[string]map[string]string{
			filePath: {"value": "second"},
		}))
	}
	seed()

	first := &repositoryIndexer{repository: firstRepository}
	second := &repositoryIndexer{
		repository: secondRepository,
		removeErr:  errors.New("remove failed"),
	}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"), store)
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()
	scanner.AddIndexer(first)
	scanner.AddIndexer(second)

	require.ErrorContains(
		t,
		scanner.RemoveFiles(context.Background(), []string{filePath}),
		"remove failed",
	)
	firstValues, err := firstRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, firstValues)
	secondValues, err := secondRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, secondValues)

	second.removeErr = nil
	require.NoError(t, scanner.RemoveFiles(context.Background(), []string{filePath}))
	firstValues, err = firstRepository.GetValues("value")
	require.NoError(t, err)
	require.Empty(t, firstValues)
	secondValues, err = secondRepository.GetValues("value")
	require.NoError(t, err)
	require.Empty(t, secondValues)

	seed()
	second.clearErr = errors.New("clear failed")
	require.ErrorContains(t, scanner.ClearHashes(), "clear failed")
	firstValues, err = firstRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, firstValues)
	secondValues, err = secondRepository.GetValues("value")
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, secondValues)
}

func TestFileScanner_RemoveFilesBatchesFingerprintDeletion(t *testing.T) {
	tempDir := t.TempDir()
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()

	const fileCount = 257
	paths := make([]string, fileCount)
	statement, err := scanner.db.Prepare(
		"INSERT INTO file_hashes (path, size, mtime) VALUES (?, 1, 1)",
	)
	require.NoError(t, err)
	for index := range fileCount {
		paths[index] = filepath.Join(
			tempDir,
			fmt.Sprintf("removed-%03d.php", index),
		)
		_, err := statement.Exec(paths[index])
		require.NoError(t, err)
	}
	survivor := filepath.Join(tempDir, "survivor.php")
	_, err = statement.Exec(survivor)
	require.NoError(t, err)
	require.NoError(t, statement.Close())

	require.NoError(t, scanner.RemoveFiles(context.Background(), paths))
	var remaining int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes",
	).Scan(&remaining))
	require.Equal(t, 1, remaining)
	var tracked int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes WHERE path = ?",
		survivor,
	).Scan(&tracked))
	require.Equal(t, 1, tracked)
}

func TestFileScanner_HonorsCanceledContext(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "canceled.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	idx := &controlledIndexer{}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()
	scanner.AddIndexer(idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, scanner.IndexFiles(ctx, []string{filePath}), context.Canceled)
	require.Zero(t, idx.calls.Load())
}

func TestFileScanner_SerializesConcurrentRuns(t *testing.T) {
	tempDir := t.TempDir()
	firstPath := filepath.Join(tempDir, "first.php")
	secondPath := filepath.Join(tempDir, "second.php")
	require.NoError(t, os.WriteFile(firstPath, []byte("<?php"), 0o644))
	require.NoError(t, os.WriteFile(secondPath, []byte("<?php"), 0o644))

	idx := &controlledIndexer{entered: make(chan struct{}, 2), release: make(chan struct{})}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()
	scanner.AddIndexer(idx)

	results := make(chan error, 2)
	go func() { results <- scanner.IndexFiles(context.Background(), []string{firstPath}) }()
	<-idx.entered
	go func() { results <- scanner.IndexFiles(context.Background(), []string{secondPath}) }()

	require.Never(t, func() bool { return len(idx.entered) > 0 }, 50*time.Millisecond, time.Millisecond)
	close(idx.release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.EqualValues(t, 1, idx.maxActive.Load())
}

func TestFileScanner_DoesNotOwnIndexers(t *testing.T) {
	tempDir := t.TempDir()
	idx := &controlledIndexer{}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	scanner.AddIndexer(idx)
	require.NoError(t, scanner.Close())
	require.Zero(t, idx.closes.Load())
}

func TestFileScanner_WatcherCanRestart(t *testing.T) {
	tempDir := t.TempDir()
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, scanner.Close()) }()

	require.NoError(t, scanner.StartWatcher())
	require.ErrorContains(t, scanner.StartWatcher(), "already running")
	scanner.StopWatcher()
	require.NoError(t, scanner.StartWatcher())
	scanner.StopWatcher()
}

func TestFileScanner_IndexFiles_SkipDirs(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create test directory structure with files
	createTestFiles(t, tempDir)

	// Create a mock indexer that tracks which files are indexed
	mockIndexer := &mockIndexer{
		indexedFiles: make(map[string]bool),
	}

	// Create a file scanner with the mock indexer
	fs, err := NewFileScanner(tempDir, filepath.Join(tempDir, "test.db"))
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, fs.Close())
	}()

	// Add the mock indexer
	fs.AddIndexer(mockIndexer)

	// Create a list of files to index
	var files []string
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".php" {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)

	// Index the files
	err = fs.IndexFiles(context.Background(), files)
	require.NoError(t, err)

	// Verify that files in excluded directories were not indexed
	for path := range mockIndexer.indexedFiles {
		relPath, err := filepath.Rel(tempDir, path)
		require.NoError(t, err)

		// Check that the file is not in any excluded directory
		pathParts := strings.Split(relPath, string(os.PathSeparator))
		for _, part := range pathParts {
			assert.False(t, defaultSkipDirs[part], "File in excluded directory was indexed: %s", path)
		}
	}

	// Verify that files in regular directories were indexed
	regularFile := filepath.Join(tempDir, "regular", "file.php")
	assert.True(t, mockIndexer.indexedFiles[regularFile], "Regular file was not indexed")

	// Verify that files in excluded directories were not indexed
	excludedFiles := []string{
		filepath.Join(tempDir, ".devenv", "file.php"),
		filepath.Join(tempDir, "node_modules", "file.php"),
		filepath.Join(tempDir, "vendor-bin", "file.php"),
		filepath.Join(tempDir, "tests", "file.php"),
		filepath.Join(tempDir, "nested", "node_modules", "file.php"),
	}

	for _, file := range excludedFiles {
		assert.False(t, mockIndexer.indexedFiles[file], "Excluded file was indexed: %s", file)
	}
}

func TestFileScanner_IndexAll_SkipDirsAtAnyDepth(t *testing.T) {
	tempDir := t.TempDir()

	createTestFiles(t, tempDir)

	mockIndexer := &mockIndexer{
		indexedFiles: make(map[string]bool),
	}

	fs, err := NewFileScanner(tempDir, filepath.Join(tempDir, "test.db"))
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, fs.Close())
	}()

	fs.AddIndexer(mockIndexer)

	err = fs.IndexAll(context.Background())
	require.NoError(t, err)

	assert.True(t, mockIndexer.indexedFiles[filepath.Join(tempDir, "regular", "file.php")], "Regular file was not indexed")
	assert.False(t, mockIndexer.indexedFiles[filepath.Join(tempDir, ".devenv", "file.php")], "Excluded file was indexed")
	assert.False(t, mockIndexer.indexedFiles[filepath.Join(tempDir, "nested", "node_modules", "file.php")], "Excluded file was indexed")
}

func TestShouldSkipRelPathKeepsComposerPackagesNamedCache(t *testing.T) {
	t.Parallel()
	require.True(t, shouldSkipRelPath(filepath.Join("var", "cache", "file.php")))
	require.True(t, shouldSkipRelPath(filepath.Join("nested", "cache", "file.php")))
	require.False(t, shouldSkipRelPath(filepath.Join(
		"vendor",
		"symfony",
		"cache",
		"Adapter",
		"AdapterInterface.php",
	)))
	require.True(t, shouldSkipRelPath(filepath.Join(
		"vendor",
		"package",
		"node_modules",
		"generated.php",
	)))
}

func TestShouldSkipRelPathIgnoresToolingDirectoriesAtAnyDepth(t *testing.T) {
	t.Parallel()

	for _, directory := range []string{
		".claude",
		".codex",
		".continue",
		".cursor",
		".delta",
		".ddev",
		".devbox",
		".devenv",
		".direnv",
		".fleet",
		".windsurf",
		".zed",
	} {
		require.True(t, shouldSkipRelPath(filepath.Join(directory, "config.yaml")))
		require.True(t, shouldSkipRelPath(filepath.Join("nested", directory, "config.yaml")))
	}
}

func TestFileScanner_SupplementalIndexerSelectivelyReopensSkippedDirectory(
	t *testing.T,
) {
	tempDir := t.TempDir()
	publicBuild := filepath.Join(tempDir, "public", "build")
	publicMedia := filepath.Join(tempDir, "public", "media")
	require.NoError(t, os.MkdirAll(publicBuild, 0o755))
	require.NoError(t, os.MkdirAll(publicMedia, 0o755))
	buildPath := filepath.Join(publicBuild, "app.css")
	mediaPath := filepath.Join(publicMedia, "upload.jpg")
	require.NoError(t, os.WriteFile(buildPath, []byte("body{}"), 0o644))
	require.NoError(t, os.WriteFile(mediaPath, []byte("image"), 0o644))

	mock := &supplementalMockIndexer{
		mockIndexer: mockIndexer{indexedFiles: make(map[string]bool)},
		publicBuild: publicBuild,
	}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(mock)

	require.NoError(t, scanner.IndexAll(context.Background()))
	assert.True(t, mock.indexedFiles[buildPath])
	assert.False(t, mock.indexedFiles[mediaPath])
}

func TestFileScanner_ConfiguredExclusionsVetoNormalAndSupplementalPaths(
	t *testing.T,
) {
	root := t.TempDir()
	generatedPath := filepath.Join(root, "src", "generated", "Model.php")
	regularPath := filepath.Join(root, "src", "Service.php")
	publicBuild := filepath.Join(root, "public", "build")
	publicPath := filepath.Join(publicBuild, "app.js")
	for _, path := range []string{generatedPath, regularPath, publicPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o644))
	}

	mock := &supplementalMockIndexer{
		mockIndexer: mockIndexer{indexedFiles: make(map[string]bool)},
		publicBuild: publicBuild,
	}
	scanner, err := NewFileScanner(root, filepath.Join(root, "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	require.NoError(t, scanner.SetExcludedPaths([]string{
		"**/generated/**",
		"public/build/**",
	}))
	scanner.AddIndexer(mock)

	require.NoError(t, scanner.IndexAll(context.Background()))
	assert.True(t, mock.indexedFiles[regularPath])
	assert.False(t, mock.indexedFiles[generatedPath])
	assert.False(t, mock.indexedFiles[publicPath])

	require.NoError(t, scanner.IndexFiles(
		context.Background(),
		[]string{generatedPath, publicPath},
	))
	assert.False(t, mock.indexedFiles[generatedPath])
	assert.False(t, mock.indexedFiles[publicPath])
}

func TestFileScanner_ConfiguredExclusionsRemoveWarmTrackedFiles(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	generatedPath := filepath.Join(root, "src", "generated", "Model.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(generatedPath), 0o755))
	require.NoError(t, os.WriteFile(generatedPath, []byte("<?php"), 0o644))

	mock := &mockIndexer{indexedFiles: make(map[string]bool)}
	dbPath := filepath.Join(cache, "scanner.db")
	first, err := NewFileScanner(root, dbPath)
	require.NoError(t, err)
	first.AddIndexer(mock)
	require.NoError(t, first.IndexAll(context.Background()))
	require.True(t, mock.indexedFiles[generatedPath])
	require.NoError(t, first.Close())

	second, err := NewFileScanner(root, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	require.NoError(t, second.SetExcludedPaths([]string{"**/generated/**"}))
	second.AddIndexer(mock)
	require.NoError(t, second.IndexAll(context.Background()))
	require.False(t, mock.indexedFiles[generatedPath])
}

func TestFileScanner_DoesNotPreparseSupplementalResourceFiles(t *testing.T) {
	tempDir := t.TempDir()
	publicBuild := filepath.Join(tempDir, "public", "build")
	require.NoError(t, os.MkdirAll(publicBuild, 0o755))
	resourcePath := filepath.Join(publicBuild, "app.js")
	require.NoError(t, os.WriteFile(
		resourcePath,
		[]byte("const broken = '"),
		0o644,
	))

	mock := &supplementalMockIndexer{
		mockIndexer:   mockIndexer{indexedFiles: make(map[string]bool)},
		publicBuild:   publicBuild,
		inspectSyntax: true,
	}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(mock)

	require.NoError(t, scanner.IndexAll(context.Background()))
	assert.False(t, mock.syntaxPrepared)
}

func TestFileScanner_IndexAll_RemovesFilesDeletedWhileStopped(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "stale.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	mockIndexer := &mockIndexer{indexedFiles: make(map[string]bool)}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(mockIndexer)

	require.NoError(t, scanner.IndexAll(context.Background()))
	require.True(t, mockIndexer.indexedFiles[filePath])
	require.NoError(t, os.Remove(filePath))

	require.NoError(t, scanner.IndexAll(context.Background()))
	require.False(t, mockIndexer.indexedFiles[filePath])
	var tracked int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes WHERE path = ?",
		filePath,
	).Scan(&tracked))
	require.Zero(t, tracked)
}

func TestFileScanner_ScanFileStatesAlignsAndFindsStalePaths(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })

	stored := []struct {
		path  string
		size  int
		mtime int
	}{
		{path: "/a.php", size: 10, mtime: 20},
		{path: "/b.php", size: 11, mtime: 21},
		{path: "/d.php", size: 12, mtime: 22},
		{path: "/f.php", size: 13, mtime: 23},
	}
	for _, state := range stored {
		_, err := scanner.db.Exec(
			"INSERT INTO file_hashes(path, size, mtime) VALUES (?, ?, ?)",
			state.path,
			state.size,
			state.mtime,
		)
		require.NoError(t, err)
	}

	states, stale, err := scanner.scanFileStates(
		context.Background(),
		[]string{"/b.php", "/c.php", "/e.php"},
		true,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"/a.php", "/d.php", "/f.php"}, stale)
	require.Equal(t, []storedFileState{
		{size: 11, mtime: 21, found: true},
		{},
		{},
	}, states)
}

func TestFileScanner_LoadFileStatesAlignsWithSortedPaths(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })

	for index, path := range []string{"/a.php", "/c.php"} {
		_, err := scanner.db.Exec(
			"INSERT INTO file_hashes(path, size, mtime) VALUES (?, ?, ?)",
			path,
			index+10,
			index+20,
		)
		require.NoError(t, err)
	}

	states, err := scanner.loadFileStates(
		context.Background(),
		[]string{"/a.php", "/b.php", "/c.php"},
	)
	require.NoError(t, err)
	require.Equal(t, []storedFileState{
		{size: 10, mtime: 20, found: true},
		{},
		{size: 11, mtime: 21, found: true},
	}, states)
}

func BenchmarkFileScannerScanFileStates(b *testing.B) {
	tempDir := b.TempDir()
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, scanner.Close()) })

	const fileCount = 4096
	files := make([]string, fileCount)
	tx, err := scanner.db.Begin()
	require.NoError(b, err)
	statement, err := tx.Prepare(
		"INSERT INTO file_hashes(path, size, mtime) VALUES (?, ?, ?)",
	)
	require.NoError(b, err)
	for index := range files {
		path := fmt.Sprintf("/workspace/src/file-%05d.php", index)
		files[index] = path
		_, err = statement.Exec(path, index+10, index+20)
		require.NoError(b, err)
	}
	require.NoError(b, statement.Close())
	require.NoError(b, tx.Commit())

	ctx := context.Background()
	b.Run("combined", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			states, stale, scanErr := scanner.scanFileStates(ctx, files, true)
			if scanErr != nil {
				b.Fatal(scanErr)
			}
			if len(states) != fileCount || len(stale) != 0 {
				b.Fatalf(
					"unexpected snapshot sizes: states=%d stale=%d",
					len(states),
					len(stale),
				)
			}
		}
	})
	b.Run("separate_queries", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			stale, scanErr := benchmarkStaleFiles(scanner, ctx, files)
			if scanErr != nil {
				b.Fatal(scanErr)
			}
			states, scanErr := scanner.loadFileStates(ctx, files)
			if scanErr != nil {
				b.Fatal(scanErr)
			}
			if len(states) != fileCount || len(stale) != 0 {
				b.Fatalf(
					"unexpected snapshot sizes: states=%d stale=%d",
					len(states),
					len(stale),
				)
			}
		}
	})
}

func benchmarkStaleFiles(
	scanner *FileScanner,
	ctx context.Context,
	current []string,
) ([]string, error) {
	rows, err := scanner.db.QueryContext(
		ctx,
		"SELECT path FROM file_hashes ORDER BY path",
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stale []string
	currentIndex := 0
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		for currentIndex < len(current) &&
			current[currentIndex] < path {
			currentIndex++
		}
		if currentIndex >= len(current) || current[currentIndex] != path {
			stale = append(stale, path)
		}
	}
	return stale, rows.Err()
}

func TestFileScanner_NotifiesBatchIndexersOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "broken.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	idx := &batchControlledIndexer{
		controlledIndexer: controlledIndexer{err: errors.New("index failed")},
	}
	scanner, err := NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(idx)

	require.ErrorContains(
		t,
		scanner.IndexFiles(context.Background(), []string{filePath, filePath}),
		"index failed",
	)
	require.EqualValues(t, 1, idx.calls.Load(), "duplicate paths must only be indexed once")
	require.EqualValues(t, 1, idx.begins.Load())
	require.EqualValues(t, 1, idx.candidates.Load())
	require.EqualValues(t, 1, idx.ends.Load())
}

func TestFileScanner_PreparesBeforeWorkspaceMutation(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "prepared.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	store, err := NewStore(filepath.Join(tempDir, "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	idx := &preparingControlledIndexer{}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
		store,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(idx)

	require.NoError(t, scanner.IndexFiles(context.Background(), []string{filePath}))
	require.EqualValues(t, 1, idx.prepareCalls.Load())
	require.EqualValues(t, 1, idx.indexCalls.Load())
	require.False(t, idx.prepareHadMutation.Load())
	require.True(t, idx.indexHadMutation.Load())
	require.Equal(t, filePath+":prepared", idx.indexedValue)
	require.True(t, idx.syntaxAvailableDuringIndex)
	require.NotNil(t, idx.file)
	require.Nil(t, idx.file.syntaxTree)
	require.Nil(t, idx.file.lineIndex)
}

func TestFileScanner_DoesNotIndexAfterPreparationFailure(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "broken.php")
	require.NoError(t, os.WriteFile(filePath, []byte("<?php"), 0o644))

	idx := &preparingControlledIndexer{prepareErr: errors.New("prepare failed")}
	scanner, err := NewFileScanner(
		tempDir,
		filepath.Join(tempDir, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(idx)

	require.ErrorContains(
		t,
		scanner.IndexFiles(context.Background(), []string{filePath}),
		"prepare failed",
	)
	require.EqualValues(t, 1, idx.prepareCalls.Load())
	require.Zero(t, idx.indexCalls.Load())
	require.NotNil(t, idx.file)
	require.Nil(t, idx.file.syntaxTree)
	var tracked int
	require.NoError(t, scanner.db.QueryRow(
		"SELECT COUNT(*) FROM file_hashes WHERE path = ?",
		filePath,
	).Scan(&tracked))
	require.Zero(t, tracked)
}

func TestFileScanner_IndexFiles_PureGoParsers(t *testing.T) {
	tempDir := t.TempDir()
	twigPath := filepath.Join(tempDir, "Resources", "views", "storefront", "page.html.twig")
	jsonPath := filepath.Join(tempDir, "Resources", "snippet", "en-GB.json")
	scssPath := filepath.Join(tempDir, "Resources", "app", "storefront", "theme.scss")
	xmlPath := filepath.Join(tempDir, "Resources", "config", "services.xml")
	yamlPath := filepath.Join(tempDir, "Resources", "config", "services.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(twigPath), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(jsonPath), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(scssPath), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(xmlPath), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(yamlPath), 0755))
	require.NoError(t, os.WriteFile(twigPath, []byte(`{% block page %}content{% endblock %}`), 0644))
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"page":{"title":"Hello"}}`), 0644))
	require.NoError(t, os.WriteFile(scssPath, []byte(`.button { color: $sw-color-brand-primary; }`), 0644))
	require.NoError(t, os.WriteFile(xmlPath, []byte(`<container><services/></container>`), 0644))
	require.NoError(t, os.WriteFile(yamlPath, []byte("services:\n  App\\\\Service: ~\n"), 0644))

	mockIndexer := &mockIndexer{
		indexedFiles: make(map[string]bool),
	}
	fs, err := NewFileScanner(tempDir, filepath.Join(tempDir, "test.db"))
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, fs.Close())
	}()
	fs.AddIndexer(mockIndexer)

	require.NoError(t, fs.IndexFiles(context.Background(), []string{twigPath, jsonPath, scssPath, xmlPath, yamlPath}))
	for _, path := range []string{twigPath, jsonPath, scssPath, xmlPath, yamlPath} {
		assert.True(t, mockIndexer.indexedFiles[path])
	}
}

// Helper function to create test files
func createTestFiles(t *testing.T, baseDir string) {
	// Create directories and files for testing
	dirs := []string{
		"regular",
		".devenv",
		"node_modules",
		"vendor-bin",
		"tests",
		filepath.Join("nested", "node_modules"),
	}

	for _, dir := range dirs {
		err := os.MkdirAll(filepath.Join(baseDir, dir), 0755)
		require.NoError(t, err)

		// Create a PHP file in each directory
		filePath := filepath.Join(baseDir, dir, "file.php")
		err = os.WriteFile(filePath, []byte("<?php\n// Test file\n"), 0644)
		require.NoError(t, err)
	}
}

// Mock indexer for testing
type mockIndexer struct {
	mu           sync.Mutex
	indexedFiles map[string]bool
}

type supplementalMockIndexer struct {
	mockIndexer
	publicBuild    string
	inspectSyntax  bool
	syntaxPrepared bool
}

func (i *supplementalMockIndexer) ShouldEnterDirectory(path string) bool {
	path = filepath.Clean(path)
	root := filepath.Clean(i.publicBuild)
	return path == filepath.Dir(root) || path == root ||
		strings.HasPrefix(path, root+string(os.PathSeparator))
}

func (i *supplementalMockIndexer) ShouldIndexPath(path string) bool {
	root := filepath.Clean(i.publicBuild)
	path = filepath.Clean(path)
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func (i *supplementalMockIndexer) Index(file *ParsedFile) error {
	if i.inspectSyntax {
		i.syntaxPrepared = file.syntaxTree != nil
	}
	return i.mockIndexer.Index(file)
}

type controlledIndexer struct {
	err       error
	calls     atomic.Int64
	closes    atomic.Int64
	active    atomic.Int64
	maxActive atomic.Int64
	entered   chan struct{}
	release   chan struct{}
}

type repositoryIndexer struct {
	repository *DataIndexer[string]
	value      string
	err        error
	failPath   string
	removeErr  error
	clearErr   error
}

type batchControlledIndexer struct {
	controlledIndexer
	begins     atomic.Int64
	candidates atomic.Int64
	ends       atomic.Int64
}

type preparingControlledIndexer struct {
	prepareErr                 error
	prepareCalls               atomic.Int64
	indexCalls                 atomic.Int64
	prepareHadMutation         atomic.Bool
	indexHadMutation           atomic.Bool
	indexedValue               string
	file                       *ParsedFile
	syntaxAvailableDuringIndex bool
}

func (i *preparingControlledIndexer) ID() string {
	return "preparing"
}

func (i *preparingControlledIndexer) Prepare(file *ParsedFile) (any, error) {
	i.prepareCalls.Add(1)
	i.prepareHadMutation.Store(file.Mutation() != nil)
	i.file = file
	if i.prepareErr != nil {
		return nil, i.prepareErr
	}
	return file.Path + ":prepared", nil
}

func (i *preparingControlledIndexer) IndexPrepared(
	file *ParsedFile,
	prepared any,
) error {
	i.indexCalls.Add(1)
	i.indexHadMutation.Store(file.Mutation() != nil)
	i.indexedValue, _ = prepared.(string)
	i.syntaxAvailableDuringIndex = file.SyntaxTree() != nil
	return nil
}

func (i *preparingControlledIndexer) Index(*ParsedFile) error {
	return errors.New("unprepared index path used")
}

func (i *preparingControlledIndexer) RemovedFiles([]string) error {
	return nil
}

func (i *preparingControlledIndexer) Close() error {
	return nil
}

func (i *preparingControlledIndexer) Clear() error {
	return nil
}

func (i *batchControlledIndexer) BeginIndexingBatch(candidateFiles []string) {
	i.begins.Add(1)
	i.candidates.Store(int64(len(candidateFiles)))
}

func (i *batchControlledIndexer) EndIndexingBatch() error {
	i.ends.Add(1)
	return nil
}

func (i *repositoryIndexer) ID() string { return i.value }

func (i *repositoryIndexer) Index(file *ParsedFile) error {
	if err := i.repository.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]string{file.Path: {"value": i.value}},
	); err != nil {
		return err
	}
	if i.failPath == "" || i.failPath == file.Path {
		return i.err
	}
	return nil
}

func (i *repositoryIndexer) RemovedFiles(paths []string) error {
	return i.repository.BatchDeleteByFilePaths(paths)
}

func (i *repositoryIndexer) RemovedFilesIn(paths []string, mutation *Mutation) error {
	if err := i.repository.BatchDeleteByFilePathsIn(mutation, paths); err != nil {
		return err
	}
	return i.removeErr
}

func (i *repositoryIndexer) Close() error { return i.repository.Close() }
func (i *repositoryIndexer) Clear() error { return i.repository.Clear() }

func (i *repositoryIndexer) ClearIn(mutation *Mutation) error {
	if err := i.repository.ClearIn(mutation); err != nil {
		return err
	}
	return i.clearErr
}

func (i *controlledIndexer) ID() string { return "controlled" }

func (i *controlledIndexer) Index(*ParsedFile) error {
	i.calls.Add(1)
	active := i.active.Add(1)
	defer i.active.Add(-1)
	for {
		maximum := i.maxActive.Load()
		if active <= maximum || i.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	if i.entered != nil {
		i.entered <- struct{}{}
		<-i.release
	}
	return i.err
}

func (i *controlledIndexer) RemovedFiles([]string) error { return nil }
func (i *controlledIndexer) Close() error {
	i.closes.Add(1)
	return nil
}
func (i *controlledIndexer) Clear() error { return nil }

func (m *mockIndexer) Index(file *ParsedFile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.indexedFiles[file.Path] = true
	return nil
}

func (m *mockIndexer) RemovedFiles(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, path := range paths {
		delete(m.indexedFiles, path)
	}
	return nil
}

func (m *mockIndexer) Name() string {
	return "mockIndexer"
}

func (m *mockIndexer) ID() string {
	return "mock"
}

func (m *mockIndexer) Close() error {
	return nil
}

func (m *mockIndexer) Clear() error {
	return nil
}
