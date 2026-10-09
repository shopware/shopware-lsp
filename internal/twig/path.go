package twig

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// templateRootMarkers are the directories a Twig loader is pointed at. Anything
// below one of them is addressable as a template.
var templateRootMarkers = []string{"/Resources/views/", "/templates/"}

// IsTemplateAssetPath reports whether a file lives under a template root while
// carrying an extension other than .twig. Twig loaders serve whatever sits
// below their paths — Symfony's profiler includes .svg views — so whether a
// template exists must not depend on it being parseable Twig.
func IsTemplateAssetPath(templatePath string) bool {
	normalized := filepath.ToSlash(templatePath)
	extension := filepath.Ext(normalized)
	// An extension-less entry is a directory, a symlink to one, or a tooling
	// file. A template root can hold any of those, and reading one as a
	// template fails the whole index run.
	if extension == "" || strings.EqualFold(extension, ".twig") ||
		strings.HasPrefix(filepath.Base(normalized), ".") ||
		isExcludedTwigPath(normalized) {
		return false
	}
	for _, marker := range templateRootMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func ConvertToRelativePath(twigPath string) string {
	index := strings.Index(twigPath, "Resources/views")
	if index != -1 {
		path := strings.TrimPrefix(strings.TrimPrefix(twigPath[index:], "Resources/views"), "/")

		if path == "" {
			return ""
		}

		return fmt.Sprintf("@Storefront/%s", path)
	}

	path := strings.TrimPrefix(twigPath, "/")

	if path == "" {
		return ""
	}

	return fmt.Sprintf("@Storefront/%s", path)
}

// isExcludedTwigPath reports whether a file uses Twig syntax without being a
// template the Symfony loader can resolve: administration sources, migration
// fixtures, and phpDocumentor's own templates.
func isExcludedTwigPath(templatePath string) bool {
	normalized := filepath.ToSlash(templatePath)
	return strings.Contains(normalized, "Resources/app/administration") ||
		strings.Contains(normalized, "Migration/Fixtures") ||
		strings.Contains(normalized, ".phpdoc/template")
}

// TemplateNames returns the portable names by which Twig can address a file.
// It preserves Shopware's @Storefront convention while also indexing standard
// Symfony templates/ paths and bundle namespaces.
func TemplateNames(twigPath string) []string {
	normalized := filepath.ToSlash(twigPath)
	var names []string
	add := func(name string) {
		name = strings.TrimPrefix(name, "/")
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	if marker := "/Resources/views/"; strings.Contains(normalized, marker) {
		index := strings.LastIndex(normalized, marker)
		relative := normalized[index+len(marker):]
		add(relative)
		add(ConvertToRelativePath(normalized))
		if bundle := getBundleNameByPath(normalized); bundle != "" && bundle != "unknown" {
			add("@" + bundle + "/" + relative)
			add(legacyBundleTemplateName(bundle, relative))
		}
	}
	if marker := "/templates/"; strings.Contains(normalized, marker) {
		index := strings.LastIndex(normalized, marker)
		add(normalized[index+len(marker):])
	}
	if len(names) == 0 {
		add(ConvertToRelativePath(normalized))
	}
	return names
}

func legacyBundleTemplateName(bundle, relative string) string {
	relative = strings.TrimPrefix(filepath.ToSlash(relative), "/")
	directory, file := filepath.ToSlash(filepath.Dir(relative)), filepath.Base(relative)
	if directory == "." || directory == "" {
		return bundle + "::" + file
	}
	return bundle + ":" + directory + ":" + file
}

func getBundleNameByPath(twigPath string) string {
	index := strings.Index(twigPath, "Resources/views")
	if index != -1 {
		possiblePath := strings.TrimSuffix(twigPath[:index], "/")
		bundleRoot := possiblePath
		if filepath.Base(possiblePath) == "src" {
			bundleRoot = filepath.Dir(possiblePath)
		}
		if name := composerPluginBundleName(bundleRoot); name != "" {
			return name
		}

		return filepath.Base(strings.Trim(bundleRoot, "/"))
	}

	return "unknown"
}
