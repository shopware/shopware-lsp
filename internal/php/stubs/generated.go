package stubs

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs/catalog"
)

//go:embed phpstorm-stubs.msgpack
var generatedCatalogData []byte

var (
	generatedCatalogOnce   sync.Once
	generatedCatalog       catalog.Catalog
	generatedCatalogErr    error
	generatedExtensionOnce sync.Once
	generatedExtensions    map[string]string
)

func generatedSymbols(version project.Version, path string) []semantic.Symbol {
	generatedCatalogOnce.Do(func() {
		generatedCatalog, generatedCatalogErr = catalog.Decode(generatedCatalogData)
	})
	if generatedCatalogErr != nil {
		panic(generatedCatalogErr)
	}
	return generatedCatalog.Materialize(version, path)
}

func generatedSymbolsForExtensions(
	version project.Version,
	path string,
	extensions []string,
) []semantic.Symbol {
	loadGeneratedCatalog()
	return generatedCatalog.MaterializeForExtensions(version, path, extensions)
}

func generatedContracts() []semantic.CallContract {
	generatedCatalogOnce.Do(func() {
		generatedCatalog, generatedCatalogErr = catalog.Decode(generatedCatalogData)
	})
	if generatedCatalogErr != nil {
		panic(generatedCatalogErr)
	}
	return generatedCatalog.MaterializeContracts()
}

func generatedContractsForExtensions(extensions []string) []semantic.CallContract {
	loadGeneratedCatalog()
	return generatedCatalog.MaterializeContractsForExtensions(extensions)
}

func loadGeneratedCatalog() {
	generatedCatalogOnce.Do(func() {
		generatedCatalog, generatedCatalogErr = catalog.Decode(generatedCatalogData)
	})
	if generatedCatalogErr != nil {
		panic(generatedCatalogErr)
	}
}

func generatedSymbolExtension(name string) (string, bool) {
	loadGeneratedCatalog()
	generatedExtensionOnce.Do(func() {
		generatedExtensions = make(
			map[string]string,
			len(generatedCatalog.ExtensionSymbols),
		)
		for _, symbol := range generatedCatalog.ExtensionSymbols {
			if symbol.Extension == "" || symbol.Name == "" {
				continue
			}
			generatedExtensions[strings.ToLower(symbol.Name)] =
				project.NormalizeExtension(symbol.Extension)
		}
	})
	key := strings.ToLower(strings.TrimPrefix(name, "\\"))
	extension, found := generatedExtensions[key]
	if !found {
		if separator := strings.Index(key, "::"); separator >= 0 {
			extension, found = generatedExtensions[key[:separator]]
		}
	}
	return extension, found
}

// ExtensionForSymbol reports the generated runtime bundle owning a global or
// class-member symbol, even when that bundle is not materialized for the
// current project.
func ExtensionForSymbol(name string) (string, bool) {
	return generatedSymbolExtension(name)
}

func generatedSource() (repository, commit string) {
	generatedCatalogOnce.Do(func() {
		generatedCatalog, generatedCatalogErr = catalog.Decode(generatedCatalogData)
	})
	if generatedCatalogErr != nil {
		panic(generatedCatalogErr)
	}
	return generatedCatalog.Repository, generatedCatalog.Commit
}
