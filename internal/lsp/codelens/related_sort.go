package codelens

import (
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func sortRelatedCodeLenses(result []protocol.CodeLens) {
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
}
