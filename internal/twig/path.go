package twig

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

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
		possiblePath := strings.Trim(twigPath[:index], "/")

		if filepath.Base(possiblePath) == "src" {
			return filepath.Base(filepath.Dir(possiblePath))
		}

		return filepath.Base(possiblePath)
	}

	return "unknown"
}
