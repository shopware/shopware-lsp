package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type rootComposerManifest struct {
	Require      map[string]any  `json:"require"`
	RequireDev   map[string]any  `json:"require-dev"`
	Repositories json.RawMessage `json:"repositories"`
}

type composerRepository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// isRootPathPackage reports whether the package in packageDir is installed by
// the workspace root from a local Composer path repository. Such packages are
// project-local sources, for example custom/static-plugins, and are not
// distributed through a remote channel like the Shopware Store.
//
// Covering the directory with a path repository is not enough: the default
// Shopware project template registers custom/plugins/* as well, where Store
// plugins live. The root manifest must also require the package.
func isRootPathPackage(root, packageDir, packageName string) bool {
	packageName = strings.ToLower(strings.TrimSpace(packageName))
	if root == "" || packageDir == "" || packageName == "" {
		return false
	}
	content, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if err != nil {
		return false
	}
	var manifest rootComposerManifest
	if json.Unmarshal(content, &manifest) != nil {
		return false
	}
	if !composerRequires(manifest.Require, packageName) &&
		!composerRequires(manifest.RequireDev, packageName) {
		return false
	}
	packageDir = filepath.Clean(packageDir)
	for _, repository := range composerRepositories(manifest.Repositories) {
		if repository.Type != "path" || strings.TrimSpace(repository.URL) == "" {
			continue
		}
		pattern := filepath.FromSlash(strings.TrimRight(repository.URL, "/"))
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, pattern)
		}
		if composerPathMatches(pattern, packageDir) {
			return true
		}
	}
	return false
}

func composerRequires(requirements map[string]any, packageName string) bool {
	for name := range requirements {
		if strings.ToLower(name) == packageName {
			return true
		}
	}
	return false
}

// composerRepositories accepts both the list form and the keyed object form
// of the repositories setting.
func composerRepositories(raw json.RawMessage) []composerRepository {
	var list []composerRepository
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var keyed map[string]composerRepository
	if json.Unmarshal(raw, &keyed) != nil {
		return nil
	}
	result := make([]composerRepository, 0, len(keyed))
	for _, repository := range keyed {
		result = append(result, repository)
	}
	return result
}

// composerPathMatches mirrors Composer's glob(GLOB_BRACE | GLOB_MARK) lookup
// of path repository URLs for a single candidate directory.
func composerPathMatches(pattern, dir string) bool {
	for _, expanded := range expandBraces(pattern) {
		if matched, err := filepath.Match(filepath.Clean(expanded), dir); err == nil && matched {
			return true
		}
	}
	return false
}

func expandBraces(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return []string{pattern}
	}
	end := strings.IndexByte(pattern[start:], '}')
	if end < 0 {
		return []string{pattern}
	}
	end += start
	var result []string
	for _, option := range strings.Split(pattern[start+1:end], ",") {
		result = append(result, expandBraces(pattern[:start]+option+pattern[end+1:])...)
	}
	return result
}
