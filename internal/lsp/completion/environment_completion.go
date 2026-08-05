package completion

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type EnvironmentCompletionProvider struct {
	index *environment.Index
}

func NewEnvironmentCompletionProvider(
	index *environment.Index,
) *EnvironmentCompletionProvider {
	return &EnvironmentCompletionProvider{index: index}
}

func (p *EnvironmentCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.LineIndex == nil || request.CompletionParams == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := environment.PHPCompletionReferenceAt(
		request.Node,
		offset,
	)
	if !found {
		reference, found = environment.CompletionReferenceAt(
			string(request.DocumentContent),
			offset,
		)
	}
	if !found {
		return nil
	}
	variables, err := p.index.Variables()
	if err != nil {
		return nil
	}
	startLine, startCharacter := request.LineIndex.PositionUTF16(
		reference.NameRange.Start,
	)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		reference.NameRange.End,
	)
	result := make([]protocol.CompletionItem, 0, len(variables))
	for _, variable := range variables {
		if len(variable.Declarations) == 0 {
			continue
		}
		result = append(result, protocol.CompletionItem{
			Label: variable.Name,
			Kind:  int(protocol.VariableCompletion),
			Detail: fmt.Sprintf(
				"Symfony environment variable · %d declaration(s)",
				len(variable.Declarations),
			),
			TextEdit: protocol.TextEdit{
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
				NewText: variable.Name,
			},
		})
	}
	return result
}

func (p *EnvironmentCompletionProvider) GetTriggerCharacters() []string {
	return []string{"(", ":"}
}
