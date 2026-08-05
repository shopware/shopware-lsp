package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// ServiceRelatedCodeLensProvider ports the reference plugin's decorator,
// parent, and prototype line markers across PHP, XML, and YAML service config.
type ServiceRelatedCodeLensProvider struct {
	services *symfony.ServiceIndex
	php      *php.PHPIndex
}

func NewServiceRelatedCodeLensProvider(
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ServiceRelatedCodeLensProvider {
	return &ServiceRelatedCodeLensProvider{
		services: services,
		php:      phpIndex,
	}
}

func (p *ServiceRelatedCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.services == nil || p.php == nil ||
		request == nil || request.CodeLensParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".xml", ".yaml", ".yml":
	default:
		return nil, nil
	}
	current, err := symfony.ServiceConfigurationInDocument(
		path,
		request.Document.SyntaxTree,
		request.Document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	if len(current.Services) == 0 && len(current.Prototypes) == 0 {
		return nil, nil
	}
	indexed, err := p.services.ServiceDefinitions()
	if err != nil {
		return nil, err
	}
	definitions := make([]symfony.Service, 0, len(indexed)+len(current.Services))
	for _, service := range indexed {
		if filepath.Clean(service.Path) != filepath.Clean(path) {
			definitions = append(definitions, service)
		}
	}
	definitions = append(definitions, current.Services...)

	var result []protocol.CodeLens
	for _, service := range current.Services {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rng := serviceCodeLensRange(
			service.IDRange,
			service.Line,
			request.Document.LineIndex,
		)
		result = append(
			result,
			serviceForwardLenses(rng, service, definitions)...,
		)
		result = append(
			result,
			serviceReverseLenses(rng, service, definitions)...,
		)
	}
	for _, prototype := range current.Prototypes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var targets []string
		for _, class := range p.services.PrototypeClasses(prototype) {
			rng := class.SelectionRange
			if rng.Len() == 0 {
				rng = class.Range
			}
			targets = append(targets, relatedTarget(
				class.Path,
				relatedSourceLine(class.Path, rng.Start),
			))
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open prototype class"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d prototype classes",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			serviceCodeLensRange(
				prototype.NamespaceRange,
				prototype.Line,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	sortRelatedServiceCodeLenses(result)
	return result, nil
}

func serviceForwardLenses(
	rng protocol.Range,
	service symfony.Service,
	definitions []symfony.Service,
) []protocol.CodeLens {
	var result []protocol.CodeLens
	for _, relation := range []struct {
		target string
		title  string
	}{
		{target: service.Decorates, title: "Open decorated service"},
		{target: service.Parent, title: "Open parent service"},
	} {
		targets := serviceDefinitionTargets(
			definitions,
			relation.target,
			"",
		)
		if len(targets) != 0 {
			result = append(result, relatedLens(
				rng,
				relation.title,
				targets,
			))
		}
	}
	return result
}

func serviceReverseLenses(
	rng protocol.Range,
	service symfony.Service,
	definitions []symfony.Service,
) []protocol.CodeLens {
	var decorators []string
	var children []string
	for _, candidate := range definitions {
		if strings.EqualFold(candidate.Decorates, service.ID) {
			decorators = append(
				decorators,
				serviceSourceTarget(candidate),
			)
		}
		if strings.EqualFold(candidate.Parent, service.ID) {
			children = append(children, serviceSourceTarget(candidate))
		}
	}
	decorators = uniqueRelatedTargets(decorators)
	children = uniqueRelatedTargets(children)
	var result []protocol.CodeLens
	if len(decorators) != 0 {
		title := "Open decorating service"
		if len(decorators) > 1 {
			title = fmt.Sprintf(
				"Open %d decorating services",
				len(decorators),
			)
		}
		result = append(result, relatedLens(rng, title, decorators))
	}
	if len(children) != 0 {
		title := "Open child service"
		if len(children) > 1 {
			title = fmt.Sprintf(
				"Open %d child services",
				len(children),
			)
		}
		result = append(result, relatedLens(rng, title, children))
	}
	return result
}

func serviceDefinitionTargets(
	definitions []symfony.Service,
	id,
	excludePath string,
) []string {
	if id == "" {
		return nil
	}
	var result []string
	for _, service := range definitions {
		if !strings.EqualFold(service.ID, id) ||
			excludePath != "" &&
				filepath.Clean(service.Path) == filepath.Clean(excludePath) {
			continue
		}
		result = append(result, serviceSourceTarget(service))
	}
	return uniqueRelatedTargets(result)
}

func serviceSourceTarget(service symfony.Service) string {
	return relatedTarget(service.Path, service.Line)
}

func serviceCodeLensRange(
	rng cst.TextRange,
	line int,
	lineIndex *cst.LineIndex,
) protocol.Range {
	if rng.Len() != 0 {
		return relatedProtocolRange(rng, lineIndex)
	}
	if line < 1 {
		line = 1
	}
	position := protocol.Position{Line: line - 1}
	return protocol.Range{Start: position, End: position}
}

func sortRelatedServiceCodeLenses(result []protocol.CodeLens) {
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
}

func (p *ServiceRelatedCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
