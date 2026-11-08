package completion

import (
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func sortCompletionItems(items []protocol.CompletionItem) {
	slices.SortFunc(items, func(left, right protocol.CompletionItem) int {
		// Labels equal up to case ("error"/"Error") are ordered by the
		// label itself, so the result does not depend on input order.
		if order := strings.Compare(
			strings.ToLower(left.Label),
			strings.ToLower(right.Label),
		); order != 0 {
			return order
		}
		return strings.Compare(left.Label, right.Label)
	})
}
