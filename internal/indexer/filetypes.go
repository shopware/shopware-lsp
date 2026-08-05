package indexer

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
)

// IsScannedPath reports whether path belongs to a language included in normal
// workspace scans. Callers that walk a directory should also apply
// ShouldSkipRelativePath to directory entries.
func IsScannedPath(path string) bool {
	const pharPHPSuffix = ".phar.php"
	if len(path) >= len(pharPHPSuffix) &&
		strings.EqualFold(path[len(path)-len(pharPHPSuffix):], pharPHPSuffix) {
		return false
	}
	// Plain HTML uses the Twig frontend for open-document framework features,
	// but large generated frontend trees commonly contain thousands of HTML
	// artifacts. Keep those documents available on demand without adding them
	// to the persistent workspace scan; Symfony template usages are normally
	// stored in *.twig files.
	if strings.EqualFold(filepath.Ext(path), ".html") {
		return false
	}
	_, ok := language.DefaultRegistry().ForPath(path)
	return ok
}

func isScannedPath(path string) bool {
	return IsScannedPath(path)
}
