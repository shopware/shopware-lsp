package hover

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/httpclient"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type HttpClientHoverProvider struct {
	phpIndex *php.PHPIndex
}

func NewHttpClientHoverProvider(
	phpIndex *php.PHPIndex,
) *HttpClientHoverProvider {
	return &HttpClientHoverProvider{phpIndex: phpIndex}
}

func (p *HttpClientHoverProvider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	reference, found := httpclient.ReferenceAt(request.Node)
	if !found || reference.Name == "" || !httpclient.Validate(
		ctx,
		p.phpIndex,
		reference,
		request.DocumentContent,
	) {
		return nil, nil
	}
	for _, option := range httpclient.Options(p.phpIndex) {
		if option.Name != reference.Name {
			continue
		}
		optionType := "mixed"
		if !option.Type.IsUnknown() {
			optionType = option.Type.String()
		}
		startLine, startCharacter := request.LineIndex.PositionUTF16(
			reference.Range.Start,
		)
		endLine, endCharacter := request.LineIndex.PositionUTF16(
			reference.Range.End,
		)
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Symfony HttpClient option** `%s`\n\n"+
						"Default type: `%s`\n\n"+
						"Declared default:\n\n```php\n%s\n```",
					option.Name,
					optionType,
					option.Default,
				),
			},
			Range: &protocol.Range{
				Start: protocol.Position{
					Line:      int(startLine),
					Character: int(startCharacter),
				},
				End: protocol.Position{
					Line:      int(endLine),
					Character: int(endCharacter),
				},
			},
		}, nil
	}
	return nil, nil
}

var _ lsp.HoverProvider = (*HttpClientHoverProvider)(nil)
