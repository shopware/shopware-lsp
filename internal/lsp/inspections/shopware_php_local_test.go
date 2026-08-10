package inspections

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/stretchr/testify/require"
)

func TestShopwarePHPLocalInspectionDeclaresAndReportsRules(t *testing.T) {
	inspection := NewShopwarePHPLocal()
	definition := inspection.Definition()
	require.Equal(t, "shopware.php.local", definition.ID)
	require.Equal(t, []language.ID{language.PHP}, definition.Languages)
	require.Len(t, definition.Problems, 9)

	document := lsp.NewTextDocument(
		"file:///LocalRules.php",
		"<?php $_GET['value'];",
		1,
	)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(
		context.Background(),
		document,
		collector,
	))
	require.Len(t, collector.problems, 1)
	require.Equal(t, diagnostics.ShopwarePHPSuperglobalCode, collector.problems[0].ID)
}
