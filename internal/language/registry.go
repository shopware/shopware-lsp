// Package language owns the registry of syntax frontends available to the
// application. It is deliberately independent of LSP and indexing so every
// consumer resolves files through the same extension-to-parser mapping.
package language

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

type ID string

const (
	JavaScript ID = "javascript"
	JSON       ID = "json"
	PHP        ID = "php"
	SCSS       ID = "scss"
	Twig       ID = "twig"
	Vue        ID = "vue"
	XML        ID = "xml"
	YAML       ID = "yaml"
)

type ParseResult struct {
	Tree   *cst.Tree
	Errors []parsekit.Error
}

type Parser func(source string) ParseResult

type Definition struct {
	ID         ID
	Extensions []string
	Parse      Parser
}

type Registry struct {
	byID        map[ID]Definition
	byExtension map[string]Definition
	extensions  []string
}

func NewRegistry(definitions ...Definition) (*Registry, error) {
	registry := &Registry{
		byID:        make(map[ID]Definition, len(definitions)),
		byExtension: make(map[string]Definition),
	}

	for _, definition := range definitions {
		if definition.ID == "" {
			return nil, fmt.Errorf("language ID must not be empty")
		}
		if definition.Parse == nil {
			return nil, fmt.Errorf("language %q has no parser", definition.ID)
		}
		if _, exists := registry.byID[definition.ID]; exists {
			return nil, fmt.Errorf("language %q is registered more than once", definition.ID)
		}

		normalized := definition
		normalized.Extensions = make([]string, 0, len(definition.Extensions))
		for _, extension := range definition.Extensions {
			extension = normalizeExtension(extension)
			if extension == "" {
				return nil, fmt.Errorf("language %q has an empty extension", definition.ID)
			}
			if existing, exists := registry.byExtension[extension]; exists {
				return nil, fmt.Errorf(
					"extension %q belongs to both %q and %q",
					extension,
					existing.ID,
					definition.ID,
				)
			}
			normalized.Extensions = append(normalized.Extensions, extension)
		}

		registry.byID[normalized.ID] = normalized
		for _, extension := range normalized.Extensions {
			registry.byExtension[extension] = normalized
			registry.extensions = append(registry.extensions, extension)
		}
	}

	slices.Sort(registry.extensions)
	return registry, nil
}

func (r *Registry) ByID(id ID) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.byID[id]
	return definition, ok
}

func (r *Registry) ByExtension(extension string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.byExtension[normalizeExtension(extension)]
	return definition, ok
}

func (r *Registry) ForPath(path string) (Definition, bool) {
	return r.ByExtension(filepath.Ext(path))
}

func (r *Registry) ParsePath(path, source string) (ID, ParseResult, bool) {
	definition, ok := r.ForPath(path)
	if !ok {
		return "", ParseResult{}, false
	}
	return definition.ID, definition.Parse(source), true
}

func (r *Registry) Extensions() []string {
	if r == nil {
		return nil
	}
	return slices.Clone(r.extensions)
}

func normalizeExtension(extension string) string {
	extension = strings.TrimSpace(strings.ToLower(extension))
	if extension == "" {
		return ""
	}
	if extension[0] != '.' {
		extension = "." + extension
	}
	return extension
}
