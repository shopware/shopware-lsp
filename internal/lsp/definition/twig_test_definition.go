package definition

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *TwigDefinitionProvider) twigTestDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.DefinitionParams == nil || request.LineIndex == nil ||
		request.Root == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.TestExpressionAt(request.Root, offset)
	if !found {
		return nil
	}
	values, _ := p.twigIndexer.GetTwigTest(reference.Name)
	seen := make(map[string]struct{}, len(values))
	locations := make([]protocol.Location, 0, len(values))
	for _, value := range values {
		if value.FilePath == "" {
			continue
		}
		key := strings.ToLower(value.FilePath) + ":" +
			strconv.Itoa(value.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		line := value.Line - 1
		if line < 0 {
			line = 0
		}
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(value.FilePath),
			Range: protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			},
		})
	}
	return locations
}
