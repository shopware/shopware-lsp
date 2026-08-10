package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestShopwarePHPSemanticInspectionDeclaresIndependentRules(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	definition := NewShopwarePHPSemantic(phpIndex).Definition()
	require.Equal(t, "shopware.php.semantic", definition.ID)
	require.Equal(t, []language.ID{language.PHP}, definition.Languages)
	require.Len(t, definition.Problems, 10)

	seen := make(map[lsp.DiagnosticID]struct{}, len(definition.Problems))
	for _, problem := range definition.Problems {
		_, duplicate := seen[problem.ID]
		require.False(t, duplicate, problem.ID)
		seen[problem.ID] = struct{}{}
	}
}
