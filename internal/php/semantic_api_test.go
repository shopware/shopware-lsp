package php

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestClassCatalogViewsAreReusedAndInvalidatedBySnapshot(t *testing.T) {
	store := semantic.NewStore()
	store.Replace(&semantic.Document{
		Path: "/project/One.php",
		Symbols: []semantic.Symbol{{
			ID: "one", Kind: semantic.ClassSymbol,
			Name: "One", FullyQualified: "App\\One",
			Path: "/project/One.php",
		}},
	})
	index := &PHPIndex{semanticStore: store}

	first := index.ClassSymbolsView()
	second := index.ClassSymbolsView()
	require.Len(t, first, 1)
	require.Same(t, &first[0], &second[0])
	require.Equal(t, []string{"App\\One"}, index.ClassNamesView())

	store.Replace(&semantic.Document{
		Path: "/project/Two.php",
		Symbols: []semantic.Symbol{{
			ID: "two", Kind: semantic.ClassSymbol,
			Name: "Two", FullyQualified: "App\\Two",
			Path: "/project/Two.php",
		}},
	})
	require.Equal(t,
		[]string{"App\\One", "App\\Two"},
		index.ClassNamesView(),
	)
}
