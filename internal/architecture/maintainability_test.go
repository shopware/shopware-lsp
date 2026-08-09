package architecture_test

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const productionFileLineLimit = 2500

// TestProductionGoFilesStayFocused is a coarse regression guard, not a design
// metric. Files approaching this size must be split by responsibility or added
// to exceptions with a concrete reason and a follow-up plan.
func TestProductionGoFilesStayFocused(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	exceptions := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredRepositoryDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if reason := exceptions[filepath.ToSlash(relative)]; reason != "" {
			return nil
		}
		lines, generated, err := goFileLineCount(path)
		if err != nil {
			return err
		}
		if generated {
			return nil
		}
		if lines > productionFileLineLimit {
			t.Errorf(
				"%s has %d lines; split it by responsibility before exceeding the %d-line production-file guard",
				filepath.ToSlash(relative),
				lines,
				productionFileLineLimit,
			)
		}
		return nil
	})
	require.NoError(t, err)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func ignoredRepositoryDirectory(name string) bool {
	switch name {
	case ".git", "dist", "node_modules", "out", "third_party":
		return true
	default:
		return false
	}
}

func goFileLineCount(path string) (lines int, generated bool, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines++
		if lines <= 10 && strings.Contains(scanner.Text(), "Code generated") &&
			strings.Contains(scanner.Text(), "DO NOT EDIT") {
			generated = true
		}
	}
	return lines, generated, scanner.Err()
}
