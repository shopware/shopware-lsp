package stubs

import (
	"sort"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

var alwaysLoadedExtensions = []string{
	"core",
	"date",
	"filter",
	"hash",
	"json",
	"pcre",
	"random",
	"reflection",
	"spl",
}

var extensionDependencies = map[string][]string{
	"dom":        {"libxml"},
	"simplexml":  {"libxml"},
	"xml":        {"libxml"},
	"xmlreader":  {"libxml"},
	"xmlwriter":  {"libxml"},
	"xsl":        {"dom", "libxml"},
	"pdo_sqlite": {"pdo", "sqlite3"},
}

// SelectedExtensions returns the deterministic runtime bundle closure for a
// project. PHP core bundles are always present; requested extensions bring in
// narrow declaration dependencies such as libxml and PDO.
func SelectedExtensions(requested []string, disabledLists ...[]string) []string {
	disabled := make(map[string]struct{})
	for _, list := range disabledLists {
		for _, extension := range list {
			extension = project.NormalizeExtension(extension)
			if extension != "" {
				disabled[extension] = struct{}{}
			}
		}
	}
	for _, extension := range alwaysLoadedExtensions {
		delete(disabled, extension)
	}
	selected := make(map[string]struct{}, len(alwaysLoadedExtensions)+len(requested))
	visiting := make(map[string]struct{})
	var add func(string) bool
	add = func(value string) bool {
		extension := project.NormalizeExtension(value)
		if extension == "" {
			return false
		}
		if _, exists := selected[extension]; exists {
			return true
		}
		if _, blocked := disabled[extension]; blocked {
			return false
		}
		if _, cycle := visiting[extension]; cycle {
			return false
		}
		visiting[extension] = struct{}{}
		for _, dependency := range extensionDependencies[extension] {
			if !add(dependency) {
				delete(visiting, extension)
				return false
			}
		}
		delete(visiting, extension)
		selected[extension] = struct{}{}
		return true
	}
	for _, extension := range alwaysLoadedExtensions {
		add(extension)
	}
	for _, extension := range requested {
		add(extension)
	}
	result := make([]string, 0, len(selected))
	for extension := range selected {
		result = append(result, extension)
	}
	sort.Strings(result)
	return result
}

func filterStubSymbols(
	symbols []semantic.Symbol,
	extensions []string,
) []semantic.Symbol {
	if extensions == nil {
		return symbols
	}
	selected := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		selected[project.NormalizeExtension(extension)] = struct{}{}
	}
	result := symbols[:0]
	for _, symbol := range symbols {
		extension, found := generatedSymbolExtension(symbol.FullyQualified)
		if found {
			if _, enabled := selected[extension]; !enabled {
				continue
			}
		}
		result = append(result, symbol)
	}
	return result
}
