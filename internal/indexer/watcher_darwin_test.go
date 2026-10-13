//go:build darwin && cgo

package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileScannerDarwinWatcherKeepsDescriptorUseBounded(t *testing.T) {
	projectRoot := t.TempDir()
	for directory := 0; directory < 8; directory++ {
		dir := filepath.Join(projectRoot, "src", string(rune('a'+directory)))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for file := 0; file < 64; file++ {
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, "file-"+strconv.Itoa(file)+".php"),
				[]byte("<?php"),
				0o644,
			))
		}
	}

	scanner, err := NewFileScanner(
		projectRoot,
		filepath.Join(t.TempDir(), "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	before := openDescriptorCount(t)
	require.NoError(t, scanner.StartWatcher())
	after := openDescriptorCount(t)

	assert.LessOrEqual(t, after-before, 16,
		"FSEvents should not open one descriptor per project file")
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	directory, err := os.Open("/dev/fd")
	require.NoError(t, err)
	defer func() { require.NoError(t, directory.Close()) }()
	entries, err := directory.Readdirnames(-1)
	require.NoError(t, err)
	return len(entries)
}

func TestDarwinWatcherMapsCanonicalPathsToWorkspace(t *testing.T) {
	scanner := &FileScanner{projectRoot: "/var/workspace"}
	for _, test := range []struct{ path, want string }{
		{"/private/var/workspace/src/example.php", "/var/workspace/src/example.php"},
		{"/private/var/workspace", "/var/workspace"},
		{"/private/var/workspace/deleted/file.php", "/var/workspace/deleted/file.php"},
		{"/private/var/workspace-other/file.php", "/private/var/workspace-other/file.php"},
	} {
		assert.Equal(t, test.want, scanner.fseventWorkspacePath(test.path, "/private/var/workspace"))
	}
}

func TestDarwinWatcherUpdatesAndRemovesFilesThroughSymlinkRoot(t *testing.T) {
	realRoot := t.TempDir()
	root := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.Symlink(realRoot, root))
	path := filepath.Join(root, "example.php")
	require.NoError(t, os.WriteFile(path, []byte("<?php // initial"), 0644))
	scanner, err := NewFileScanner(root, filepath.Join(t.TempDir(), "scanner.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	mock := &mockIndexer{indexedFiles: make(map[string]bool)}
	scanner.AddIndexer(mock)
	require.NoError(t, scanner.IndexFiles(context.Background(), []string{path}))
	require.NoError(t, scanner.StartWatcher())
	mock.mu.Lock()
	delete(mock.indexedFiles, path)
	mock.mu.Unlock()
	require.NoError(t, os.WriteFile(path, []byte("<?php // changed content"), 0644))
	require.Eventually(t, func() bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return len(mock.indexedFiles) == 1 && mock.indexedFiles[path]
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, os.Remove(path))
	require.Eventually(t, func() bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return len(mock.indexedFiles) == 0
	}, 3*time.Second, 20*time.Millisecond)
}
