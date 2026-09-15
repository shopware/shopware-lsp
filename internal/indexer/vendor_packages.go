package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// VendorPackages reports Composer packages that a nested vendor directory
// duplicates from the workspace root.
//
// The root vendor directory must stay indexable: in a Flex project it is where
// shopware/core actually lives. Extensions checked out with their own
// composer install carry a second copy of packages the workspace already
// provides, which would otherwise be indexed as a duplicate of core.
//
// A package counts as provided by the workspace when the root vendor directory
// contains it, or when the root manifest declares it through name or replace.
// The monorepo needs the manifest source: it replaces shopware/core and ships
// it from src/Core, so the root vendor directory never contains it.
//
// The zero value reports nothing as duplicated. It is safe for concurrent
// scanner and watcher use.
type VendorPackages struct {
	root     string
	replaced map[string]bool

	mu     sync.RWMutex
	cached map[string]bool
}

type vendorManifest struct {
	Name    string         `json:"name"`
	Replace map[string]any `json:"replace"`
}

// NewVendorPackages reads the root manifest once so later lookups only stat
// candidate package directories.
func NewVendorPackages(projectRoot string) *VendorPackages {
	packages := &VendorPackages{
		root:     filepath.Clean(projectRoot),
		replaced: map[string]bool{},
		cached:   map[string]bool{},
	}

	content, err := os.ReadFile(filepath.Join(packages.root, "composer.json"))
	if err != nil {
		return packages
	}
	var manifest vendorManifest
	if json.Unmarshal(content, &manifest) != nil {
		return packages
	}
	if name := normalizePackageName(manifest.Name); name != "" {
		packages.replaced[name] = true
	}
	for name := range manifest.Replace {
		if normalized := normalizePackageName(name); normalized != "" {
			packages.replaced[normalized] = true
		}
	}
	return packages
}

// DuplicatesRootPackage reports whether a workspace-relative path sits inside a
// nested vendor package that the workspace root already provides.
func (p *VendorPackages) DuplicatesRootPackage(relativePath string) bool {
	if p == nil || p.root == "" {
		return false
	}
	candidate := nestedVendorPackage(relativePath)
	if candidate == "" {
		return false
	}

	p.mu.RLock()
	duplicate, known := p.cached[candidate]
	p.mu.RUnlock()
	if known {
		return duplicate
	}

	duplicate = p.provides(candidate)

	p.mu.Lock()
	p.cached[candidate] = duplicate
	p.mu.Unlock()
	return duplicate
}

func (p *VendorPackages) provides(candidate string) bool {
	if p.replaced[candidate] {
		return true
	}
	_, err := os.Stat(filepath.Join(p.root, "vendor", filepath.FromSlash(candidate)))
	return err == nil
}

// nestedVendorPackage returns the vendor-relative package prefix for a path
// below a vendor directory that is not the workspace root's own, and an empty
// string for anything else. Paths keep at most two segments after the vendor
// directory so lookups stay per package rather than per file.
func nestedVendorPackage(relativePath string) string {
	relativePath = normalizeRelativePath(relativePath)
	if relativePath == "" {
		return ""
	}

	segments := strings.Split(relativePath, "/")
	for position, segment := range segments {
		if segment != "vendor" {
			continue
		}
		// The workspace root's own vendor directory is not nested.
		if position == 0 {
			return ""
		}
		remainder := segments[position+1:]
		if len(remainder) == 0 {
			return ""
		}
		return strings.Join(remainder[:min(len(remainder), 2)], "/")
	}
	return ""
}

func normalizePackageName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
