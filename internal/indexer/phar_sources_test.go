package indexer

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileScannerIndexesAndRefreshesPHARSources(t *testing.T) {
	projectRoot := t.TempDir()
	cacheRoot := t.TempDir()
	archivePath := filepath.Join(
		projectRoot,
		"vendor",
		"analysis",
		"analysis.phar",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(archivePath), 0o755))
	firstTime := time.Unix(1_700_000_000, 123)
	writeIndexerTestPHAR(t, archivePath, map[string][]byte{
		"src/Library.php": []byte("<?php class First {}"),
		"README.md":       []byte("not source"),
	})
	require.NoError(t, os.Chtimes(archivePath, firstTime, firstTime))

	// Composer-generated launchers can end in .phar without being archives.
	wrapperPath := filepath.Join(projectRoot, "vendor", "bin", "analysis.phar")
	require.NoError(t, os.MkdirAll(filepath.Dir(wrapperPath), 0o755))
	require.NoError(t, os.WriteFile(
		wrapperPath,
		[]byte("<?php require __DIR__.'/../analysis/analysis.phar';"),
		0o644,
	))

	recorder := newPHARRecordingIndexer()
	scanner, err := NewFileScanner(
		projectRoot,
		filepath.Join(cacheRoot, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(recorder)

	require.NoError(t, scanner.IndexAll(context.Background()))
	require.Equal(t, 1, recorder.callCount())
	firstSources := recorder.snapshot()
	require.Len(t, firstSources, 1)
	var sourcePath string
	for filePath, content := range firstSources {
		sourcePath = filePath
		require.Equal(t, "<?php class First {}", content)
	}
	require.True(t, pathWithin(scanner.pharCache, sourcePath))
	require.NotEqual(t, archivePath, sourcePath)

	// An unchanged warm scan must neither re-extract nor re-index the source.
	require.NoError(t, scanner.IndexAll(context.Background()))
	require.Equal(t, 1, recorder.callCount())

	secondTime := firstTime.Add(time.Second)
	writeIndexerTestPHAR(t, archivePath, map[string][]byte{
		"src/Library.php": []byte("<?php class Other {}"),
	})
	require.NoError(t, os.Chtimes(archivePath, secondTime, secondTime))
	require.NoError(t, scanner.IndexAll(context.Background()))
	require.Equal(t, 2, recorder.callCount())
	require.Equal(
		t,
		"<?php class Other {}",
		recorder.snapshot()[sourcePath],
	)
}

func TestFileScannerRebuildsIncompletePHARCache(t *testing.T) {
	projectRoot := t.TempDir()
	cacheRoot := t.TempDir()
	archivePath := filepath.Join(projectRoot, "library.phar")
	writeIndexerTestPHAR(t, archivePath, map[string][]byte{
		"src/One.php": []byte("<?php class One {}"),
		"src/Two.php": []byte("<?php class Two {}"),
	})

	scanner, err := NewFileScanner(
		projectRoot,
		filepath.Join(cacheRoot, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })

	first, err := scanner.discoverFiles(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.NoError(t, os.Remove(first[0]))

	second, err := scanner.discoverFiles(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, second)
	for _, filePath := range second {
		_, err := os.Stat(filePath)
		require.NoError(t, err)
	}
}

func TestFileScannerWatcherRefreshesPHARSources(t *testing.T) {
	projectRoot := t.TempDir()
	cacheRoot := t.TempDir()
	archivePath := filepath.Join(projectRoot, "library.phar")
	firstTime := time.Unix(1_700_000_000, 0)
	writeIndexerTestPHAR(t, archivePath, map[string][]byte{
		"src/Library.php": []byte("<?php class Before {}"),
	})
	require.NoError(t, os.Chtimes(archivePath, firstTime, firstTime))

	recorder := newPHARRecordingIndexer()
	scanner, err := NewFileScanner(
		projectRoot,
		filepath.Join(cacheRoot, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(recorder)
	require.NoError(t, scanner.IndexAll(context.Background()))
	require.NoError(t, scanner.StartWatcher())

	writeIndexerTestPHAR(t, archivePath, map[string][]byte{
		"src/Library.php": []byte("<?php class After {}"),
	})
	secondTime := firstTime.Add(time.Second)
	require.NoError(t, os.Chtimes(archivePath, secondTime, secondTime))

	require.Eventually(t, func() bool {
		if recorder.callCount() < 2 {
			return false
		}
		for _, content := range recorder.snapshot() {
			if content == "<?php class After {}" {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
}

func TestSafePHAREntryPath(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"file.php", "src/Nested/File.php"} {
		actual, err := safePHAREntryPath(valid)
		require.NoError(t, err)
		require.Equal(t, valid, actual)
	}
	for _, invalid := range []string{
		"",
		".",
		"../outside.php",
		"src/../../outside.php",
		"/absolute.php",
		`src\windows.php`,
		"C:/windows.php",
		"src/./file.php",
	} {
		_, err := safePHAREntryPath(invalid)
		require.Error(t, err, invalid)
	}
}

func TestRelativePathWithinUsesPathComponentBoundaries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	relative, within := relativePathWithin(
		root,
		filepath.Join(root, "src", "Example.php"),
	)
	require.True(t, within)
	require.Equal(t, filepath.Join("src", "Example.php"), relative)
	require.True(t, pathWithin(root, root))
	require.False(t, pathWithin(root, root+"-copy"))
	require.False(t, pathWithin(root, filepath.Dir(root)))
}

func TestFileScannerOnlyAcceptsWorkspaceAndPHARCachePaths(t *testing.T) {
	projectRoot := t.TempDir()
	cacheRoot := t.TempDir()
	scanner, err := NewFileScanner(
		projectRoot,
		filepath.Join(cacheRoot, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })

	require.True(t, scanner.shouldIndexPath(
		filepath.Join(projectRoot, "src", "File.php"),
	))
	require.False(t, scanner.shouldIndexPath(
		filepath.Join(filepath.Dir(projectRoot), "unrelated.php"),
	))
	require.True(t, scanner.shouldIndexPath(
		filepath.Join(scanner.pharCache, "archive", "files", "tests", "File.php"),
	))
	require.True(t, scanner.shouldPreparsePath(
		filepath.Join(scanner.pharCache, "archive", "files", "tests", "File.php"),
	))
}

type pharRecordingIndexer struct {
	mu      sync.Mutex
	calls   int
	sources map[string]string
}

func newPHARRecordingIndexer() *pharRecordingIndexer {
	return &pharRecordingIndexer{sources: make(map[string]string)}
}

func (indexer *pharRecordingIndexer) ID() string { return "phar-recording" }

func (indexer *pharRecordingIndexer) Index(file *ParsedFile) error {
	indexer.mu.Lock()
	defer indexer.mu.Unlock()
	indexer.calls++
	indexer.sources[file.Path] = string(file.Content)
	return nil
}

func (indexer *pharRecordingIndexer) RemovedFiles(paths []string) error {
	indexer.mu.Lock()
	defer indexer.mu.Unlock()
	for _, filePath := range paths {
		delete(indexer.sources, filePath)
	}
	return nil
}

func (indexer *pharRecordingIndexer) Close() error { return nil }
func (indexer *pharRecordingIndexer) Clear() error { return nil }

func (indexer *pharRecordingIndexer) callCount() int {
	indexer.mu.Lock()
	defer indexer.mu.Unlock()
	return indexer.calls
}

func (indexer *pharRecordingIndexer) snapshot() map[string]string {
	indexer.mu.Lock()
	defer indexer.mu.Unlock()
	snapshot := make(map[string]string, len(indexer.sources))
	for filePath, content := range indexer.sources {
		snapshot[filePath] = content
	}
	return snapshot
}

func writeIndexerTestPHAR(
	t *testing.T,
	filePath string,
	entries map[string][]byte,
) {
	t.Helper()

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)

	var entryManifest bytes.Buffer
	var contents bytes.Buffer
	for _, name := range names {
		content := entries[name]
		writeIndexerTestUint32(t, &entryManifest, uint32(len(name)))
		_, err := entryManifest.WriteString(name)
		require.NoError(t, err)
		writeIndexerTestUint32(t, &entryManifest, uint32(len(content)))
		writeIndexerTestUint32(t, &entryManifest, 1_700_000_000)
		writeIndexerTestUint32(t, &entryManifest, uint32(len(content)))
		writeIndexerTestUint32(
			t,
			&entryManifest,
			crc32.ChecksumIEEE(content),
		)
		writeIndexerTestUint32(t, &entryManifest, 0o100644)
		writeIndexerTestUint32(t, &entryManifest, 0)
		_, err = contents.Write(content)
		require.NoError(t, err)
	}

	var manifest bytes.Buffer
	writeIndexerTestUint32(t, &manifest, uint32(len(entries)))
	require.NoError(t, binary.Write(&manifest, binary.LittleEndian, uint16(0x0011)))
	writeIndexerTestUint32(t, &manifest, 0)
	writeIndexerTestUint32(t, &manifest, 0)
	writeIndexerTestUint32(t, &manifest, 0)
	_, err := manifest.Write(entryManifest.Bytes())
	require.NoError(t, err)

	var archive bytes.Buffer
	_, err = archive.WriteString("<?php __HALT_COMPILER(); ?>\r\n")
	require.NoError(t, err)
	writeIndexerTestUint32(t, &archive, uint32(manifest.Len()))
	_, err = archive.Write(manifest.Bytes())
	require.NoError(t, err)
	_, err = archive.Write(contents.Bytes())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filePath, archive.Bytes(), 0o644))
}

func writeIndexerTestUint32(
	t *testing.T,
	writer io.Writer,
	value uint32,
) {
	t.Helper()
	require.NoError(t, binary.Write(writer, binary.LittleEndian, value))
}
