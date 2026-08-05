package symfonyconfig

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResourceFiles resolves a statically known Symfony configuration resource
// relative to the declaring config file. Files matched by globs are returned
// in deterministic order; directories and missing targets are excluded.
func ResourceFiles(currentPath, resourcePath string) []string {
	if currentPath == "" || resourcePath == "" ||
		strings.ContainsRune(resourcePath, '\x00') {
		return nil
	}
	target := resourcePath
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(currentPath), target)
	}
	target = filepath.Clean(target)

	var candidates []string
	if strings.ContainsAny(target, "*?[") {
		candidates, _ = filepath.Glob(target)
	} else {
		candidates = []string{target}
	}
	var result []string
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		result = append(result, filepath.Clean(candidate))
	}
	sort.Strings(result)
	return result
}
