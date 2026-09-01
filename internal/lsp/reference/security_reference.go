package reference

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SecurityReferenceProvider struct {
	index *security.Index
}

func NewSecurityReferenceProvider(
	index *security.Index,
) *SecurityReferenceProvider {
	return &SecurityReferenceProvider{index: index}
}

func (p *SecurityReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if current, found := security.ConfigReferenceAt(
		request.Node,
	); found {
		return p.configReferences(request, current)
	}
	current, ok := security.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
		request.SourceString(),
		offset,
	)
	if !ok {
		return nil, nil
	}
	attribute, found, err := p.index.Attribute(current.Name)
	if err != nil || !found {
		return nil, err
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	occurrences := make([]security.Occurrence, 0, len(attribute.Occurrences))
	for _, occurrence := range attribute.Occurrences {
		if occurrence.File == currentPath {
			continue
		}
		occurrences = append(occurrences, occurrence)
	}
	for _, occurrence := range security.OccurrencesInDocument(
		currentPath,
		request.Root,
		request.SourceString(),
	) {
		if strings.EqualFold(occurrence.Name, current.Name) {
			occurrences = append(occurrences, occurrence)
		}
	}

	seen := make(map[string]struct{}, len(occurrences))
	var result []protocol.Location
	for _, occurrence := range occurrences {
		if occurrence.Origin == security.OriginBuiltIn ||
			occurrence.Role == security.DeclarationOccurrence &&
				!request.Context.IncludeDeclaration {
			continue
		}
		var location protocol.Location
		var exists bool
		if occurrence.File == currentPath {
			location = protocol.Location{
				URI: request.TextDocument.URI,
				Range: securityReferenceRange(
					occurrence.Range,
					request.LineIndex,
				),
			}
			exists = true
		} else {
			location, exists = securityOccurrenceLocation(occurrence)
		}
		if !exists {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result, nil
}

func (p *SecurityReferenceProvider) configReferences(
	request *lsp.ReferenceRequest,
	current security.ConfigOccurrence,
) ([]protocol.Location, error) {
	symbol, found, err := p.index.ConfigSymbol(current.Name, current.Kind)
	if err != nil {
		return nil, err
	}
	currentPath, _ := uriutil.Path(request.TextDocument.URI)
	var occurrences []security.ConfigOccurrence
	if found {
		for _, occurrence := range symbol.Occurrences {
			if occurrence.File != currentPath {
				occurrences = append(occurrences, occurrence)
			}
		}
	}
	for _, occurrence := range security.ConfigOccurrencesInDocument(
		currentPath,
		request.Root,
	) {
		if occurrence.Kind == current.Kind &&
			strings.EqualFold(occurrence.Name, current.Name) {
			occurrences = append(occurrences, occurrence)
		}
	}

	seen := make(map[string]struct{}, len(occurrences))
	var result []protocol.Location
	for _, occurrence := range occurrences {
		if occurrence.Role == security.ConfigDeclaration &&
			!request.Context.IncludeDeclaration {
			continue
		}
		var location protocol.Location
		var exists bool
		if occurrence.File == currentPath {
			location = protocol.Location{
				URI: request.TextDocument.URI,
				Range: securityReferenceRange(
					occurrence.Range,
					request.LineIndex,
				),
			}
			exists = true
		} else {
			location, exists = securityConfigOccurrenceLocation(occurrence)
		}
		if !exists {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result, nil
}

func securityOccurrenceLocation(
	occurrence security.Occurrence,
) (protocol.Location, bool) {
	source, err := os.ReadFile(occurrence.File)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(occurrence.File),
		Range: securityReferenceRange(
			occurrence.Range,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func securityConfigOccurrenceLocation(
	occurrence security.ConfigOccurrence,
) (protocol.Location, bool) {
	source, err := os.ReadFile(occurrence.File)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{
		URI: uriutil.FileURI(occurrence.File),
		Range: securityReferenceRange(
			occurrence.Range,
			cst.NewLineIndex(string(source)),
		),
	}, true
}

func securityReferenceRange(
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
