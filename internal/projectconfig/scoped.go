package projectconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Scope is one nested diagnostics-only project configuration. Root is the
// directory containing .config and therefore owns every file below it.
type Scope struct {
	Root          string  `json:"root"`
	Path          string  `json:"path"`
	Configuration Partial `json:"configuration,omitempty"`
	Error         string  `json:"error,omitempty"`
}

var scopedDiscoverySkipDirectories = map[string]bool{
	".git": true, ".idea": true, ".vscode": true,
	".github": true, ".gitlab": true, ".run": true,
	"node_modules": true, "vendor": true, "vendor-bin": true,
	"var": true, "cache": true, "dist": true, "build": true,
	"public": true, "tests": true, "test": true, "fixtures": true,
	"_fixtures": true, "coverage": true, "out": true,
	".tmp": true, ".direnv": true, ".devenv": true,
}

// LoadScopes discovers diagnostics-only configurations below workspaceRoot.
// The workspace-root configuration itself remains owned by Load.
func LoadScopes(workspaceRoot string) ([]Scope, error) {
	workspaceRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve configuration workspace: %w", err)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	seen := make(map[string]bool)
	var result []Scope
	visitedDirectories := make(map[string]bool)
	if realRoot, evalErr := filepath.EvalSymlinks(workspaceRoot); evalErr == nil {
		visitedDirectories[filepath.Clean(realRoot)] = true
	}
	var walkDirectories func(string) error
	walkDirectories = func(directory string) error {
		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == ".config" {
				configPath := Path(directory)
				if _, statErr := os.Stat(configPath); statErr == nil && directory != workspaceRoot &&
					!seen[directory] {
					seen[directory] = true
					result = append(result, loadScope(directory, configPath))
				} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
					return statErr
				}
				continue
			}
			if scopedDiscoverySkipDirectories[name] {
				continue
			}
			child := filepath.Join(directory, name)
			isDirectory := entry.IsDir()
			if !isDirectory && entry.Type()&os.ModeSymlink != 0 {
				if info, statErr := os.Stat(child); statErr == nil {
					isDirectory = info.IsDir()
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return statErr
				}
			}
			if isDirectory {
				if entry.Type()&os.ModeSymlink != 0 {
					realChild, evalErr := filepath.EvalSymlinks(child)
					if evalErr != nil {
						return evalErr
					}
					realChild = filepath.Clean(realChild)
					if visitedDirectories[realChild] {
						continue
					}
					visitedDirectories[realChild] = true
				}
				if err := walkDirectories(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	walkErr := walkDirectories(workspaceRoot)
	if walkErr != nil {
		return nil, fmt.Errorf("discover nested Shopware LSP configurations: %w", walkErr)
	}
	slices.SortFunc(result, func(left, right Scope) int {
		if depth := pathDepth(left.Root) - pathDepth(right.Root); depth != 0 {
			return depth
		}
		return strings.Compare(left.Root, right.Root)
	})
	return result, nil
}

func loadScope(root, path string) Scope {
	result := Scope{Root: root, Path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		result.Configuration, err = Decode(data)
	}
	if err == nil {
		err = ValidateNested(result.Configuration)
	}
	if err != nil {
		result.Error = fmt.Sprintf("%s: %v", path, err)
	}
	return result
}

// ValidateNested rejects settings whose behavior cannot be scoped safely to a
// nested extension repository.
func ValidateNested(value Partial) error {
	if value.PHP != nil || value.Shopware != nil || len(value.Features) != 0 ||
		value.Indexing != nil || len(value.Domains) != 0 || value.Check != nil {
		return errors.New("nested configuration may only contain diagnostics")
	}
	return nil
}

// ScopeErrors joins every invalid nested configuration for strict CLI startup.
func ScopeErrors(values []Scope) error {
	var result []error
	for _, value := range values {
		if value.Error != "" {
			result = append(result, errors.New(value.Error))
		}
	}
	return errors.Join(result...)
}

// Contains reports whether candidate belongs to root without resolving
// symlinks, preserving the workspace-visible path used by editor URIs.
func Contains(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pathDepth(path string) int {
	path = filepath.Clean(path)
	return strings.Count(filepath.ToSlash(path), "/")
}
