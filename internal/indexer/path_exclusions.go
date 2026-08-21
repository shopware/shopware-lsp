package indexer

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/pathmatch"
)

const excludedDirectoryProbe = ".shopware-lsp-descendant"

// PathExclusions matches workspace-relative paths excluded by configuration.
// It is immutable and safe for concurrent scanner and watcher use.
type PathExclusions struct {
	matcher pathmatch.Matcher
}

func NewPathExclusions(patterns []string) (PathExclusions, error) {
	matcher, err := pathmatch.Compile(patterns)
	if err != nil {
		return PathExclusions{}, err
	}
	return PathExclusions{matcher: matcher}, nil
}

// Excludes reports whether a workspace-relative file or directory is excluded.
func (e PathExclusions) Excludes(relativePath string) bool {
	relativePath = normalizeRelativePath(relativePath)
	return relativePath != "" && e.matcher.Match(relativePath)
}

// ExcludesDirectory reports whether a directory itself or all of its immediate
// descendants are excluded. The descendant probe makes patterns ending in /**
// pruneable without incorrectly pruning for file-only patterns such as **/*.php.
func (e PathExclusions) ExcludesDirectory(relativePath string) bool {
	relativePath = normalizeRelativePath(relativePath)
	return relativePath != "" && (e.matcher.Match(relativePath) ||
		e.matcher.Match(relativePath+"/"+excludedDirectoryProbe))
}

func normalizeRelativePath(relativePath string) string {
	relativePath = filepath.ToSlash(strings.ReplaceAll(relativePath, `\`, "/"))
	relativePath = strings.TrimPrefix(relativePath, "./")
	if relativePath == "." {
		return ""
	}
	return strings.Trim(relativePath, "/")
}
