package symfony

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// ServiceConfiguration is the statically knowable navigation model of one
// open Symfony service configuration document.
type ServiceConfiguration struct {
	Services   []Service
	Prototypes []ServicePrototype
}

// ServiceConfigurationInDocument parses current editor contents without
// publishing them to the workspace index. This lets code lenses reflect
// unsaved PHP, XML, and YAML service relationships.
func ServiceConfigurationInDocument(
	path string,
	tree *cst.Tree,
	lineIndex *cst.LineIndex,
) (ServiceConfiguration, error) {
	if tree == nil || tree.Root == nil || lineIndex == nil {
		return ServiceConfiguration{}, nil
	}
	var services []Service
	var prototypes []ServicePrototype
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		config, parseErr := parsePHPServiceConfigTree(
			path,
			tree.Root,
			lineIndex,
		)
		services, prototypes, err =
			config.Services, config.Prototypes, parseErr
	case ".xml":
		services, _, prototypes, err = parseXMLServiceConfigTree(
			path,
			tree,
			lineIndex,
		)
	case ".yaml", ".yml":
		services, _, prototypes, err = parseYAMLServiceConfigTree(
			path,
			tree,
			lineIndex,
		)
	}
	return ServiceConfiguration{
		Services:   services,
		Prototypes: prototypes,
	}, err
}

// ServiceDeclarations returns source-backed declarations for a service ID.
// Expanded prototype services point back to the prototype declaration.
func (idx *ServiceIndex) ServiceDeclarations(
	id string,
) ([]Service, error) {
	if idx == nil || id == "" {
		return nil, nil
	}
	result, err := idx.serviceIndex.GetValues(id)
	if err != nil {
		return nil, err
	}
	prototypes, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}
	for _, service := range prototypes {
		if strings.EqualFold(service.ID, id) {
			result = append(result, service)
		}
	}
	sortServicesBySource(result)
	return result, nil
}

// ServiceDefinitions returns explicit source definitions. Generated prototype
// services are excluded because they cannot declare parent/decorator edges.
func (idx *ServiceIndex) ServiceDefinitions() ([]Service, error) {
	if idx == nil {
		return nil, nil
	}
	result, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	sortServicesBySource(result)
	return result, nil
}

// PrototypeClasses resolves a prototype against the current immutable PHP
// workspace snapshot, including resource/exclude patterns.
func (idx *ServiceIndex) PrototypeClasses(
	prototype ServicePrototype,
) []semantic.Symbol {
	if idx == nil || idx.phpIndex == nil {
		return nil
	}
	var result []semantic.Symbol
	for _, class := range idx.phpIndex.ClassSymbols() {
		if prototypeMatchesClass(prototype, class) {
			result = append(result, class)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Path != result[right].Path {
			return result[left].Path < result[right].Path
		}
		return result[left].SelectionRange.Start <
			result[right].SelectionRange.Start
	})
	return result
}

func sortServicesBySource(services []Service) {
	sort.Slice(services, func(left, right int) bool {
		if services[left].Path != services[right].Path {
			return services[left].Path < services[right].Path
		}
		if services[left].Line != services[right].Line {
			return services[left].Line < services[right].Line
		}
		return strings.ToLower(services[left].ID) <
			strings.ToLower(services[right].ID)
	})
}
