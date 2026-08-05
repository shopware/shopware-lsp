package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) documentColors(
	ctx context.Context,
	params *protocol.DocumentColorParams,
) ([]protocol.ColorInformation, error) {
	if params == nil {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &DocumentColorRequest{DocumentColorParams: params, Document: document}
	seen := make(map[protocol.Range]struct{})
	var result []protocol.ColorInformation
	for _, provider := range s.documentColorProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		colors, err := provider.GetDocumentColors(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("document color provider %T: %w", provider, err)
		}
		for _, color := range colors {
			if !validProtocolRange(color.Range) {
				continue
			}
			if _, exists := seen[color.Range]; exists {
				continue
			}
			seen[color.Range] = struct{}{}
			result = append(result, color)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return protocolRangeLess(result[i].Range, result[j].Range)
	})
	return result, nil
}

func (s *Server) colorPresentations(
	ctx context.Context,
	params *protocol.ColorPresentationParams,
) ([]protocol.ColorPresentation, error) {
	if params == nil {
		return nil, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	request := &ColorPresentationRequest{
		ColorPresentationParams: params,
		Document:                document,
	}
	seen := make(map[string]struct{})
	var result []protocol.ColorPresentation
	for _, provider := range s.documentColorProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		presentations, err := provider.GetColorPresentations(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("color presentation provider %T: %w", provider, err)
		}
		for _, presentation := range presentations {
			key := presentation.Label
			if presentation.TextEdit != nil {
				key += fmt.Sprintf("\x00%v\x00%s", presentation.TextEdit.Range, presentation.TextEdit.NewText)
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, presentation)
		}
	}
	return result, nil
}

func validProtocolRange(value protocol.Range) bool {
	return value.Start.Line >= 0 && value.Start.Character >= 0 &&
		value.End.Line >= 0 && value.End.Character >= 0 &&
		(value.Start.Line < value.End.Line ||
			(value.Start.Line == value.End.Line &&
				value.Start.Character <= value.End.Character))
}

func protocolRangeLess(left, right protocol.Range) bool {
	if left.Start.Line != right.Start.Line {
		return left.Start.Line < right.Start.Line
	}
	if left.Start.Character != right.Start.Character {
		return left.Start.Character < right.Start.Character
	}
	if left.End.Line != right.End.Line {
		return left.End.Line < right.End.Line
	}
	return left.End.Character < right.End.Character
}
