package completion

import (
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

func completionRequestAt(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.CompletionRequest {
	request := consoleCompletionRequest(document, node)
	line, character := document.LineIndex.PositionUTF16(offset)
	request.Position.Line = int(line)
	request.Position.Character = int(character)
	return request
}
