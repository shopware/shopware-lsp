package completion

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type ConsoleHelperCompletionProvider struct {
	phpIndex *php.PHPIndex
	catalog  *console.HelperCatalog
}

func NewConsoleHelperCompletionProvider(
	phpIndex *php.PHPIndex,
) *ConsoleHelperCompletionProvider {
	return &ConsoleHelperCompletionProvider{
		phpIndex: phpIndex,
		catalog:  console.NewHelperCatalog(phpIndex),
	}
}

func (p *ConsoleHelperCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil
	}
	reference, found := console.HelperReferenceAt(request.Node)
	if !found || !console.ValidateHelperReference(
		ctx,
		p.phpIndex,
		reference,
		request.DocumentContent,
	) {
		return nil
	}
	startLine, startCharacter := request.LineIndex.PositionUTF16(
		reference.Range.Start,
	)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		reference.Range.End,
	)
	editRange := protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
	var result []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, helper := range p.catalog.Helpers() {
		if _, duplicate := seen[helper.Name]; duplicate {
			continue
		}
		seen[helper.Name] = struct{}{}
		item := protocol.CompletionItem{
			Label:  helper.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: helper.Class,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: helper.Name,
			},
		}
		if helper.Summary != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = helper.Summary
		}
		result = append(result, item)
	}
	return result
}

func (p *ConsoleHelperCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}

var _ lsp.CompletionProvider = (*ConsoleHelperCompletionProvider)(nil)
