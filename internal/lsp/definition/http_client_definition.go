package definition

import (
	"context"
	"os"

	"github.com/shopware/shopware-lsp/internal/httpclient"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type HttpClientDefinitionProvider struct {
	phpIndex *php.PHPIndex
}

func NewHttpClientDefinitionProvider(
	phpIndex *php.PHPIndex,
) *HttpClientDefinitionProvider {
	return &HttpClientDefinitionProvider{phpIndex: phpIndex}
}

func (p *HttpClientDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	reference, found := httpclient.ReferenceAt(request.Node)
	if !found || reference.Name == "" || !httpclient.Validate(
		ctx,
		p.phpIndex,
		reference,
		request.DocumentContent,
	) {
		return nil
	}
	var result []protocol.Location
	seen := make(map[string]struct{})
	for _, option := range httpclient.Options(p.phpIndex) {
		if option.Name != reference.Name {
			continue
		}
		key := option.File + ":" + option.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		content, err := os.ReadFile(option.File)
		if err != nil {
			continue
		}
		lineIndex := cst.NewLineIndex(string(content))
		startLine, startCharacter := lineIndex.PositionUTF16(
			option.Range.Start,
		)
		endLine, endCharacter := lineIndex.PositionUTF16(
			option.Range.End,
		)
		result = append(result, protocol.Location{
			URI: uriutil.FileURI(option.File),
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
		})
	}
	return result
}

var _ lsp.GotoDefinitionProvider = (*HttpClientDefinitionProvider)(nil)
