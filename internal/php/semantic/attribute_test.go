package semantic

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestSymbolMetadataIsCompactAndCopyOnWrite(t *testing.T) {
	var symbol Symbol
	require.LessOrEqual(t, unsafe.Sizeof(symbol), uintptr(272))
	require.Zero(t, symbol.metadata)
	require.Nil(t, symbol.Attributes())
	require.Nil(t, symbol.ConstantArray())
	require.Empty(t, symbol.DocSummary())

	symbol.SetMetadata(
		[]Attribute{{Name: "Route"}},
		[]ConstantArrayItem{{Key: "one", Value: "1"}},
		"Original summary.",
	)
	require.NotZero(t, symbol.metadata)

	copied := symbol
	copied.SetDocSummary("Changed summary.")
	copied.SetAttributes(nil)
	require.Equal(t, "Original summary.", symbol.DocSummary())
	require.Equal(t, "Route", symbol.Attributes()[0].Name)
	require.Equal(t, "Changed summary.", copied.DocSummary())
	require.Nil(t, copied.Attributes())
	require.Equal(t, "one", copied.ConstantArray()[0].Key)

	copied.SetMetadata(nil, nil, "")
	require.Zero(t, copied.metadata)

	attributes := []Attribute{{Name: "NoAllocation"}}
	allocations := testing.AllocsPerRun(1000, func() {
		var candidate Symbol
		candidate.SetMetadata(attributes, nil, "Summary")
		runtime.KeepAlive(candidate)
	})
	require.Zero(t, allocations)
}

func TestAttributeNamedMatchesQualifiedAndShortNames(t *testing.T) {
	t.Parallel()
	attributes := []Attribute{{Name: `\JetBrains\PhpStorm\NoReturn\`}}

	for _, name := range []string{
		"NoReturn",
		"noreturn",
		`JetBrains\PhpStorm\NoReturn`,
		`\JETBRAINS\PHPSTORM\NORETURN\`,
	} {
		_, found := AttributeNamed(attributes, name)
		require.True(t, found, name)
	}
	_, found := AttributeNamed(attributes, `Other\NoReturn`)
	require.False(t, found)
}

func TestSymbolViewAttributesPreserveSnapshotLayers(t *testing.T) {
	t.Parallel()
	const symbolID SymbolID = "target"
	baseSymbol := Symbol{
		ID: symbolID, Kind: FunctionSymbol, Name: "target", Path: "/base.php",
	}
	baseSymbol.SetAttributes([]Attribute{{Name: "BaseAttribute"}})
	base := NewSnapshot(1, []*Document{{
		Path:    "/base.php",
		Symbols: []Symbol{baseSymbol},
	}})
	view, found := base.SymbolView(symbolID)
	require.True(t, found)
	require.Equal(t, "BaseAttribute", view.Attributes()[0].Name)

	openSymbol := Symbol{
		ID: "open-target", Kind: FunctionSymbol,
		Name: "openTarget", Path: "/open.php",
	}
	openSymbol.SetAttributes([]Attribute{{Name: "OpenAttribute"}})
	overlay := base.WithDocument(&Document{
		Path:    "/open.php",
		Symbols: []Symbol{openSymbol},
	})
	view, found = overlay.SymbolView("open-target")
	require.True(t, found)
	require.Equal(t, "OpenAttribute", view.Attributes()[0].Name)
}

func TestDeprecationOfNamedAttributeArguments(t *testing.T) {
	t.Parallel()
	details, found := DeprecationOf([]Attribute{{
		Name: "JetBrains\\PhpStorm\\Deprecated",
		Arguments: []AttributeArgument{
			{
				Name:  "replacement",
				Value: AttributeValue{Kind: AttributeValueString, Value: "next()"},
			},
			{
				Name:  "since",
				Value: AttributeValue{Kind: AttributeValueString, Value: "2.0"},
			},
			{
				Name:  "reason",
				Value: AttributeValue{Kind: AttributeValueString, Value: "Legacy API"},
			},
		},
	}})
	require.True(t, found)
	require.Equal(t, Deprecation{
		Reason:      "Legacy API",
		Replacement: "next()",
		Since:       "2.0",
	}, details)
}
