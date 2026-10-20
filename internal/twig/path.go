package twig

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// storefrontDistMarker is the compiled storefront asset directory Shopware's
// TwigLoaderConfigCompilerPass registers as an extra path of the owning
// bundle's Twig namespace, alongside Resources/views.
const storefrontDistMarker = "/Resources/app/storefront/dist/"

// templateRootMarkers are the directories a Twig loader is pointed at. Anything
// below one of them is addressable as a template.
var templateRootMarkers = []string{
	"/Resources/views/",
	"/templates/",
	storefrontDistMarker,
}

// storefrontDistRelative returns the namespace-relative name of a file below
// a bundle's storefront dist root, without the bundle namespace itself.
func storefrontDistRelative(normalized string) (string, bool) {
	index := strings.LastIndex(normalized, storefrontDistMarker)
	if index < 0 {
		return "", false
	}
	return normalized[index+len(storefrontDistMarker):], true
}

// isStorefrontDistDirectory reports whether a directory is a storefront dist
// root or sits below one. The scanner skips every `dist` directory, so the
// Twig indexer uses this to reopen only the dist roots a loader serves.
func isStorefrontDistDirectory(directory string) bool {
	normalized := filepath.ToSlash(directory)
	return strings.HasSuffix(normalized, strings.TrimSuffix(storefrontDistMarker, "/")) ||
		strings.Contains(normalized, storefrontDistMarker)
}

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
	normalized := filepath.ToSlash(twigPath)
	if relative, ok := storefrontDistRelative(normalized); ok {
		if relative == "" {
			return ""
		}
		if bundle := getBundleNameByPath(normalized); bundle != "" && bundle != "unknown" {
			return fmt.Sprintf("@%s/%s", bundle, relative)
		}
		return ""
	}

	index := strings.Index(normalized, "Resources/views")
	if index != -1 {
		path := strings.TrimPrefix(strings.TrimPrefix(normalized[index:], "Resources/views"), "/")

		if path == "" {
			return ""
		}

		return fmt.Sprintf("@Storefront/%s", path)
	}

	// Files below no loader root have no portable template name. Deriving
	// one from the absolute path would leak it into template suggestions.
	if isAbsolutePath(normalized) {
		return ""
	}

	path := strings.TrimPrefix(normalized, "/")

	if path == "" {
		return ""
	}

	return fmt.Sprintf("@Storefront/%s", path)
}

func isAbsolutePath(normalized string) bool {
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	// Windows drive-letter paths, including the slash-normalized form.
	return len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/' &&
		((normalized[0] >= 'a' && normalized[0] <= 'z') ||
			(normalized[0] >= 'A' && normalized[0] <= 'Z'))
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

	// The dist root is registered only under its bundle namespace, so it
	// contributes exactly that one name and no un-namespaced alias.
	if relative, ok := storefrontDistRelative(normalized); ok {
		if relative != "" {
			if bundle := getBundleNameByPath(normalized); bundle != "" && bundle != "unknown" {
				add("@" + bundle + "/" + relative)
			}
		}
		return names
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
	normalized := filepath.ToSlash(twigPath)
	for _, marker := range []string{"/Resources/views/", storefrontDistMarker} {
		if index := strings.Index(normalized, marker); index != -1 {
			return bundleNameBefore(normalized, index)
		}
	}

	return "unknown"
}

// bundleNameBefore derives the bundle, plugin, app, or theme name from the
// directory owning a Resources root: the directory directly above
// Resources, or the one above `src` for bundles laid out as
// `<Bundle>/src/Resources`.
func bundleNameBefore(normalized string, index int) string {
	possiblePath := strings.Trim(normalized[:index], "/")

	if filepath.Base(possiblePath) == "src" {
		return filepath.Base(filepath.Dir(possiblePath))
	}

	return filepath.Base(possiblePath)
}
