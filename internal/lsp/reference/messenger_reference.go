package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type MessengerReferenceProvider struct {
	index    *messenger.Index
	phpIndex *php.PHPIndex
}

func NewMessengerReferenceProvider(
	index *messenger.Index,
	phpIndex *php.PHPIndex,
) *MessengerReferenceProvider {
	return &MessengerReferenceProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *MessengerReferenceProvider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		request == nil || request.Node == nil {
		return nil, nil
	}
	if reference, found := messenger.ReferenceAt(
		ctx,
		request.TextDocument.URI,
		request.Root,
		request.Node,
	); found {
		switch reference.Role {
		case messenger.ReferenceMessage:
			return p.messageLocations(
				reference.Name,
				request.Context.IncludeDeclaration,
			)
		case messenger.ReferenceHandlerMethod:
			return p.handlerLocations(
				reference.Class,
				reference.Name,
				request.Context.IncludeDeclaration,
			)
		}
	}
	if !strings.EqualFold(
		filepath.Ext(request.TextDocument.URI),
		".php",
	) {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return nil, nil
	}
	symbol, found := php.SymbolAt(
		phpContext.Document,
		phpContext.Snapshot,
		request.Node.RangeTrimmedTrivia().Start,
	)
	if !found {
		return nil, nil
	}
	switch symbol.Kind {
	case semantic.ClassSymbol,
		semantic.InterfaceSymbol,
		semantic.EnumSymbol:
		return p.messageLocations(
			symbol.FullyQualified,
			request.Context.IncludeDeclaration,
		)
	case semantic.MethodSymbol:
		class, exists := phpContext.Snapshot.Symbol(symbol.Container)
		if !exists {
			return nil, nil
		}
		return p.handlerLocations(
			class.FullyQualified,
			symbol.Name,
			request.Context.IncludeDeclaration,
		)
	default:
		return nil, nil
	}
}

func (p *MessengerReferenceProvider) messageLocations(
	name string,
	includeDeclaration bool,
) ([]protocol.Location, error) {
	message, found, err := p.index.GetMessage(name)
	if err != nil || !found {
		return nil, err
	}
	var result []protocol.Location
	for _, occurrence := range message.Occurrences {
		rng := occurrence.MessageRange
		if rng.Len() == 0 {
			rng = occurrence.Range
		}
		if location, exists := messengerLocation(
			occurrence.File,
			rng,
		); exists {
			result = append(result, location)
		}
	}
	if includeDeclaration {
		if symbol, exists := p.phpIndex.FindClass(name); exists {
			if location, found := messengerSymbolLocation(symbol); found {
				result = append(result, location)
			}
		}
	}
	return uniqueMessengerLocations(result), nil
}

func (p *MessengerReferenceProvider) handlerLocations(
	className,
	methodName string,
	includeDeclaration bool,
) ([]protocol.Location, error) {
	messages, err := p.index.MessagesForHandler(className, methodName)
	if err != nil {
		return nil, err
	}
	var result []protocol.Location
	for _, message := range messages {
		for _, occurrence := range message.Occurrences {
			rng := occurrence.MessageRange
			if occurrence.Kind == messenger.HandlerOccurrence &&
				strings.EqualFold(occurrence.Class, className) &&
				strings.EqualFold(occurrence.Method, methodName) &&
				occurrence.HandlerRange.Len() != 0 {
				rng = occurrence.HandlerRange
			}
			if rng.Len() == 0 {
				rng = occurrence.Range
			}
			if location, exists := messengerLocation(
				occurrence.File,
				rng,
			); exists {
				result = append(result, location)
			}
		}
	}
	if includeDeclaration {
		for _, method := range p.phpIndex.FindMethods(
			className,
			methodName,
		) {
			if location, found := messengerSymbolLocation(method); found {
				result = append(result, location)
			}
		}
	}
	return uniqueMessengerLocations(result), nil
}

func messengerSymbolLocation(
	symbol semantic.Symbol,
) (protocol.Location, bool) {
	rng := symbol.SelectionRange
	if rng.Len() == 0 {
		rng = symbol.Range
	}
	return messengerLocation(symbol.Path, rng)
}

func messengerLocation(
	path string,
	rng cst.TextRange,
) (protocol.Location, bool) {
	source, err := os.ReadFile(path)
	if err != nil {
		return protocol.Location{}, false
	}
	lineIndex := cst.NewLineIndex(string(source))
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(startLine),
				Character: int(startCharacter),
			},
			End: protocol.Position{
				Line:      int(endLine),
				Character: int(endCharacter),
			},
		},
	}, true
}

func uniqueMessengerLocations(
	values []protocol.Location,
) []protocol.Location {
	seen := make(map[string]struct{}, len(values))
	result := make([]protocol.Location, 0, len(values))
	for _, value := range values {
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			value.URI,
			value.Range.Start.Line,
			value.Range.Start.Character,
			value.Range.End.Line,
			value.Range.End.Character,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
