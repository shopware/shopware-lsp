package definition

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigIncludeParameterDefinitionProvider struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTwigIncludeParameterDefinitionProvider(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TwigIncludeParameterDefinitionProvider {
	return &TwigIncludeParameterDefinitionProvider{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TwigIncludeParameterDefinitionProvider) GetDefinition(
	_ context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.twigIndex == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	parameter, found := twig.IncludeParameterAt(
		request.Root,
		request.Node,
		offset,
	)
	if !found {
		return nil
	}

	variables, _ := p.twigIndex.FindTemplateVariable(
		parameter.Name,
		parameter.Template,
	)
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	if templateMatchesTwigPath(parameter.Template, currentPath) {
		filtered := variables[:0]
		for _, variable := range variables {
			if variable.FilePath != currentPath {
				filtered = append(filtered, variable)
			}
		}
		variables = filtered
		for _, variable := range twig.TemplateInputVariablesInDocument(
			currentPath,
			request.Root,
		) {
			if variable.Name == parameter.Name {
				variables = append(variables, variable)
			}
		}
	}

	var locations []protocol.Location
	for _, variable := range variables {
		if variable.FilePath == currentPath {
			locations = append(locations, protocol.Location{
				URI: uriutil.FileURI(currentPath),
				Range: twigComponentProtocolRange(
					variable.Range,
					request.LineIndex,
				),
			})
			continue
		}
		if location, ok := componentFileLocation(
			variable.FilePath,
			variable.Range,
		); ok {
			locations = append(locations, location)
		}
	}
	if p.phpIndex != nil {
		phpVariables, _ := p.phpIndex.TwigTemplateVariableSources(
			parameter.Name,
			parameter.Template,
		)
		for _, variable := range phpVariables {
			if location, ok := componentFileLocation(
				variable.File,
				variable.Range,
			); ok {
				locations = append(locations, location)
			}
		}
	}
	if len(locations) == 0 {
		return nil
	}
	return uniqueComponentLocations(locations)
}

func templateMatchesTwigPath(template, path string) bool {
	for _, name := range twig.TemplateNames(path) {
		if name == template {
			return true
		}
	}
	return false
}
