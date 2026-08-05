package completion

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/httpclient"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type HttpClientCompletionProvider struct {
	phpIndex *php.PHPIndex
}

func NewHttpClientCompletionProvider(
	phpIndex *php.PHPIndex,
) *HttpClientCompletionProvider {
	return &HttpClientCompletionProvider{phpIndex: phpIndex}
}

func (p *HttpClientCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil
	}
	reference, found := httpclient.ReferenceAt(request.Node)
	if !found || !httpclient.Validate(
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
	used := httpclient.UsedOptionNames(reference)
	seen := make(map[string]struct{})
	var result []protocol.CompletionItem
	for _, option := range httpclient.Options(p.phpIndex) {
		if _, duplicate := seen[option.Name]; duplicate {
			continue
		}
		seen[option.Name] = struct{}{}
		if _, exists := used[option.Name]; exists {
			continue
		}
		detail := "Symfony HttpClient option"
		if !option.Type.IsUnknown() {
			detail += " · " + option.Type.String()
		}
		item := protocol.CompletionItem{
			Label:  option.Name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: detail,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: option.Name,
			},
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = fmt.Sprintf(
			"Default from `%s::OPTIONS_DEFAULTS`:\n\n```php\n%s\n```",
			httpclient.ClientInterface,
			option.Default,
		)
		result = append(result, item)
	}
	return result
}

func (p *HttpClientCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}

var _ lsp.CompletionProvider = (*HttpClientCompletionProvider)(nil)
