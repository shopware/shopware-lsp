package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func addServiceWorkspaceSymbols(
	file *indexer.ParsedFile,
	services []Service,
	parameters []Parameter,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(services)+len(parameters))
	for _, service := range services {
		if service.ID == "" || service.Path == "" {
			continue
		}
		container := "Symfony service"
		if service.Class != "" {
			container += " · " + service.Class
		}
		rangeValue := indexer.WorkspaceSymbolRangeFromText(file, service.IDRange)
		if service.IDRange.Len() == 0 {
			rangeValue = indexer.WorkspaceSymbolRangeAtLine(service.Line)
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: service.ID, ContainerName: container,
			Aliases: []string{service.Class}, Path: service.Path,
			Domain: "symfony.service", Kind: indexer.WorkspaceSymbolObject,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    rangeValue,
		})
	}
	for _, parameter := range parameters {
		if parameter.Name == "" || parameter.Path == "" {
			continue
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: parameter.Name, ContainerName: "Symfony parameter",
			Path: parameter.Path, Domain: "symfony.parameter",
			Kind:     indexer.WorkspaceSymbolConstant,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(parameter.Line),
		})
	}
	file.AddWorkspaceSymbols(result...)
}

func addRouteWorkspaceSymbols(
	file *indexer.ParsedFile,
	routes []Route,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(routes))
	for _, route := range routes {
		if route.Name == "" || route.FilePath == "" {
			continue
		}
		detail := "ANY"
		if len(route.Methods) > 0 {
			detail = strings.Join(route.Methods, "|")
		}
		if route.Path != "" {
			detail += " " + route.Path
		}
		if route.Controller != "" {
			detail += " · " + route.Controller
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: route.Name, ContainerName: "Symfony route · " + detail,
			Aliases: []string{route.Path, route.Controller},
			Path:    route.FilePath, Domain: "symfony.route",
			Kind:     indexer.WorkspaceSymbolMethod,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(route.Line),
		})
	}
	file.AddWorkspaceSymbols(result...)
}
