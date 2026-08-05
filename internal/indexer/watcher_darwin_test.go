//go:build darwin && cgo

package indexer

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
