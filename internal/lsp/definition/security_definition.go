package definition

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SecurityDefinitionProvider struct {
	index *security.Index
}

func NewSecurityDefinitionProvider(
	index *security.Index,
) *SecurityDefinitionProvider {
	return &SecurityDefinitionProvider{index: index}
}

func (p *SecurityDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if current, found := security.ConfigReferenceAt(
		request.Node,
	); found {
		return p.configDefinitions(request, current)
	}
	reference, ok := security.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
		string(request.DocumentContent),
		offset,
	)
	if !ok {
		return nil
	}
	attribute, found, err := p.index.Attribute(reference.Name)
	if err != nil || !found {
		return nil
	}
	var result []protocol.Location
	for _, occurrence := range attribute.Declarations() {
		if occurrence.Origin == security.OriginBuiltIn {
			continue
		}
		if location, exists := consoleLocation(
			occurrence.File,
			occurrence.Range,
		); exists {
			result = append(result, location)
		}
	}
	return uniqueEventLocations(result)
}

func securityDefinitionRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func (p *SecurityDefinitionProvider) configDefinitions(
	request *lsp.DefinitionRequest,
	current security.ConfigOccurrence,
) []protocol.Location {
	symbol, found, err := p.index.ConfigSymbol(current.Name, current.Kind)
	if err != nil {
		return nil
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	var declarations []security.ConfigOccurrence
	if found {
		for _, occurrence := range symbol.Declarations() {
			if occurrence.File != currentPath {
				declarations = append(declarations, occurrence)
			}
		}
	}
	for _, occurrence := range security.ConfigOccurrencesInDocument(
		currentPath,
		request.Root,
	) {
		if occurrence.Role == security.ConfigDeclaration &&
			occurrence.Kind == current.Kind &&
			strings.EqualFold(occurrence.Name, current.Name) {
			declarations = append(declarations, occurrence)
		}
	}
	var result []protocol.Location
	for _, declaration := range declarations {
		if declaration.File == currentPath {
			result = append(result, protocol.Location{
				URI: request.TextDocument.URI,
				Range: securityDefinitionRange(
					declaration.Range,
					request.LineIndex,
				),
			})
			continue
		}
		if location, exists := consoleLocation(
			declaration.File,
			declaration.Range,
		); exists {
			result = append(result, location)
		}
	}
	return uniqueEventLocations(result)
}
