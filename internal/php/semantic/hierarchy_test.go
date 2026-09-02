package semantic

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

func TestSnapshotReverseHierarchyIndexesDirectEdgesAndMethodOverrides(t *testing.T) {
	t.Parallel()
	snapshot := NewSnapshot(1, []*Document{{
		Path: "/project/Hierarchy.php",
		Symbols: []Symbol{
			classSymbol("contract", InterfaceSymbol, "App\\Contract", nil, nil),
			methodSymbol("contract.execute", "contract", "execute", Public),
			classSymbol("trait", TraitSymbol, "App\\Reusable", nil, nil),
			methodSymbol("trait.execute", "trait", "execute", Public),
			classSymbol("base", ClassSymbol, "App\\Base", nil, []string{"App\\Contract"}),
			classSymbol("child", ClassSymbol, "App\\Child", []string{"App\\Base"}, nil),
			{
				ID:             "child.execute",
				Kind:           MethodSymbol,
				Name:           "execute",
				FullyQualified: "App\\Child::execute",
				Container:      "child",
				Path:           "/project/Hierarchy.php",
				Visibility:     Public,
				Range:          cst.TextRange{Start: 40, End: 50},
				SelectionRange: cst.TextRange{Start: 42, End: 49},
			},
			methodSymbol("child.private", "child", "hidden", Private),
			classSymbol("grand", ClassSymbol, "App\\GrandChild", []string{"App\\Child"}, nil),
			methodSymbol("grand.execute", "grand", "execute", Public),
			methodSymbol("grand.private", "grand", "hidden", Public),
		},
	}})

	// Add the trait relationship after construction so the compact fixture
	// remains readable.
	child, found := snapshot.Symbol("child")
	require.True(t, found)
	child.SetTraits([]string{"App\\Reusable"})
	snapshot = snapshot.WithUpdatedSymbols(&Document{Symbols: []Symbol{child}})
	// Updated symbol overlays intentionally share hierarchy keys. Publish a
	// declaration overlay to exercise hierarchy-name changes.
	childDocument := &Document{
		Path: "/project/Hierarchy.php",
		Symbols: []Symbol{
			classSymbol("contract", InterfaceSymbol, "App\\Contract", nil, nil),
			methodSymbol("contract.execute", "contract", "execute", Public),
			classSymbol("trait", TraitSymbol, "App\\Reusable", nil, nil),
			methodSymbol("trait.execute", "trait", "execute", Public),
			classSymbol("base", ClassSymbol, "App\\Base", nil, []string{"App\\Contract"}),
			child,
			methodSymbol("child.execute", "child", "execute", Public),
			methodSymbol("child.private", "child", "hidden", Private),
			classSymbol("grand", ClassSymbol, "App\\GrandChild", []string{"App\\Child"}, nil),
			methodSymbol("grand.execute", "grand", "execute", Public),
			methodSymbol("grand.private", "grand", "hidden", Public),
		},
	}
	snapshot = snapshot.WithDocument(childDocument)

	require.Equal(t, []string{"App\\Base"}, hierarchyNames(snapshot.DirectSubtypes("App\\Contract")))
	require.Equal(t, []string{"App\\Child"}, hierarchyNames(snapshot.DirectSubtypes("App\\Base")))
	require.Equal(t, []string{"App\\GrandChild"}, hierarchyNames(snapshot.DirectSubtypes("App\\Child")))
	require.Equal(t, []string{"App\\Child"}, hierarchyNames(snapshot.TraitConsumers("App\\Reusable")))
	require.Equal(t, []string{"App\\Base"}, hierarchyNames(snapshot.DirectSupertypes("child")))

	require.Equal(
		t,
		[]string{"App\\Child::execute", "App\\GrandChild::execute"},
		hierarchyNames(snapshot.MethodOverrides("contract.execute")),
	)
	require.Equal(
		t,
		[]string{"App\\Child::execute", "App\\GrandChild::execute"},
		hierarchyNames(snapshot.MethodOverrides("trait.execute")),
	)
	require.Equal(
		t,
		[]string{"App\\GrandChild::execute"},
		hierarchyNames(snapshot.MethodOverrides("child.execute")),
	)
	require.Empty(t, snapshot.MethodOverrides("child.private"))
}

func TestSnapshotReverseHierarchyHonorsDocumentOverlays(t *testing.T) {
	t.Parallel()
	basePath := "/project/Parents.php"
	childPath := "/project/Child.php"
	snapshot := NewSnapshot(1, []*Document{
		{
			Path: basePath,
			Symbols: []Symbol{
				classSymbol("base", ClassSymbol, "App\\Base", nil, nil),
				classSymbol("other", ClassSymbol, "App\\Other", nil, nil),
			},
		},
		{
			Path: childPath,
			Symbols: []Symbol{
				classSymbol("child", ClassSymbol, "App\\Child", []string{"App\\Base"}, nil),
			},
		},
	})
	require.Equal(t, []string{"App\\Child"}, hierarchyNames(snapshot.DirectSubtypes("App\\Base")))

	overlay := &Document{
		Path: childPath,
		Symbols: []Symbol{
			classSymbol("child", ClassSymbol, "App\\Child", []string{"App\\Other"}, nil),
		},
	}
	updated := snapshot.WithDocument(overlay)
	require.Empty(t, updated.DirectSubtypes("App\\Base"))
	require.Equal(t, []string{"App\\Child"}, hierarchyNames(updated.DirectSubtypes("App\\Other")))
}

func TestSnapshotIndexesClassAliasRelationships(t *testing.T) {
	t.Parallel()
	target := classSymbol("target", ClassSymbol, "App\\Target", nil, nil)
	alias := classSymbol("alias", ClassSymbol, "Legacy\\Alias", []string{"App\\Target"}, nil)
	alias.Flags = SyntheticFlag | ClassAliasFlag
	snapshot := NewSnapshot(1, []*Document{{
		Path:    "/project/Alias.php",
		Symbols: []Symbol{target, alias},
	}})

	require.Equal(t, []string{"Legacy\\Alias"}, hierarchyNames(snapshot.ClassAliases("App\\Target")))
	require.Empty(t, snapshot.DirectSubtypes("App\\Target"))
	resolved, found := snapshot.ClassAliasTarget("alias")
	require.True(t, found)
	require.Equal(t, "App\\Target", resolved.FullyQualified)
	require.True(t, snapshot.IsSubtypeOf("Legacy\\Alias", "App\\Target"))
	require.True(t, snapshot.IsSubtypeOf("App\\Target", "Legacy\\Alias"))
}

func classSymbol(
	id SymbolID,
	kind SymbolKind,
	name string,
	extends,
	implements []string,
) Symbol {
	symbol := Symbol{
		ID:             id,
		Kind:           kind,
		Name:           shortSemanticName(name),
		FullyQualified: name,
		Path:           "/project/Hierarchy.php",
		Range:          cst.TextRange{Start: 1, End: 10},
		SelectionRange: cst.TextRange{Start: 2, End: 9},
	}
	symbol.SetHierarchy(extends, implements, nil, nil, nil, nil, nil)
	return symbol
}

func methodSymbol(
	id,
	container SymbolID,
	name string,
	visibility Visibility,
) Symbol {
	className := "App\\" + stringsForSemanticID(container)
	return Symbol{
		ID:             id,
		Kind:           MethodSymbol,
		Name:           name,
		FullyQualified: className + "::" + name,
		Container:      container,
		Path:           "/project/Hierarchy.php",
		Visibility:     visibility,
		Range:          cst.TextRange{Start: 20, End: 30},
		SelectionRange: cst.TextRange{Start: 22, End: 29},
	}
}

func shortSemanticName(name string) string {
	for index := len(name) - 1; index >= 0; index-- {
		if name[index] == '\\' {
			return name[index+1:]
		}
	}
	return name
}

func stringsForSemanticID(id SymbolID) string {
	switch id {
	case "contract":
		return "Contract"
	case "trait":
		return "Reusable"
	case "base":
		return "Base"
	case "child":
		return "Child"
	case "grand":
		return "GrandChild"
	default:
		return string(id)
	}
}

func hierarchyNames(symbols []Symbol) []string {
	result := make([]string, len(symbols))
	for index := range symbols {
		result[index] = symbols[index].FullyQualified
	}
	return result
}
