package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestPHPTypeExtensionInfersHeaderBagDefault(t *testing.T) {
	t.Parallel()
	customHeaderBag := semantic.Symbol{
		ID:             "custom-header-bag",
		Kind:           semantic.ClassSymbol,
		Name:           "CustomHeaderBag",
		FullyQualified: "App\\CustomHeaderBag",
		Path:           "/header-bag.php",
	}
	customHeaderBag.SetExtends([]string{
		"Symfony\\Component\\HttpFoundation\\HeaderBag",
	})
	document := &semantic.Document{
		Path: "/header-bag.php",
		Symbols: []semantic.Symbol{
			{
				ID:             "header-bag",
				Kind:           semantic.ClassSymbol,
				Name:           "HeaderBag",
				FullyQualified: "Symfony\\Component\\HttpFoundation\\HeaderBag",
				Path:           "/header-bag.php",
			},
			customHeaderBag,
		},
	}
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	extension := NewPHPTypeExtension(nil)

	fact, ok := extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "get",
		Receiver: types.Named("App\\CustomHeaderBag"),
		Arguments: []inference.CallArgument{
			{Type: types.LiteralString("CONTENT_TYPE")},
			{Type: types.LiteralString("")},
		},
	})
	require.True(t, ok)
	require.Equal(t, "string", fact.Type.String())

	_, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "get",
		Receiver: types.Named("App\\CustomHeaderBag"),
		Arguments: []inference.CallArgument{
			{Type: types.LiteralString("CONTENT_TYPE")},
			{Type: types.Int()},
		},
	})
	require.False(t, ok, "an invalid default must remain visible to signature diagnostics")
}
