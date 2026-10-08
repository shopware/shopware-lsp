package completion

import (
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func sortCompletionItems(items []protocol.CompletionItem) {
	slices.SortFunc(items, func(left, right protocol.CompletionItem) int {
		return strings.Compare(
			strings.ToLower(left.Label),
			strings.ToLower(right.Label),
		)
	})
}
